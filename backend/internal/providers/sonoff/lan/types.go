package lan

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const DefaultPort = 8081

// Request contains the information needed to address one Sonoff LAN device.
//
// DeviceKey is the eWeLink devicekey (not selfApikey). DIY devices send their
// payload as JSON and do not need a device key. Sequence, SelfAPIKey and IV
// are optional test/integration overrides; normal callers can leave them empty.
type Request struct {
	DeviceID  string
	DeviceKey string
	DIY       bool
	Host      string
	Port      int

	Sequence   string
	SelfAPIKey string
	IV         string
}

// Service is the address and TXT profile announced by an eWeLink mDNS
// service. TXT values are kept unmodified so callers can decide whether they
// need the device key to decode the announced data.
type Service struct {
	Instance string
	Service  string
	Host     string
	Address  string
	Port     int
	TXT      map[string]string
}

// Device is the decoded, useful subset of a Sonoff mDNS profile.
type Device struct {
	DeviceID   string
	Type       string
	APIVersion string
	Sequence   string
	Encrypt    bool
	IV         string
	Host       string
	Port       int
	Data       json.RawMessage
	TXT        map[string]string
}

// Endpoint returns the HTTP base URL for the device.
func (r Request) Endpoint() (string, error) {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "", errors.New("Sonoff host is required")
	}
	port := r.Port
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid Sonoff port %d", port)
	}

	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("invalid Sonoff host URL %q", host)
		}
		if parsed.Path == "" {
			parsed.Path = "/"
		}
		if parsed.Port() == "" {
			parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port))
		}
		return strings.TrimRight(parsed.String(), "/"), nil
	}

	host = strings.TrimSuffix(host, ".")
	if parsedIP := net.ParseIP(host); parsedIP == nil && strings.Contains(host, "/") {
		return "", fmt.Errorf("invalid Sonoff host %q", r.Host)
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ParseService converts an mDNS service into a Device. If the TXT profile is
// encrypted, the returned Data remains nil until DecodeServiceData is called
// with the device key.
func ParseService(service Service) (Device, error) {
	txt := cloneTXT(service.TXT)
	deviceID := strings.TrimSpace(txt["id"])
	if deviceID == "" {
		return Device{}, errors.New("Sonoff TXT profile has no id")
	}
	port := service.Port
	if port == 0 {
		port = DefaultPort
	}
	address := strings.TrimSpace(service.Address)
	if address == "" {
		address = strings.TrimSuffix(strings.TrimSpace(service.Host), ".")
	}
	if address == "" {
		return Device{}, errors.New("Sonoff service has no address or host")
	}
	device := Device{
		DeviceID:   deviceID,
		Type:       strings.TrimSpace(txt["type"]),
		APIVersion: strings.TrimSpace(txt["apivers"]),
		Sequence:   strings.TrimSpace(txt["seq"]),
		IV:         strings.TrimSpace(txt["iv"]),
		Host:       address,
		Port:       port,
		TXT:        txt,
	}
	device.Encrypt = strings.EqualFold(strings.TrimSpace(txt["encrypt"]), "true")
	if data := JoinTXTData(txt); data != "" {
		if device.Encrypt {
			// The wire representation is not JSON until the device key is known.
			device.Data = json.RawMessage(strconv.Quote(data))
		} else if json.Valid([]byte(data)) {
			device.Data = json.RawMessage(data)
		} else {
			return Device{}, fmt.Errorf("invalid Sonoff TXT data: %w", errInvalidJSON)
		}
	}
	return device, nil
}

// DecodeServiceData decodes the data1..data4 payload from an mDNS profile.
// For DIY profiles it simply validates and returns the clear-text JSON.
func DecodeServiceData(service Service, deviceKey string) (json.RawMessage, error) {
	data := JoinTXTData(service.TXT)
	if data == "" {
		return nil, errors.New("Sonoff TXT profile has no data1..data4 payload")
	}
	encrypted := strings.EqualFold(strings.TrimSpace(service.TXT["encrypt"]), "true")
	if !encrypted {
		if !json.Valid([]byte(data)) {
			return nil, fmt.Errorf("invalid Sonoff TXT data: %w", errInvalidJSON)
		}
		return json.RawMessage(data), nil
	}
	iv, err := ParseIV(service.TXT["iv"])
	if err != nil {
		return nil, err
	}
	decoded, err := Decode(deviceKey, data, iv, false)
	if err != nil {
		return nil, fmt.Errorf("decode Sonoff TXT data: %w", err)
	}
	if !json.Valid(decoded) {
		return nil, fmt.Errorf("invalid decoded Sonoff TXT data: %w", errInvalidJSON)
	}
	return json.RawMessage(decoded), nil
}

var errInvalidJSON = errors.New("payload is not valid JSON")

func cloneTXT(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
