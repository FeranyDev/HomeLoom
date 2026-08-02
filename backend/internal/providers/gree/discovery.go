package gree

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"syscall"
	"time"

	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const (
	greeScanMessage       = `{"t":"scan"}`
	greeDiscoveryReadTick = 100 * time.Millisecond
	maximumDiscoveryRead  = 64 * 1024
)

type discoveryDatagram struct {
	host    string
	payload []byte
}

type discoveryTransport interface {
	Scan(context.Context, time.Duration) ([]discoveryDatagram, error)
}

type udpDiscoveryTransport struct {
	port int
}

func newUDPDiscoveryTransport(port int) discoveryTransport {
	return &udpDiscoveryTransport{port: port}
}

// Scan sends the Gree discovery message from an ephemeral UDP port so devices
// can reply to the same socket. It broadcasts once to the limited broadcast
// address and once to each active interface's directed broadcast address.
func (t *udpDiscoveryTransport) Scan(ctx context.Context, timeout time.Duration) ([]discoveryDatagram, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return nil, errors.New("Gree discovery timeout must be positive")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("open Gree discovery socket: %w", err)
	}
	defer conn.Close()
	if err := enableUDPBroadcast(conn); err != nil {
		return nil, fmt.Errorf("enable Gree UDP broadcast: %w", err)
	}

	broadcasts := greeBroadcastAddresses()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	var lastSendErr error
	sent := false
	for _, broadcast := range broadcasts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, err := conn.WriteToUDP([]byte(greeScanMessage), &net.UDPAddr{IP: broadcast, Port: t.port})
		if err != nil {
			lastSendErr = err
			continue
		}
		sent = true
	}
	if !sent && lastSendErr != nil {
		return nil, fmt.Errorf("send Gree discovery broadcast: %w", lastSendErr)
	}

	result := make([]discoveryDatagram, 0)
	buffer := make([]byte, maximumDiscoveryRead)
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return result, nil
		}
		readWindow := remaining
		if readWindow > greeDiscoveryReadTick {
			readWindow = greeDiscoveryReadTick
		}
		if err := conn.SetReadDeadline(time.Now().Add(readWindow)); err != nil {
			return result, fmt.Errorf("set Gree discovery deadline: %w", err)
		}
		count, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			var networkErr net.Error
			if errors.As(err, &networkErr) && networkErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, fmt.Errorf("receive Gree discovery response: %w", err)
		}
		result = append(result, discoveryDatagram{host: source.IP.String(), payload: append([]byte(nil), buffer[:count]...)})
	}
}

func enableUDPBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		controlErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return controlErr
}

func greeBroadcastAddresses() []net.IP {
	seen := map[string]struct{}{}
	result := make([]net.IP, 0, 4)
	add := func(ip net.IP) {
		ip = ip.To4()
		if ip == nil {
			return
		}
		key := ip.String()
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, append(net.IP(nil), ip...))
	}
	add(net.IPv4(255, 255, 255, 255))

	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, mask, ok := ipv4AddressAndMask(address)
			if !ok {
				continue
			}
			add(net.IPv4(ip[0]|^mask[0], ip[1]|^mask[1], ip[2]|^mask[2], ip[3]|^mask[3]))
		}
	}
	return result
}

func ipv4AddressAndMask(address net.Addr) ([4]byte, [4]byte, bool) {
	var ip net.IP
	var mask net.IPMask
	switch typed := address.(type) {
	case *net.IPNet:
		ip, mask = typed.IP, typed.Mask
	case *net.IPAddr:
		ip = typed.IP
		mask = net.CIDRMask(32, 32)
	default:
		return [4]byte{}, [4]byte{}, false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return [4]byte{}, [4]byte{}, false
	}
	if len(mask) == net.IPv6len {
		mask = mask[net.IPv6len-net.IPv4len:]
	}
	if len(mask) != net.IPv4len {
		return [4]byte{}, [4]byte{}, false
	}
	return [4]byte{ip4[0], ip4[1], ip4[2], ip4[3]}, [4]byte{mask[0], mask[1], mask[2], mask[3]}, true
}

func scanGreeDevices(ctx context.Context, timeout time.Duration) ([]providersdk.DiscoveryCandidate, error) {
	return scanGreeDevicesWithTransport(ctx, timeout, newUDPDiscoveryTransport(defaultPort))
}

func scanGreeDevicesWithTransport(ctx context.Context, timeout time.Duration, transport discoveryTransport) ([]providersdk.DiscoveryCandidate, error) {
	if transport == nil {
		return nil, errors.New("Gree discovery transport is required")
	}
	datagrams, err := transport.Scan(ctx, timeout)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(datagrams))
	result := make([]providersdk.DiscoveryCandidate, 0, len(datagrams))
	for _, datagram := range datagrams {
		candidate, ok := parseGreeDiscoveryResponse(datagram.host, datagram.payload)
		if !ok {
			// UDP broadcast shares a noisy LAN. A malformed or unrelated packet
			// is isolated to this datagram and never aborts the scan.
			continue
		}
		if _, exists := seen[candidate.MAC]; exists {
			continue
		}
		seen[candidate.MAC] = struct{}{}
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MAC == result[j].MAC {
			return result[i].Host < result[j].Host
		}
		return result[i].MAC < result[j].MAC
	})
	return result, nil
}

func parseGreeDiscoveryResponse(host string, payload []byte) (providersdk.DiscoveryCandidate, bool) {
	inner, err := decodeEnvelope(payload, []byte(genericGreeDeviceKey), 1)
	if err != nil {
		return providersdk.DiscoveryCandidate{}, false
	}
	if text, ok := inner["t"].(string); !ok || text != "dev" {
		return providersdk.DiscoveryCandidate{}, false
	}
	macText, ok := inner["mac"].(string)
	if !ok {
		return providersdk.DiscoveryCandidate{}, false
	}
	subMAC, mainMAC, err := normalizeMAC(macText)
	if err != nil || strings.TrimSpace(host) == "" {
		return providersdk.DiscoveryCandidate{}, false
	}
	mac := subMAC
	if subMAC != mainMAC {
		mac += "@" + mainMAC
	}
	name := discoveryString(inner["name"])
	if name == "" {
		suffix := mainMAC
		if len(suffix) > 4 {
			suffix = suffix[len(suffix)-4:]
		}
		name = "Gree " + suffix
	}
	metadata := make(map[string]string)
	for key, sourceKey := range map[string]string{"brand": "brand", "model": "model", "version": "ver"} {
		if value := discoveryString(inner[sourceKey]); value != "" {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	id := "gree-" + strings.ReplaceAll(mac, "@", "-")
	return providersdk.DiscoveryCandidate{
		ID:       id,
		Provider: ProviderType,
		Name:     name,
		Host:     host,
		Port:     defaultPort,
		MAC:      mac,
		Metadata: metadata,
	}, true
}

func discoveryString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func (p *Provider) Scan(ctx context.Context) ([]providersdk.DiscoveryCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return scanGreeDevices(ctx, p.config.requestTimeout())
}

var _ providersdk.DiscoveryScanner = (*Provider)(nil)
