package srtp

import (
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
)

type Server struct {
	address  string
	conn     net.PacketConn
	sessions map[uint32]*Session
	mu       sync.Mutex
}

func NewServer(address string) *Server {
	return &Server{
		address:  address,
		sessions: map[uint32]*Session{},
	}
}

// NewSessionServer creates one of the per-media UDP sockets used by a HomeKit
// streaming session. HomeKit requires the accessory address family to match
// the controller offer; keeping the port ephemeral also prevents video and
// audio RTCP from competing in a shared SSRC lookup table.
func (s *Server) NewSessionServer(ipv6 bool) *Server {
	return s.NewSessionServerAt(ipv6, "")
}

// NewWildcardSessionServer creates a per-media socket that deliberately
// ignores a concrete address configured on the base server. This is required
// when a local Apple controller advertises one of this host's VPN/TUN
// addresses: binding the accessory's physical address forces outbound RTP to
// use the wrong local interface, while a wildcard socket lets the kernel pick
// the controller-facing source address and can still receive RTCP sent to the
// accessory address.
func (s *Server) NewWildcardSessionServer(ipv6 bool) *Server {
	host := "0.0.0.0"
	if ipv6 {
		host = "::"
	}
	return NewServer(net.JoinHostPort(host, "0"))
}

// NewSessionServerAt creates a per-media socket bound to the same concrete
// local address advertised in HomeKit SetupEndpoints. Binding 0.0.0.0 lets the
// kernel receive controller RTCP, but the OS may select a different source IP
// for outbound RTP on VPN/HomeHub routes and Apple then silently discards it.
func (s *Server) NewSessionServerAt(ipv6 bool, localAddress string) *Server {
	host := "0.0.0.0"
	if ipv6 {
		host = "::"
	}
	if compatibleSessionHost(localAddress, ipv6) {
		host = localAddress
	}
	configuredHost, _, err := net.SplitHostPort(s.address)
	if host == "0.0.0.0" || host == "::" {
		if err == nil && compatibleSessionHost(configuredHost, ipv6) {
			host = configuredHost
		}
	}
	return NewServer(net.JoinHostPort(host, "0"))
}

func compatibleSessionHost(host string, ipv6 bool) bool {
	plainHost := host
	if zone := strings.LastIndexByte(plainHost, '%'); zone >= 0 {
		plainHost = plainHost[:zone]
	}
	ip := net.ParseIP(plainHost)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	return (ipv6 && ip.To4() == nil) || (!ipv6 && ip.To4() != nil)
}

func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn.LocalAddr().(*net.UDPAddr).Port
	}

	_, a, _ := net.SplitHostPort(s.address)
	i, _ := strconv.Atoi(a)
	return i
}

// EnsureListening binds the UDP socket before SetupEndpoints advertises a port.
// Apple Home will dial the accessory SRTP endpoint immediately after the write
// response; delaying Listen until SelectedStreamConfiguration is too late.
func (s *Server) EnsureListening() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return nil
	}
	conn, err := net.ListenPacket("udp", s.address)
	if err != nil {
		return err
	}
	tunePacketBuffers(conn)
	s.conn = conn
	go s.handle(conn)
	return nil
}

func (s *Server) AddSession(session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := session.init(); err != nil {
		return err
	}

	if s.conn == nil {
		conn, err := net.ListenPacket("udp", s.address)
		if err != nil {
			return err
		}
		tunePacketBuffers(conn)
		s.conn = conn
		go s.handle(conn)
	}

	session.conn = s.conn
	s.sessions[session.Remote.SSRC] = session
	return nil
}

// Scrypted raises both buffers to 1 MiB for HomeKit streaming. IDR frames are
// emitted as short RTP bursts, and the platform UDP defaults can otherwise
// drop packets before the controller has a chance to report congestion.
func tunePacketBuffers(conn net.PacketConn) {
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		return
	}
	_ = udp.SetReadBuffer(1 << 20)
	_ = udp.SetWriteBuffer(1 << 20)
}

func (s *Server) DelSession(session *Session) {
	s.mu.Lock()

	if session != nil && session.Remote != nil {
		delete(s.sessions, session.Remote.SSRC)
	}

	// check s.conn for https://github.com/AlexxIT/go2rtc/issues/734
	if len(s.sessions) == 0 && s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}

	s.mu.Unlock()
}

func (s *Server) Close() {
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	clear(s.sessions)
	s.mu.Unlock()
}

func (s *Server) GetSession(ssrc uint32) (session *Session) {
	s.mu.Lock()
	session = s.sessions[ssrc]
	// Scrypted dedicates one socket to each video/audio stream. The SSRC in a
	// controller Receiver Report is not reliable enough to be the sole routing
	// key across Apple OS versions. On a single-session socket, route the packet
	// to its only possible owner and let SRTP authentication validate it.
	if session == nil && len(s.sessions) == 1 {
		for _, session = range s.sessions {
			break
		}
	}
	s.mu.Unlock()
	return
}

func (s *Server) handle(conn net.PacketConn) error {
	b := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFrom(b)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if n < 2 {
			continue
		}

		// Multiplexing RTP Data and Control Packets on a Single Port
		// https://datatracker.ietf.org/doc/html/rfc5761

		switch packetType := b[1]; packetType {
		case 99, 110, 0x80 | 99, 0x80 | 110:
			if n < 12 {
				continue
			}
			// this is default position for SSRC in RTP packet
			ssrc := binary.BigEndian.Uint32(b[8:])
			if session := s.GetSession(ssrc); session != nil {
				session.ReadRTP(b[:n])
			}

		case 200, 201, 202, 203, 204, 205, 206, 207:
			if n < 8 {
				continue
			}
			// this is default position for SSRC in RTCP packet
			ssrc := binary.BigEndian.Uint32(b[4:])
			if session := s.GetSession(ssrc); session != nil {
				session.ReadRTCP(b[:n])
			}
		}
	}
}
