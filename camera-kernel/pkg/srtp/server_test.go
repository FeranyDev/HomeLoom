package srtp

import (
	"net"
	"testing"
)

func TestServerReleasesSocketAndCanStartFreshSession(t *testing.T) {
	if !canListenUDP(t) {
		t.Skip("UDP listen not permitted in this environment")
	}
	server := NewServer("127.0.0.1:0")
	first := testSession(1001, 2001)
	if err := server.AddSession(first); err != nil {
		t.Fatal(err)
	}
	firstConn := server.conn
	if firstConn == nil || len(server.sessions) != 1 {
		t.Fatalf("first session was not registered: conn=%v sessions=%d", firstConn, len(server.sessions))
	}
	server.DelSession(first)
	if server.conn != nil || len(server.sessions) != 0 {
		t.Fatalf("server retained stopped session: conn=%v sessions=%d", server.conn, len(server.sessions))
	}

	second := testSession(1002, 2002)
	if err := server.AddSession(second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.DelSession(second) })
	if server.conn == nil || server.conn == firstConn || len(server.sessions) != 1 {
		t.Fatalf("fresh session did not receive a fresh socket: conn=%v sessions=%d", server.conn, len(server.sessions))
	}
}

func testSession(localSSRC, remoteSSRC uint32) *Session {
	return &Session{
		Local: &Endpoint{
			Addr: "127.0.0.1", Port: 0, MasterKey: []byte("0123456789abcdef"),
			MasterSalt: []byte("0123456789abcd"), SSRC: localSSRC,
		},
		Remote: &Endpoint{
			Addr: "127.0.0.1", Port: 9999, MasterKey: []byte("fedcba9876543210"),
			MasterSalt: []byte("dcba9876543210"), SSRC: remoteSSRC,
		},
		RTCPInterval: 500,
	}
}

func TestEnsureListeningBindsUDPPort(t *testing.T) {
	if !canListenUDP(t) {
		t.Skip("UDP listen not permitted in this environment")
	}
	server := NewServer("127.0.0.1:0")
	if err := server.EnsureListening(); err != nil {
		t.Fatal(err)
	}
	defer server.conn.Close()
	if server.Port() == 0 {
		t.Fatal("EnsureListening left port unset")
	}
	if err := server.EnsureListening(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionServersUseIndependentAddressFamilySockets(t *testing.T) {
	base := NewServer("0.0.0.0:8443")
	if got := base.NewSessionServer(false).address; got != "0.0.0.0:0" {
		t.Fatalf("IPv4 session address = %q", got)
	}
	if got := base.NewSessionServer(true).address; got != "[::]:0" {
		t.Fatalf("IPv6 session address = %q", got)
	}
}

func TestWildcardSessionServerIgnoresConfiguredConcreteHost(t *testing.T) {
	base := NewServer("127.0.0.1:8443")
	if got := base.NewWildcardSessionServer(false).address; got != "0.0.0.0:0" {
		t.Fatalf("wildcard IPv4 session address = %q", got)
	}
	if got := base.NewWildcardSessionServer(true).address; got != "[::]:0" {
		t.Fatalf("wildcard IPv6 session address = %q", got)
	}
}

func TestSessionServersBindAdvertisedConcreteAddress(t *testing.T) {
	base := NewServer("0.0.0.0:8443")
	if got := base.NewSessionServerAt(false, "192.0.2.30").address; got != "192.0.2.30:0" {
		t.Fatalf("concrete IPv4 session address = %q", got)
	}
	if got := base.NewSessionServerAt(true, "fe80::1234%en0").address; got != "[fe80::1234%en0]:0" {
		t.Fatalf("concrete IPv6 session address = %q", got)
	}
	if got := base.NewSessionServerAt(false, "fe80::1234").address; got != "0.0.0.0:0" {
		t.Fatalf("wrong-family session address = %q", got)
	}
}

func TestSessionServersBindIndependentPorts(t *testing.T) {
	if !canListenUDP(t) {
		t.Skip("UDP listen not permitted in this environment")
	}
	base := NewServer("127.0.0.1:8443")
	video := base.NewSessionServer(false)
	audio := base.NewSessionServer(false)
	if err := video.EnsureListening(); err != nil {
		t.Fatal(err)
	}
	defer video.Close()
	if err := audio.EnsureListening(); err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	if video.Port() == audio.Port() {
		t.Fatalf("video/audio session port = %d", video.Port())
	}
}

func TestSingleSessionSocketRoutesUnknownControllerSSRC(t *testing.T) {
	server := NewServer("127.0.0.1:0")
	session := testSession(1001, 2001)
	server.sessions[session.Remote.SSRC] = session
	if got := server.GetSession(9999); got != session {
		t.Fatalf("single-session fallback = %p, want %p", got, session)
	}
	server.sessions[3001] = testSession(1002, 3001)
	if got := server.GetSession(9999); got != nil {
		t.Fatalf("multi-session socket routed unknown SSRC to %p", got)
	}
}

func canListenUDP(t *testing.T) bool {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
