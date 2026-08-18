package lan

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/brutella/dnssd"
)

// ServiceType is the DNS-SD service advertised by eWeLink LAN devices.
// Discovery only returns transient endpoint metadata; it deliberately does
// not decode the TXT data because encrypted profiles require a device key.
const ServiceType = "_ewelink._tcp.local."

// ServiceBrowser keeps DNS-SD transport replaceable. The provider package
// uses it to test parsing and deduplication without opening multicast sockets.
type ServiceBrowser interface {
	Browse(context.Context, string, func(Service)) error
}

// DiscoverServices browses eWeLink's DNS-SD service for the requested
// duration. Reaching its own timeout is a successful, finite scan; a caller
// cancellation remains an error. Results are deterministic and contain no
// decoded data or device key.
func DiscoverServices(ctx context.Context, timeout time.Duration, browser ServiceBrowser) ([]Service, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return nil, errors.New("Sonoff mDNS discovery timeout must be positive")
	}
	if browser == nil {
		browser = dnsSDBrowser{}
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	seen := make(map[string]Service)
	err := browser.Browse(scanCtx, ServiceType, func(service Service) {
		service = sanitizeService(service)
		if service.Address == "" || strings.TrimSpace(service.TXT["id"]) == "" {
			return
		}
		key := strings.TrimSpace(service.TXT["id"])
		if previous, exists := seen[key]; !exists || serviceAddressKey(service) < serviceAddressKey(previous) {
			seen[key] = service
		}
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	result := make([]Service, 0, len(seen))
	for _, service := range seen {
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := strings.TrimSpace(result[i].TXT["id"]), strings.TrimSpace(result[j].TXT["id"])
		if left == right {
			return serviceAddressKey(result[i]) < serviceAddressKey(result[j])
		}
		return left < right
	})
	return result, nil
}

type dnsSDBrowser struct{}

func (dnsSDBrowser) Browse(ctx context.Context, serviceType string, add func(Service)) error {
	return dnssd.LookupType(ctx, serviceType, func(entry dnssd.BrowseEntry) {
		address := firstRoutableAddress(entry.IPs)
		if address == "" {
			return
		}
		add(Service{
			Instance: entry.Name,
			Service:  entry.Type + "." + entry.Domain + ".",
			Host:     strings.TrimSuffix(entry.Host, "."),
			Address:  address,
			Port:     entry.Port,
			TXT:      cloneTXT(entry.Text),
		})
	}, nil)
}

func sanitizeService(service Service) Service {
	service.Instance = strings.TrimSpace(service.Instance)
	service.Service = strings.TrimSpace(service.Service)
	service.Host = strings.TrimSuffix(strings.TrimSpace(service.Host), ".")
	service.Address = strings.TrimSpace(service.Address)
	if service.Port == 0 {
		service.Port = DefaultPort
	}
	service.TXT = cloneTXT(service.TXT)
	return service
}

func serviceAddressKey(service Service) string {
	return strings.TrimSpace(service.Address) + ":" + strings.TrimSpace(service.Host)
}

func firstRoutableAddress(values []net.IP) string {
	for _, value := range values {
		if value == nil || value.IsUnspecified() || value.IsMulticast() || value.IsLoopback() {
			continue
		}
		if ipv4 := value.To4(); ipv4 != nil {
			return ipv4.String()
		}
		return value.String()
	}
	return ""
}
