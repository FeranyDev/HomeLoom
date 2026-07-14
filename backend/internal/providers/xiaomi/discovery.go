package xiaomi

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

	"github.com/brutella/dnssd"
)

const centralHubService = "_miot-central._tcp.local."

type Gateway struct {
	Instance    string   `json:"instance"`
	HostName    string   `json:"hostName"`
	Addresses   []string `json:"addresses"`
	Port        int      `json:"port"`
	DID         string   `json:"did,omitempty"`
	GroupID     string   `json:"groupId,omitempty"`
	Role        int      `json:"role,omitempty"`
	MQTTEnabled bool     `json:"mqttEnabled"`
}

func (g Gateway) PreferredAddress() string {
	for _, address := range g.Addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
			return address
		}
	}
	if len(g.Addresses) > 0 {
		return g.Addresses[0]
	}
	return strings.TrimSuffix(g.HostName, ".")
}

func DiscoverGateways(ctx context.Context) ([]Gateway, error) {
	var mu sync.Mutex
	items := make(map[string]Gateway)
	err := dnssd.LookupType(ctx, centralHubService, func(entry dnssd.BrowseEntry) {
		addresses := make([]string, 0, len(entry.IPs))
		for _, ip := range entry.IPs {
			addresses = append(addresses, ip.String())
		}
		sort.Slice(addresses, func(i, j int) bool {
			left, right := net.ParseIP(addresses[i]), net.ParseIP(addresses[j])
			if (left.To4() != nil) != (right.To4() != nil) {
				return left.To4() != nil
			}
			return addresses[i] < addresses[j]
		})
		gateway := Gateway{Instance: entry.Name, HostName: entry.Host, Addresses: addresses, Port: entry.Port}
		if profile, decodeErr := decodeGatewayProfile(entry.Text["profile"]); decodeErr == nil {
			gateway.DID, gateway.GroupID, gateway.Role, gateway.MQTTEnabled = parseGatewayProfile(profile)
		}
		mu.Lock()
		items[entry.ServiceInstanceName()+"/"+entry.IfaceName] = gateway
		mu.Unlock()
	}, func(entry dnssd.BrowseEntry) {
		mu.Lock()
		delete(items, entry.ServiceInstanceName()+"/"+entry.IfaceName)
		mu.Unlock()
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("discover Xiaomi central hubs: %w", err)
	}
	mu.Lock()
	result := make([]Gateway, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	mu.Unlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role != result[j].Role {
			return result[i].Role > result[j].Role
		}
		return result[i].Instance < result[j].Instance
	})
	return result, nil
}

func decodeGatewayProfile(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(value); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("mDNS profile is not valid base64")
}

func parseGatewayProfile(profile []byte) (did, groupID string, role int, mqtt bool) {
	if len(profile) < 23 {
		return "", "", 0, false
	}
	did = fmt.Sprintf("%d", binary.BigEndian.Uint64(profile[1:9]))
	group := append([]byte(nil), profile[9:17]...)
	for left, right := 0, len(group)-1; left < right; left, right = left+1, right-1 {
		group[left], group[right] = group[right], group[left]
	}
	return did, hex.EncodeToString(group), int(profile[20] >> 4), ((profile[22] >> 1) & 1) == 1
}
