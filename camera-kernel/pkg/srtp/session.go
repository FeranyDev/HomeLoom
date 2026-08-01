package srtp

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

type Session struct {
	Local  *Endpoint
	Remote *Endpoint

	OnReadRTP func(packet *rtp.Packet)

	Recv int // bytes recv
	Send int // bytes send

	conn net.PacketConn // local conn endpoint

	PayloadType  uint8
	RTCPInterval time.Duration

	senderRTCP rtcp.SenderReport
	senderTime time.Time

	rtpPacketsSent       atomic.Uint64
	rtpBytesSent         atomic.Uint64
	rtpWriteErrors       atomic.Uint64
	rtcpDatagramsRecv    atomic.Uint64
	rtcpPacketsRecv      atomic.Uint64
	rtcpDecryptErrors    atomic.Uint64
	rtcpParseErrors      atomic.Uint64
	rtcpReceiverReports  atomic.Uint64
	rtcpReportBlocks     atomic.Uint64
	rtcpMatchedReports   atomic.Uint64
	rtcpReportedSSRC     atomic.Uint32
	rtcpFractionLost     atomic.Uint32
	rtcpTotalLost        atomic.Uint32
	rtcpLastSequence     atomic.Uint32
	rtcpJitter           atomic.Uint32
	rtcpLastSenderReport atomic.Uint32
	rtcpPLI              atomic.Uint64
	rtcpFIR              atomic.Uint64
	rtcpNACK             atomic.Uint64
	lastRTCPUnixNano     atomic.Int64
	rtcpReady            chan struct{}
	rtcpReadyOnce        sync.Once
}

type Endpoint struct {
	Addr       string
	Port       uint16
	MasterKey  []byte
	MasterSalt []byte
	SSRC       uint32

	addr net.Addr
	srtp *srtp.Context
}

type SessionStats struct {
	LocalSSRC            uint32 `json:"localSsrc"`
	RTPPacketsSent       uint64 `json:"rtpPacketsSent"`
	RTPBytesSent         uint64 `json:"rtpBytesSent"`
	RTPWriteErrors       uint64 `json:"rtpWriteErrors"`
	RTCPDatagramsRecv    uint64 `json:"rtcpDatagramsReceived"`
	RTCPPacketsRecv      uint64 `json:"rtcpPacketsReceived"`
	RTCPDecryptErrors    uint64 `json:"rtcpDecryptErrors"`
	RTCPParseErrors      uint64 `json:"rtcpParseErrors"`
	RTCPReceiverReports  uint64 `json:"rtcpReceiverReports"`
	RTCPReportBlocks     uint64 `json:"rtcpReportBlocks"`
	RTCPMatchedReports   uint64 `json:"rtcpMatchedReports"`
	RTCPReportedSSRC     uint32 `json:"rtcpReportedSsrc"`
	RTCPFractionLost     uint32 `json:"rtcpFractionLost"`
	RTCPTotalLost        uint32 `json:"rtcpTotalLost"`
	RTCPLastSequence     uint32 `json:"rtcpLastSequence"`
	RTCPJitter           uint32 `json:"rtcpJitter"`
	RTCPLastSenderReport uint32 `json:"rtcpLastSenderReport"`
	RTCPPLI              uint64 `json:"rtcpPli"`
	RTCPFIR              uint64 `json:"rtcpFir"`
	RTCPNACK             uint64 `json:"rtcpNack"`
}

func (s *Session) Stats() SessionStats {
	var localSSRC uint32
	if s.Local != nil {
		localSSRC = s.Local.SSRC
	}
	return SessionStats{
		LocalSSRC:            localSSRC,
		RTPPacketsSent:       s.rtpPacketsSent.Load(),
		RTPBytesSent:         s.rtpBytesSent.Load(),
		RTPWriteErrors:       s.rtpWriteErrors.Load(),
		RTCPDatagramsRecv:    s.rtcpDatagramsRecv.Load(),
		RTCPPacketsRecv:      s.rtcpPacketsRecv.Load(),
		RTCPDecryptErrors:    s.rtcpDecryptErrors.Load(),
		RTCPParseErrors:      s.rtcpParseErrors.Load(),
		RTCPReceiverReports:  s.rtcpReceiverReports.Load(),
		RTCPReportBlocks:     s.rtcpReportBlocks.Load(),
		RTCPMatchedReports:   s.rtcpMatchedReports.Load(),
		RTCPReportedSSRC:     s.rtcpReportedSSRC.Load(),
		RTCPFractionLost:     s.rtcpFractionLost.Load(),
		RTCPTotalLost:        s.rtcpTotalLost.Load(),
		RTCPLastSequence:     s.rtcpLastSequence.Load(),
		RTCPJitter:           s.rtcpJitter.Load(),
		RTCPLastSenderReport: s.rtcpLastSenderReport.Load(),
		RTCPPLI:              s.rtcpPLI.Load(),
		RTCPFIR:              s.rtcpFIR.Load(),
		RTCPNACK:             s.rtcpNACK.Load(),
	}
}

func (e *Endpoint) init() (err error) {
	e.addr = &net.UDPAddr{IP: net.ParseIP(e.Addr), Port: int(e.Port)}
	e.srtp, err = srtp.CreateContext(e.MasterKey, e.MasterSalt, profile(e.MasterKey))
	return
}

// SetZone selects the outgoing interface for an IPv6 link-local controller.
// HAP SetupEndpoints carries a bare fe80:: address, so the zone has to be
// recovered from the already-established HAP TCP connection.
func (s *Session) SetZone(zone string) {
	if zone == "" || s.Remote == nil {
		return
	}
	addr, ok := s.Remote.addr.(*net.UDPAddr)
	if !ok || addr == nil || addr.IP == nil {
		return
	}
	if addr.IP.To4() != nil || !addr.IP.IsLinkLocalUnicast() {
		return
	}
	addr.Zone = zone
}

func profile(key []byte) srtp.ProtectionProfile {
	switch len(key) {
	case 16:
		return srtp.ProtectionProfileAes128CmHmacSha1_80
		//case 32:
		//	return srtp.ProtectionProfileAes256CmHmacSha1_80
	}
	return 0
}

func (s *Session) init() error {
	if err := s.Local.init(); err != nil {
		return err
	}
	if err := s.Remote.init(); err != nil {
		return err
	}

	s.senderRTCP.SSRC = s.Local.SSRC
	// Scrypted emits an RTCP Sender Report immediately before the first RTP
	// packet. A zero senderTime makes WriteRTP do the same while subsequent
	// reports follow the controller-selected interval.
	s.senderTime = time.Time{}
	s.rtcpReady = make(chan struct{})

	return nil
}

// WaitForRTCP waits for the controller to punch the return UDP path. A timeout
// is advisory: Scrypted also continues streaming after one second because some
// local/HomeHub paths do not send an initial report.
func (s *Session) WaitForRTCP(timeout time.Duration, done <-chan struct{}) bool {
	if s.rtcpReady == nil {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.rtcpReady:
		return true
	case <-done:
		return false
	case <-timer.C:
		return false
	}
}

func (s *Session) LastRTCPAt() time.Time {
	value := s.lastRTCPUnixNano.Load()
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value)
}

func (s *Session) WriteRTP(packet *rtp.Packet) (int, error) {
	if s.Local.srtp == nil {
		return 0, nil // before init call
	}

	clone := rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Marker:         packet.Marker,
			PayloadType:    s.PayloadType,
			SequenceNumber: packet.SequenceNumber,
			Timestamp:      packet.Timestamp,
			SSRC:           s.Local.SSRC,
		},
		Payload: packet.Payload,
	}

	if now := time.Now(); now.After(s.senderTime) {
		// The first Sender Report precedes the first RTP packet, but its RTP
		// timestamp must already describe that packet. A zero timestamp makes
		// the controller establish an invalid NTP/RTP clock mapping.
		s.senderRTCP.NTPTime = ntpTime(now)
		s.senderRTCP.RTPTime = clone.Timestamp
		s.senderTime = now.Add(s.RTCPInterval)
		_, _ = s.WriteRTCP(&s.senderRTCP)
	}

	b, err := clone.Marshal()
	if err != nil {
		s.rtpWriteErrors.Add(1)
		return 0, err
	}

	s.senderRTCP.PacketCount++
	s.senderRTCP.RTPTime = clone.Timestamp
	s.senderRTCP.OctetCount += uint32(len(clone.Payload))

	if b, err = s.Local.srtp.EncryptRTP(nil, b, nil); err != nil {
		s.rtpWriteErrors.Add(1)
		return 0, err
	}

	n, err := s.conn.WriteTo(b, s.Remote.addr)
	if err != nil {
		s.rtpWriteErrors.Add(1)
		return n, err
	}
	s.rtpPacketsSent.Add(1)
	s.rtpBytesSent.Add(uint64(n))
	return n, nil
}

// ntpTime converts wall time to the 64-bit NTP timestamp required by RTCP
// Sender Reports: seconds since 1900 in the upper word and the fractional
// second in the lower word. Unix nanoseconds are not wire-compatible with NTP.
func ntpTime(value time.Time) uint64 {
	const unixToNTP = uint64(2_208_988_800)
	seconds := uint64(value.Unix()) + unixToNTP
	fraction := uint64(value.Nanosecond()) * (uint64(1) << 32) / 1_000_000_000
	return seconds<<32 | fraction
}

func (s *Session) WriteRTCP(packet rtcp.Packet) (int, error) {
	b, err := packet.Marshal()
	if err != nil {
		return 0, err
	}
	b, err = s.Local.srtp.EncryptRTCP(nil, b, nil)
	if err != nil {
		return 0, err
	}
	return s.conn.WriteTo(b, s.Remote.addr)
}

func (s *Session) ReadRTP(b []byte) {
	packet := &rtp.Packet{}

	b, err := s.Remote.srtp.DecryptRTP(nil, b, &packet.Header)
	if err != nil {
		return
	}

	if err = packet.Unmarshal(b); err != nil {
		return
	}

	if s.OnReadRTP != nil {
		s.OnReadRTP(packet)
	}
}

func (s *Session) ReadRTCP(b []byte) {
	s.rtcpDatagramsRecv.Add(1)
	s.lastRTCPUnixNano.Store(time.Now().UnixNano())
	s.rtcpReadyOnce.Do(func() {
		close(s.rtcpReady)
	})

	header := rtcp.Header{}
	b, err := s.Remote.srtp.DecryptRTCP(nil, b, &header)
	if err != nil {
		s.rtcpDecryptErrors.Add(1)
		return
	}

	packets, err := rtcp.Unmarshal(b)
	if err != nil {
		s.rtcpParseErrors.Add(1)
		return
	}
	s.rtcpPacketsRecv.Add(uint64(len(packets)))

	sendReceiverReport := false
	for _, packet := range packets {
		switch report := packet.(type) {
		case *rtcp.ReceiverReport:
			s.rtcpReceiverReports.Add(1)
			s.rtcpReportBlocks.Add(uint64(len(report.Reports)))
			for _, block := range report.Reports {
				s.rtcpReportedSSRC.Store(block.SSRC)
				if s.Local == nil || block.SSRC != s.Local.SSRC {
					continue
				}
				s.rtcpMatchedReports.Add(1)
				s.rtcpFractionLost.Store(uint32(block.FractionLost))
				s.rtcpTotalLost.Store(block.TotalLost)
				s.rtcpLastSequence.Store(block.LastSequenceNumber)
				s.rtcpJitter.Store(block.Jitter)
				s.rtcpLastSenderReport.Store(block.LastSenderReport)
			}
		case *rtcp.PictureLossIndication:
			s.rtcpPLI.Add(1)
		case *rtcp.FullIntraRequest:
			s.rtcpFIR.Add(1)
		case *rtcp.TransportLayerNack:
			s.rtcpNACK.Add(1)
		case *rtcp.SenderReport:
			sendReceiverReport = true
		}
	}

	if !sendReceiverReport {
		return
	}

	receiverRTCP := rtcp.ReceiverReport{SSRC: s.Local.SSRC}
	_, _ = s.WriteRTCP(&receiverRTCP)
}
