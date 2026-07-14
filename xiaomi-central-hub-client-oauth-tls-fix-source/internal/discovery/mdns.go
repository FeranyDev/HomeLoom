package discovery

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	Service = "_miot-central._tcp.local."
)

// Gateway is a central-hub candidate advertised through mDNS/DNS-SD.
type Gateway struct {
	Instance    string   `json:"instance"`
	HostName    string   `json:"host_name"`
	Addresses   []string `json:"addresses"`
	Port        int      `json:"port"`
	DID         string   `json:"did,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	Role        int      `json:"role,omitempty"`
	MQTTEnabled bool     `json:"mqtt_enabled"`
	TXT         []string `json:"txt"`
}

// BrowseOptions controls which network interfaces are used and optionally
// exposes diagnostics. InterfaceNames may contain OS interface names such as
// en0, eth0, br-lan, or Ethernet. When empty, all suitable IPv4 multicast
// interfaces are queried.
type BrowseOptions struct {
	InterfaceNames []string
	QueryInterval  time.Duration
	Debugf         func(format string, args ...any)
}

type srvRecord struct {
	port   int
	target string
}

type records struct {
	instances map[string]bool
	srv       map[string]srvRecord
	txt       map[string][]string
	addresses map[string]map[string]bool
}

type mdnsSocket struct {
	iface net.Interface
	conn  *net.UDPConn
}

type mdnsPacket struct {
	iface string
	from  string
	data  []byte
}

type dnsQuestion struct {
	name    string
	rrType  uint16
	unicast bool
}

// Browse discovers central hubs on all suitable interfaces.
func Browse(ctx context.Context) ([]Gateway, error) {
	return BrowseWithOptions(ctx, BrowseOptions{})
}

// BrowseWithOptions performs a complete DNS-SD browse. Unlike a one-shot PTR
// query, it actively resolves every discovered instance's SRV/TXT records and
// then resolves the SRV target's A/AAAA records. Xiaomi's official integration
// follows the same two-stage browse-and-resolve behavior.
func BrowseWithOptions(ctx context.Context, options BrowseOptions) ([]Gateway, error) {
	if options.QueryInterval <= 0 {
		options.QueryInterval = time.Second
	}
	debugf := options.Debugf
	if debugf == nil {
		debugf = func(string, ...any) {}
	}

	interfaces, err := selectInterfaces(options.InterfaceNames)
	if err != nil {
		return nil, err
	}
	group := &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}

	var sockets []mdnsSocket
	for _, iface := range interfaces {
		conn, listenErr := net.ListenMulticastUDP("udp4", &iface, group)
		if listenErr != nil {
			debugf("跳过网卡 %s：无法加入 mDNS 多播组：%v", iface.Name, listenErr)
			continue
		}
		_ = conn.SetReadBuffer(256 * 1024)
		sockets = append(sockets, mdnsSocket{iface: iface, conn: conn})
		debugf("使用网卡 %s (%s)", iface.Name, interfaceIPv4s(iface))
	}
	if len(sockets) == 0 {
		return nil, errors.New("没有可用的 IPv4 mDNS 多播网卡；请检查网卡状态、容器网络模式和防火墙")
	}
	defer func() {
		for _, socket := range sockets {
			_ = socket.conn.Close()
		}
	}()

	packets := make(chan mdnsPacket, 256)
	var readers sync.WaitGroup
	for _, socket := range sockets {
		readers.Add(1)
		go func(socket mdnsSocket) {
			defer readers.Done()
			readPackets(ctx, socket, packets)
		}(socket)
	}
	go func() {
		readers.Wait()
		close(packets)
	}()

	all := records{
		instances: make(map[string]bool),
		srv:       make(map[string]srvRecord),
		txt:       make(map[string][]string),
		addresses: make(map[string]map[string]bool),
	}

	queryAll := func() {
		// DNS-SD browse question: multicast response requested (QM).
		ptrQuery, buildErr := buildQuery([]dnsQuestion{{name: Service, rrType: 12}})
		if buildErr == nil {
			sendOnAll(sockets, group, ptrQuery, debugf)
		}

		// Service resolution questions: request unicast responses (QU), matching
		// the official zeroconf AsyncServiceInfo resolution behavior.
		for instance := range all.instances {
			query, queryErr := buildQuery([]dnsQuestion{
				{name: instance, rrType: 33, unicast: true}, // SRV
				{name: instance, rrType: 16, unicast: true}, // TXT
			})
			if queryErr == nil {
				sendOnAll(sockets, group, query, debugf)
			}
		}
		for _, srv := range all.srv {
			query, queryErr := buildQuery([]dnsQuestion{
				{name: srv.target, rrType: 1, unicast: true},  // A
				{name: srv.target, rrType: 28, unicast: true}, // AAAA
			})
			if queryErr == nil {
				sendOnAll(sockets, group, query, debugf)
			}
		}
	}

	queryAll()
	ticker := time.NewTicker(options.QueryInterval)
	defer ticker.Stop()

	packetCount := 0
	for {
		select {
		case <-ctx.Done():
			gateways := buildGateways(all)
			debugf("发现结束：收到 %d 个 mDNS 数据包，PTR=%d SRV=%d TXT=%d 主机=%d，完整网关=%d",
				packetCount, len(all.instances), len(all.srv), len(all.txt), len(all.addresses), len(gateways))
			return gateways, nil
		case <-ticker.C:
			queryAll()
		case packet, ok := <-packets:
			if !ok {
				gateways := buildGateways(all)
				return gateways, nil
			}
			packetCount++
			beforeInstances := len(all.instances)
			beforeSRV := len(all.srv)
			beforeTXT := len(all.txt)
			beforeHosts := len(all.addresses)
			parseDNSMessage(packet.data, &all)
			debugf("收到 mDNS：网卡=%s 来源=%s 字节=%d，记录累计 PTR=%d SRV=%d TXT=%d 主机=%d",
				packet.iface, packet.from, len(packet.data), len(all.instances), len(all.srv), len(all.txt), len(all.addresses))
			// Resolve newly learned names immediately instead of waiting for the
			// next periodic retry.
			if len(all.instances) != beforeInstances || len(all.srv) != beforeSRV ||
				len(all.txt) != beforeTXT || len(all.addresses) != beforeHosts {
				queryAll()
			}
		}
	}
}

func readPackets(ctx context.Context, socket mdnsSocket, output chan<- mdnsPacket) {
	buffer := make([]byte, 65535)
	for {
		_ = socket.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, from, err := socket.conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			return
		}
		data := append([]byte(nil), buffer[:n]...)
		select {
		case output <- mdnsPacket{iface: socket.iface.Name, from: from.String(), data: data}:
		case <-ctx.Done():
			return
		}
	}
}

func selectInterfaces(requested []string) ([]net.Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("枚举网卡失败：%w", err)
	}
	wanted := make(map[string]bool)
	for _, raw := range requested {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				wanted[strings.ToLower(name)] = true
			}
		}
	}

	var selected []net.Interface
	found := make(map[string]bool)
	for _, iface := range all {
		if len(wanted) > 0 && !wanted[strings.ToLower(iface.Name)] {
			continue
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if len(wanted) == 0 && iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(interfaceIPv4List(iface)) == 0 {
			continue
		}
		selected = append(selected, iface)
		found[strings.ToLower(iface.Name)] = true
	}
	if len(wanted) > 0 {
		var missing []string
		for name := range wanted {
			if !found[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf("指定网卡不存在、未启用、没有 IPv4 或不支持多播：%s", strings.Join(missing, ", "))
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("找不到已启用且支持 IPv4 多播的非回环网卡")
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Index < selected[j].Index })
	return selected, nil
}

func interfaceIPv4List(iface net.Interface) []string {
	addresses, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var result []string
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip != nil && ip.To4() != nil {
			result = append(result, ip.String())
		}
	}
	return result
}

func interfaceIPv4s(iface net.Interface) string {
	values := interfaceIPv4List(iface)
	if len(values) == 0 {
		return "无 IPv4"
	}
	return strings.Join(values, ",")
}

func sendOnAll(sockets []mdnsSocket, group *net.UDPAddr, payload []byte, debugf func(string, ...any)) {
	for _, socket := range sockets {
		if _, err := socket.conn.WriteToUDP(payload, group); err != nil {
			debugf("网卡 %s 发送 mDNS 查询失败：%v", socket.iface.Name, err)
		}
	}
}

func buildPTRQuery(name string) ([]byte, error) {
	return buildQuery([]dnsQuestion{{name: name, rrType: 12}})
}

func buildQuery(questions []dnsQuestion) ([]byte, error) {
	if len(questions) == 0 || len(questions) > 65535 {
		return nil, errors.New("invalid DNS question count")
	}
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(questions)))
	for _, question := range questions {
		encoded, err := encodeName(question.name)
		if err != nil {
			return nil, err
		}
		packet = append(packet, encoded...)
		packet = binary.BigEndian.AppendUint16(packet, question.rrType)
		class := uint16(1) // IN
		if question.unicast {
			class |= 0x8000 // QU bit in mDNS question class
		}
		packet = binary.BigEndian.AppendUint16(packet, class)
	}
	return packet, nil
}

func encodeName(name string) ([]byte, error) {
	name = strings.TrimSuffix(name, ".")
	var out []byte
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return nil, fmt.Errorf("invalid DNS label %q", label)
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0), nil
}

func parseDNSMessage(message []byte, out *records) {
	if len(message) < 12 {
		return
	}
	qd := int(binary.BigEndian.Uint16(message[4:6]))
	an := int(binary.BigEndian.Uint16(message[6:8]))
	ns := int(binary.BigEndian.Uint16(message[8:10]))
	ar := int(binary.BigEndian.Uint16(message[10:12]))
	offset := 12
	for i := 0; i < qd; i++ {
		_, consumed, err := decodeName(message, offset)
		if err != nil || consumed+4 > len(message) {
			return
		}
		offset = consumed + 4
	}
	for i := 0; i < an+ns+ar; i++ {
		name, next, err := decodeName(message, offset)
		if err != nil || next+10 > len(message) {
			return
		}
		rrType := binary.BigEndian.Uint16(message[next : next+2])
		rdLength := int(binary.BigEndian.Uint16(message[next+8 : next+10]))
		rdata := next + 10
		if rdata+rdLength > len(message) {
			return
		}
		parseRecord(message, canonical(name), rrType, rdata, rdLength, out)
		offset = rdata + rdLength
	}
}

func parseRecord(message []byte, name string, rrType uint16, rdata, length int, out *records) {
	switch rrType {
	case 12: // PTR
		instance, _, err := decodeName(message, rdata)
		if err == nil && canonical(name) == canonical(Service) {
			out.instances[canonical(instance)] = true
		}
	case 33: // SRV
		if length < 6 {
			return
		}
		target, _, err := decodeName(message, rdata+6)
		if err == nil {
			out.srv[name] = srvRecord{
				port:   int(binary.BigEndian.Uint16(message[rdata+4 : rdata+6])),
				target: canonical(target),
			}
			out.instances[name] = true
		}
	case 16: // TXT
		end := rdata + length
		var values []string
		for pos := rdata; pos < end; {
			itemLength := int(message[pos])
			pos++
			if pos+itemLength > end {
				break
			}
			values = append(values, string(message[pos:pos+itemLength]))
			pos += itemLength
		}
		out.txt[name] = values
	case 1: // A
		if length == net.IPv4len {
			addAddress(out, name, net.IP(message[rdata:rdata+length]).String())
		}
	case 28: // AAAA
		if length == net.IPv6len {
			addAddress(out, name, net.IP(message[rdata:rdata+length]).String())
		}
	}
}

func decodeName(message []byte, start int) (string, int, error) {
	if start < 0 || start >= len(message) {
		return "", 0, errors.New("DNS name offset out of range")
	}
	var labels []string
	pos := start
	next := -1
	seen := make(map[int]bool)
	for steps := 0; steps < 128; steps++ {
		if pos >= len(message) {
			return "", 0, errors.New("truncated DNS name")
		}
		if seen[pos] {
			return "", 0, errors.New("DNS compression pointer loop")
		}
		seen[pos] = true
		length := int(message[pos])
		if length == 0 {
			pos++
			if next < 0 {
				next = pos
			}
			return strings.Join(labels, ".") + ".", next, nil
		}
		if length&0xc0 == 0xc0 {
			if pos+1 >= len(message) {
				return "", 0, errors.New("truncated DNS compression pointer")
			}
			pointer := ((length & 0x3f) << 8) | int(message[pos+1])
			if next < 0 {
				next = pos + 2
			}
			pos = pointer
			continue
		}
		if length > 63 || pos+1+length > len(message) {
			return "", 0, errors.New("invalid DNS label")
		}
		labels = append(labels, string(message[pos+1:pos+1+length]))
		pos += 1 + length
	}
	return "", 0, errors.New("DNS name exceeds pointer limit")
}

func addAddress(out *records, host, address string) {
	if out.addresses[host] == nil {
		out.addresses[host] = make(map[string]bool)
	}
	out.addresses[host][address] = true
}

func buildGateways(all records) []Gateway {
	instances := make([]string, 0, len(all.instances))
	for instance := range all.instances {
		instances = append(instances, instance)
	}
	sort.Strings(instances)
	gateways := make([]Gateway, 0, len(instances))
	for _, instance := range instances {
		srv, ok := all.srv[instance]
		if !ok {
			continue
		}
		var addresses []string
		for address := range all.addresses[srv.target] {
			addresses = append(addresses, address)
		}
		sort.Slice(addresses, func(i, j int) bool {
			left, right := net.ParseIP(addresses[i]), net.ParseIP(addresses[j])
			if (left.To4() != nil) != (right.To4() != nil) {
				return left.To4() != nil
			}
			return addresses[i] < addresses[j]
		})
		gateway := Gateway{
			Instance:  strings.TrimSuffix(instance, "."),
			HostName:  strings.TrimSuffix(srv.target, "."),
			Addresses: addresses,
			Port:      srv.port,
			TXT:       append([]string(nil), all.txt[instance]...),
		}
		if profile, err := profileFromTXT(gateway.TXT); err == nil {
			gateway.DID, gateway.GroupID, gateway.Role, gateway.MQTTEnabled = parseProfile(profile)
		}
		gateways = append(gateways, gateway)
	}
	return gateways
}

func canonical(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, "."))) + "."
}

func profileFromTXT(txt []string) ([]byte, error) {
	for _, item := range txt {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.EqualFold(key, "profile") {
			continue
		}
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		} {
			if data, err := enc.DecodeString(value); err == nil {
				return data, nil
			}
		}
	}
	return nil, errors.New("mDNS TXT has no decodable profile")
}

// parseProfile follows the profile layout advertised by supported central hubs.
func parseProfile(profile []byte) (did, groupID string, role int, mqtt bool) {
	if len(profile) < 23 {
		return "", "", 0, false
	}
	did = fmt.Sprintf("%d", binary.BigEndian.Uint64(profile[1:9]))
	group := append([]byte(nil), profile[9:17]...)
	for i, j := 0, len(group)-1; i < j; i, j = i+1, j-1 {
		group[i], group[j] = group[j], group[i]
	}
	groupID = hex.EncodeToString(group)
	role = int(profile[20] >> 4)
	mqtt = ((profile[22] >> 1) & 1) == 1
	return did, groupID, role, mqtt
}

// PreferredAddress picks IPv4 first because link-local IPv6 needs a zone ID.
func (g Gateway) PreferredAddress() string {
	for _, raw := range g.Addresses {
		if ip := net.ParseIP(raw); ip != nil && ip.To4() != nil {
			return raw
		}
	}
	if len(g.Addresses) > 0 {
		return g.Addresses[0]
	}
	return g.HostName
}
