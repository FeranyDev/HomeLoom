package srtp

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	pionsrtp "github.com/pion/srtp/v3"
)

func TestNTPTimeUses1900EpochAndFraction(t *testing.T) {
	value := time.Date(1970, time.January, 1, 0, 0, 0, 500_000_000, time.UTC)
	got := ntpTime(value)
	const want = uint64(2_208_988_800)<<32 | uint64(1)<<31
	if got != want {
		t.Fatalf("ntpTime() = 0x%x, want 0x%x", got, want)
	}
}

func TestSessionSetsZoneForIPv6LinkLocalRemote(t *testing.T) {
	remote := &Endpoint{
		Addr:       "fe80::1234",
		Port:       5000,
		MasterKey:  make([]byte, 16),
		MasterSalt: make([]byte, 14),
	}
	if err := remote.init(); err != nil {
		t.Fatal(err)
	}
	session := &Session{Remote: remote}
	session.SetZone("en0")

	addr, ok := remote.addr.(*net.UDPAddr)
	if !ok || addr.Zone != "en0" {
		t.Fatalf("link-local remote zone = %#v", remote.addr)
	}
}

func TestSessionDoesNotSetZoneForIPv4OrGlobalIPv6(t *testing.T) {
	for _, address := range []string{"192.0.2.10", "2001:db8::10"} {
		remote := &Endpoint{
			Addr:       address,
			Port:       5000,
			MasterKey:  make([]byte, 16),
			MasterSalt: make([]byte, 14),
		}
		if err := remote.init(); err != nil {
			t.Fatal(err)
		}
		session := &Session{Remote: remote}
		session.SetZone("en0")
		if zone := remote.addr.(*net.UDPAddr).Zone; zone != "" {
			t.Fatalf("remote %s unexpectedly received zone %q", address, zone)
		}
	}
}

func TestSessionStatsTrackRTPWritesAndErrors(t *testing.T) {
	session := testSession(1001, 2001)
	session.RTCPInterval = time.Hour
	if err := session.init(); err != nil {
		t.Fatal(err)
	}
	conn := &packetConnStub{}
	session.conn = conn
	packet := &rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 2},
		Payload: []byte{1, 2, 3, 4},
	}

	written, err := session.WriteRTP(packet)
	if err != nil {
		t.Fatal(err)
	}
	status := session.Stats()
	if status.RTPPacketsSent != 1 || status.RTPBytesSent != uint64(written) || status.RTPWriteErrors != 0 {
		t.Fatalf("successful RTP stats = %#v", status)
	}
	if conn.writeCalls != 2 {
		t.Fatalf("first RTP packet produced %d UDP writes, want initial RTCP SR + RTP", conn.writeCalls)
	}
	if len(conn.writes) != 2 {
		t.Fatalf("captured UDP writes = %d, want 2", len(conn.writes))
	}
	decrypt, err := pionsrtp.CreateContext(
		session.Local.MasterKey,
		session.Local.MasterSalt,
		profile(session.Local.MasterKey),
	)
	if err != nil {
		t.Fatal(err)
	}
	header := rtcp.Header{}
	plain, err := decrypt.DecryptRTCP(nil, conn.writes[0], &header)
	if err != nil {
		t.Fatalf("decrypt initial Sender Report: %v", err)
	}
	packets, err := rtcp.Unmarshal(plain)
	if err != nil {
		t.Fatalf("unmarshal initial Sender Report: %v", err)
	}
	report, ok := packets[0].(*rtcp.SenderReport)
	if !ok || report.RTPTime != packet.Timestamp {
		t.Fatalf("initial Sender Report = %#v, want RTP timestamp %d", packets[0], packet.Timestamp)
	}

	conn.writeErr = errors.New("write failed")
	if _, err = session.WriteRTP(packet); err == nil {
		t.Fatal("failed RTP write returned no error")
	}
	status = session.Stats()
	if status.RTPPacketsSent != 1 || status.RTPWriteErrors != 1 {
		t.Fatalf("failed RTP stats = %#v", status)
	}
}

func TestSessionRTCPReturnPathReadinessCountsInvalidDatagrams(t *testing.T) {
	session := testSession(1001, 2001)
	session.RTCPInterval = time.Hour
	if err := session.init(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ready := make(chan bool, 1)
	go func() {
		ready <- session.WaitForRTCP(time.Second, done)
	}()

	session.ReadRTCP(make([]byte, 20))

	if !<-ready {
		t.Fatal("RTCP datagram did not mark the controller UDP return path ready")
	}
	status := session.Stats()
	if status.RTCPDatagramsRecv != 1 || status.RTCPPacketsRecv != 0 || status.RTCPDecryptErrors != 1 {
		t.Fatalf("invalid RTCP datagram stats = %#v", status)
	}
	if session.LastRTCPAt().IsZero() {
		t.Fatal("RTCP datagram did not update liveness time")
	}
}

func TestSessionParsesControllerReceiverFeedback(t *testing.T) {
	session := testSession(1001, 2001)
	if err := session.init(); err != nil {
		t.Fatal(err)
	}
	plain, err := rtcp.Marshal([]rtcp.Packet{
		&rtcp.ReceiverReport{
			SSRC: 2001,
			Reports: []rtcp.ReceptionReport{{
				SSRC:               1001,
				FractionLost:       7,
				TotalLost:          3,
				LastSequenceNumber: 1234,
				Jitter:             55,
				LastSenderReport:   66,
			}},
		},
		&rtcp.PictureLossIndication{SenderSSRC: 2001, MediaSSRC: 1001},
		&rtcp.FullIntraRequest{
			SenderSSRC: 2001,
			FIR:        []rtcp.FIREntry{{SSRC: 1001, SequenceNumber: 1}},
		},
		&rtcp.TransportLayerNack{
			SenderSSRC: 2001,
			MediaSSRC:  1001,
			Nacks:      []rtcp.NackPair{{PacketID: 10}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller, err := pionsrtp.CreateContext(
		session.Remote.MasterKey,
		session.Remote.MasterSalt,
		profile(session.Remote.MasterKey),
	)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := controller.EncryptRTCP(nil, plain, nil)
	if err != nil {
		t.Fatal(err)
	}

	session.ReadRTCP(encrypted)

	status := session.Stats()
	if status.LocalSSRC != 1001 ||
		status.RTCPDatagramsRecv != 1 ||
		status.RTCPPacketsRecv != 4 ||
		status.RTCPDecryptErrors != 0 ||
		status.RTCPParseErrors != 0 ||
		status.RTCPReceiverReports != 1 ||
		status.RTCPReportBlocks != 1 ||
		status.RTCPMatchedReports != 1 ||
		status.RTCPReportedSSRC != 1001 ||
		status.RTCPFractionLost != 7 ||
		status.RTCPTotalLost != 3 ||
		status.RTCPLastSequence != 1234 ||
		status.RTCPJitter != 55 ||
		status.RTCPLastSenderReport != 66 ||
		status.RTCPPLI != 1 ||
		status.RTCPFIR != 1 ||
		status.RTCPNACK != 1 {
		t.Fatalf("controller RTCP feedback stats = %#v", status)
	}
}

type packetConnStub struct {
	writeErr   error
	writeCalls int
	writes     [][]byte
}

func (c *packetConnStub) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (c *packetConnStub) WriteTo(payload []byte, _ net.Addr) (int, error) {
	c.writeCalls++
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return len(payload), nil
}

func (c *packetConnStub) Close() error                     { return nil }
func (c *packetConnStub) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *packetConnStub) SetDeadline(time.Time) error      { return nil }
func (c *packetConnStub) SetReadDeadline(time.Time) error  { return nil }
func (c *packetConnStub) SetWriteDeadline(time.Time) error { return nil }
