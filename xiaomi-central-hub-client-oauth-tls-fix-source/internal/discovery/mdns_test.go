package discovery

import (
	"encoding/base64"
	"encoding/binary"
	"net"
	"testing"
)

type testRR struct {
	name   string
	rrType uint16
	rdata  []byte
}

func TestBuildQuerySetsQUFlag(t *testing.T) {
	packet, err := buildQuery([]dnsQuestion{{name: "hub._miot-central._tcp.local.", rrType: 33, unicast: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, next, err := decodeName(packet, 12)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint16(packet[next+2 : next+4]); got != 0x8001 {
		t.Fatalf("question class = %#x, want %#x", got, uint16(0x8001))
	}
}

func TestParseSeparateDNSServiceResponses(t *testing.T) {
	instance := "living-room._miot-central._tcp.local."
	host := "central-hub.local."

	profile := make([]byte, 23)
	profile[0] = 1
	binary.BigEndian.PutUint64(profile[1:9], 123456789)
	copy(profile[9:17], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	profile[20] = 0x10
	profile[22] = 0x02

	ptrName, _ := encodeName(instance)
	srvTarget, _ := encodeName(host)
	srvData := make([]byte, 6)
	binary.BigEndian.PutUint16(srvData[4:6], 8883)
	srvData = append(srvData, srvTarget...)
	txtValue := "profile=" + base64.StdEncoding.EncodeToString(profile)
	txtData := append([]byte{byte(len(txtValue))}, []byte(txtValue)...)

	all := records{
		instances: make(map[string]bool),
		srv:       make(map[string]srvRecord),
		txt:       make(map[string][]string),
		addresses: make(map[string]map[string]bool),
	}
	parseDNSMessage(testDNSResponse([]testRR{{name: Service, rrType: 12, rdata: ptrName}}), &all)
	parseDNSMessage(testDNSResponse([]testRR{
		{name: instance, rrType: 33, rdata: srvData},
		{name: instance, rrType: 16, rdata: txtData},
	}), &all)
	parseDNSMessage(testDNSResponse([]testRR{{name: host, rrType: 1, rdata: []byte(net.IPv4(192, 168, 100, 50).To4())}}), &all)

	gateways := buildGateways(all)
	if len(gateways) != 1 {
		t.Fatalf("len(gateways) = %d, want 1", len(gateways))
	}
	gateway := gateways[0]
	if gateway.Port != 8883 || gateway.DID != "123456789" || gateway.Role != 1 || !gateway.MQTTEnabled {
		t.Fatalf("unexpected gateway: %+v", gateway)
	}
	if len(gateway.Addresses) != 1 || gateway.Addresses[0] != "192.168.100.50" {
		t.Fatalf("unexpected addresses: %v", gateway.Addresses)
	}
	if gateway.GroupID != "0807060504030201" {
		t.Fatalf("group id = %s", gateway.GroupID)
	}
}

func testDNSResponse(records []testRR) []byte {
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[2:4], 0x8400)
	binary.BigEndian.PutUint16(packet[6:8], uint16(len(records)))
	for _, record := range records {
		name, _ := encodeName(record.name)
		packet = append(packet, name...)
		packet = binary.BigEndian.AppendUint16(packet, record.rrType)
		packet = binary.BigEndian.AppendUint16(packet, 0x8001)
		packet = binary.BigEndian.AppendUint32(packet, 120)
		packet = binary.BigEndian.AppendUint16(packet, uint16(len(record.rdata)))
		packet = append(packet, record.rdata...)
	}
	return packet
}
