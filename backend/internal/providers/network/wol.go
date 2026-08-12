package network

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

type udpWaker struct{}

func (udpWaker) Wake(ctx context.Context, request WakeRequest) error {
	if len(request.MAC) != 6 {
		return fmt.Errorf("Wake-on-LAN requires a six-byte MAC address")
	}
	if request.Port < 1 || request.Port > 65535 {
		return fmt.Errorf("Wake-on-LAN port is invalid")
	}
	ip := net.ParseIP(request.BroadcastAddress)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("Wake-on-LAN address must be IPv4")
	}
	dialer := net.Dialer{Control: enableBroadcast}
	if request.Interface != "" {
		interfaceAddress, err := interfaceIPv4(request.Interface)
		if err != nil {
			return err
		}
		dialer.LocalAddr = &net.UDPAddr{IP: interfaceAddress}
	}
	connection, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(request.BroadcastAddress, strconv.Itoa(request.Port)))
	if err != nil {
		return fmt.Errorf("dial Wake-on-LAN UDP socket: %w", err)
	}
	defer connection.Close()
	packet := magicPacket(request.MAC)
	if _, err := connection.Write(packet); err != nil {
		return fmt.Errorf("send Wake-on-LAN packet: %w", err)
	}
	return nil
}

func magicPacket(mac net.HardwareAddr) []byte {
	packet := make([]byte, 6+16*len(mac))
	for index := 0; index < 6; index++ {
		packet[index] = 0xff
	}
	for index := 6; index < len(packet); index += len(mac) {
		copy(packet[index:], mac)
	}
	return packet
}

func interfaceIPv4(value string) (net.IP, error) {
	if parsed := net.ParseIP(value); parsed != nil && parsed.To4() != nil {
		return parsed.To4(), nil
	}
	interfaceValue, err := net.InterfaceByName(value)
	if err != nil {
		return nil, fmt.Errorf("resolve Wake-on-LAN interface %q: %w", value, err)
	}
	addresses, err := interfaceValue.Addrs()
	if err != nil {
		return nil, fmt.Errorf("list Wake-on-LAN interface %q addresses: %w", value, err)
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil {
			return network.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("Wake-on-LAN interface %q has no IPv4 address", value)
}
