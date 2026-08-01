package homekit

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
	"github.com/AlexxIT/go2rtc/pkg/srtp"
)

type fakeAccessoryServer struct {
	accessory *hap.Accessory
	consumer  *Consumer
	srtp      *srtp.Server
	events    map[uint64]bool
}

func (s *fakeAccessoryServer) GetPair(string) []byte        { return nil }
func (s *fakeAccessoryServer) AddPair(string, []byte, byte) {}
func (s *fakeAccessoryServer) DelPair(string)               {}
func (s *fakeAccessoryServer) GetAccessories(net.Conn) []*hap.Accessory {
	return []*hap.Accessory{s.accessory}
}
func (s *fakeAccessoryServer) GetCharacteristic(_ net.Conn, _ uint8, iid uint64) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		return nil
	}
	if char.Type == camera.TypeSetupEndpoints && s.consumer != nil {
		value, err := tlv8.MarshalBase64(s.consumer.GetAnswer())
		if err != nil {
			return nil
		}
		return value
	}
	return char.Value
}
func (s *fakeAccessoryServer) SetCharacteristicEvent(_ net.Conn, _ uint8, iid uint64, enabled bool) {
	if s.events == nil {
		s.events = make(map[uint64]bool)
	}
	s.events[iid] = enabled
}
func (s *fakeAccessoryServer) SetCharacteristic(conn net.Conn, _ uint8, iid uint64, value any, writeResponse bool) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil || char.Type != camera.TypeSetupEndpoints {
		return nil
	}
	var offer camera.SetupEndpointsRequest
	if err := tlv8.UnmarshalBase64(value, &offer); err != nil {
		return nil
	}
	consumer := NewConsumer(conn, s.srtp)
	consumer.SetOffer(&offer)
	s.consumer = consumer
	encoded, err := tlv8.MarshalBase64(consumer.GetAnswer())
	if err != nil {
		return nil
	}
	char.Value = encoded
	if writeResponse {
		return encoded
	}
	return nil
}
func (s *fakeAccessoryServer) GetImage(net.Conn, int, int) []byte { return nil }

func TestServerHandlerReturnsSetupEndpointsWriteResponse(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	setup := accessory.Services[1].GetCharacter(camera.TypeSetupEndpoints)
	if setup == nil {
		t.Fatal("missing SetupEndpoints")
	}

	server := &fakeAccessoryServer{
		accessory: accessory,
		srtp:      srtp.NewServer("127.0.0.1:0"),
	}
	localAddress := firstPrivateIPv4()

	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServerHandler(server)(&tcpAddrConn{
			Conn:  peer,
			local: &net.TCPAddr{IP: net.ParseIP(localAddress), Port: 51826},
		})
	}()

	offer := camera.SetupEndpointsRequest{
		SessionID: "0123456789abcdef",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "192.0.2.10", VideoRTPPort: 5000, AudioRTPPort: 5002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
		AudioCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
	}
	offerValue, err := tlv8.MarshalBase64(offer)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"characteristics": []map[string]any{{
			"aid":   accessory.AID,
			"iid":   setup.IID,
			"value": offerValue,
			"r":     true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := "PUT /characteristics HTTP/1.1\r\nHost: camera\r\nContent-Type: application/hap+json\r\nContent-Length: " +
		itoa(len(body)) + "\r\n\r\n" + string(body)
	if _, err := io.WriteString(client, req); err != nil {
		t.Fatal(err)
	}

	res, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, body=%s", res.StatusCode, readAll(res.Body))
	}
	payload := readAll(res.Body)
	var decoded hap.JSONCharacters
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode response: %v body=%s", err, payload)
	}
	if len(decoded.Value) != 1 || decoded.Value[0].Value == nil {
		t.Fatalf("write-response payload = %s", payload)
	}
	var answer camera.SetupEndpointsResponse
	if err := tlv8.UnmarshalBase64(decoded.Value[0].Value, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Status != camera.SetupEndpointsStatusSuccess {
		t.Fatalf("answer status = %d", answer.Status)
	}
	if answer.Address.IPAddr != localAddress {
		t.Fatalf("answer address = %#v", answer.Address)
	}
	if answer.VideoCrypto.CryptoSuite != camera.CryptoAES_CM_128_HMAC_SHA1_80 ||
		answer.AudioCrypto.CryptoSuite != camera.CryptoAES_CM_128_HMAC_SHA1_80 ||
		answer.VideoSSRC == 0 || answer.AudioSSRC == 0 {
		t.Fatalf("incomplete answer = %#v", answer)
	}

	// Ordinary selected-stream writes still return 204.
	selected := accessory.Services[1].GetCharacter(camera.TypeSelectedStreamConfiguration)
	selectedBody, err := json.Marshal(map[string]any{
		"characteristics": []map[string]any{{
			"aid":   accessory.AID,
			"iid":   selected.IID,
			"value": selected.Value,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req = "PUT /characteristics HTTP/1.1\r\nHost: camera\r\nContent-Type: application/hap+json\r\nContent-Length: " +
		itoa(len(selectedBody)) + "\r\n\r\n" + string(selectedBody)
	if _, err := io.WriteString(client, req); err != nil {
		t.Fatal(err)
	}
	res, err = http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("selected stream status = %d body=%s", res.StatusCode, readAll(res.Body))
	}

	_ = client.Close()
	if err := <-errCh; err != nil && !strings.Contains(err.Error(), "closed") && err != io.EOF && err != io.ErrClosedPipe {
		t.Fatalf("handler error: %v", err)
	}
}

func TestServerHandlerRegistersCharacteristicEventWithoutTreatingItAsValueWrite(t *testing.T) {
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	streaming := accessory.Services[1].GetCharacter(camera.TypeStreamingStatus)
	if streaming == nil {
		t.Fatal("missing StreamingStatus")
	}
	server := &fakeAccessoryServer{accessory: accessory}

	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServerHandler(server)(peer)
	}()

	body, err := json.Marshal(map[string]any{
		"characteristics": []map[string]any{{
			"aid": accessory.AID,
			"iid": streaming.IID,
			"ev":  true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := "PUT /characteristics HTTP/1.1\r\nHost: camera\r\nContent-Type: application/hap+json\r\nContent-Length: " +
		itoa(len(body)) + "\r\n\r\n" + string(body)
	if _, err := io.WriteString(client, req); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("event subscription status = %d body=%s", res.StatusCode, readAll(res.Body))
	}
	if !server.events[streaming.IID] {
		t.Fatalf("StreamingStatus event was not registered: %#v", server.events)
	}

	_ = client.Close()
	if err := <-errCh; err != nil && !strings.Contains(err.Error(), "closed") && err != io.EOF && err != io.ErrClosedPipe {
		t.Fatalf("handler error: %v", err)
	}
}

func TestServerHandlerUsesSetupEndpointsReadAfterWrite(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	setup := accessory.Services[1].GetCharacter(camera.TypeSetupEndpoints)
	server := &fakeAccessoryServer{accessory: accessory, srtp: srtp.NewServer("127.0.0.1:0")}
	localAddress := firstPrivateIPv4()
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- ServerHandler(server)(&tcpAddrConn{
			Conn:  peer,
			local: &net.TCPAddr{IP: net.ParseIP(localAddress), Port: 51826},
		})
	}()

	offerValue, err := tlv8.MarshalBase64(camera.SetupEndpointsRequest{
		SessionID: "fedcba9876543210",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "192.0.2.11", VideoRTPPort: 6000, AudioRTPPort: 6002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
		AudioCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"characteristics": []map[string]any{{
			"aid":   accessory.AID,
			"iid":   setup.IID,
			"value": offerValue,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := "PUT /characteristics HTTP/1.1\r\nHost: camera\r\nContent-Type: application/hap+json\r\nContent-Length: " +
		itoa(len(body)) + "\r\n\r\n" + string(body)
	if _, err := io.WriteString(client, req); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", res.StatusCode, readAll(res.Body))
	}

	get := "GET /characteristics?id=" + itoa(int(accessory.AID)) + "." + itoa(int(setup.IID)) +
		" HTTP/1.1\r\nHost: camera\r\n\r\n"
	if _, err := io.WriteString(client, get); err != nil {
		t.Fatal(err)
	}
	res, err = http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", res.StatusCode, readAll(res.Body))
	}
	var response hap.JSONCharacters
	if err := json.Unmarshal(readAll(res.Body), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Value) != 1 || response.Value[0].Value == nil {
		t.Fatalf("missing SetupEndpoints GET response: %#v", response)
	}
	var answer camera.SetupEndpointsResponse
	if err := tlv8.UnmarshalBase64(response.Value[0].Value, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Status != camera.SetupEndpointsStatusSuccess || answer.Address.IPAddr != localAddress {
		t.Fatalf("SetupEndpoints GET answer = %#v", answer)
	}
	_ = client.Close()
	<-errCh
}

func TestGetAnswerIsIdempotent(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	localAddress := firstPrivateIPv4()
	consumer := NewConsumer(&tcpAddrConn{Conn: local, local: &net.TCPAddr{IP: net.ParseIP(localAddress), Port: 51826}}, srtp.NewServer("127.0.0.1:0"))
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID:   "0123456789abcdef",
		Address:     camera.Address{IPVersion: 0, IPAddr: "192.0.2.10", VideoRTPPort: 5000, AudioRTPPort: 5002},
		VideoCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
		AudioCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
	})
	first := consumer.GetAnswer()
	second := consumer.GetAnswer()
	if first.VideoCrypto.MasterKey != second.VideoCrypto.MasterKey ||
		first.AudioCrypto.MasterKey != second.AudioCrypto.MasterKey ||
		first.VideoSSRC != second.VideoSSRC ||
		first.AudioSSRC != second.AudioSSRC ||
		first.Address.VideoRTPPort != second.Address.VideoRTPPort ||
		first.Address.AudioRTPPort != second.Address.AudioRTPPort {
		t.Fatalf("GetAnswer regenerated SRTP material: first=%#v second=%#v", first, second)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func readAll(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}
