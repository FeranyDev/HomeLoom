package xiaomi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // #nosec G501 -- the miIO wire protocol requires MD5.
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

const (
	miotLocalPort          = "54321"
	miotLocalMaximumPacket = 64 * 1024
)

func localMIoTTimeout(requestTimeout time.Duration) time.Duration {
	const maximum = 3 * time.Second
	if requestTimeout <= 0 || requestTimeout > maximum {
		return maximum
	}
	return requestTimeout
}

var miotHelloPacket = func() []byte {
	packet := make([]byte, 32)
	binary.BigEndian.PutUint16(packet[0:2], 0x2131)
	binary.BigEndian.PutUint16(packet[2:4], 32)
	for index := 4; index < len(packet); index++ {
		packet[index] = 0xff
	}
	return packet
}()

type miotLocalAccess struct {
	Host  string
	Token string
}

type miotLocalClient interface {
	GetProperties(context.Context, miotLocalAccess, []cloudProperty) ([]cloudProperty, error)
	SetProperties(context.Context, miotLocalAccess, []cloudProperty) ([]cloudProperty, error)
	Action(context.Context, miotLocalAccess, cloudAction) error
}

type udpMIoTLocalClient struct {
	timeout  time.Duration
	sequence atomic.Uint32
}

func newUDPMIoTLocalClient(timeout time.Duration) *udpMIoTLocalClient {
	return &udpMIoTLocalClient{timeout: timeout}
}

func (c *udpMIoTLocalClient) GetProperties(ctx context.Context, access miotLocalAccess, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.request(ctx, access, "get_properties", input, &result)
	return result, err
}

func (c *udpMIoTLocalClient) SetProperties(ctx context.Context, access miotLocalAccess, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.request(ctx, access, "set_properties", input, &result)
	return result, err
}

func (c *udpMIoTLocalClient) Action(ctx context.Context, access miotLocalAccess, input cloudAction) error {
	var result json.RawMessage
	if err := c.request(ctx, access, "action", input, &result); err != nil {
		return err
	}
	var status struct {
		Code int `json:"code"`
	}
	if len(result) > 0 && result[0] == '{' && json.Unmarshal(result, &status) == nil && status.Code != 0 {
		return fmt.Errorf("local MIoT action rejected with code %d", status.Code)
	}
	return nil
}

func (c *udpMIoTLocalClient) request(ctx context.Context, access miotLocalAccess, method string, parameters, output any) error {
	host, token, err := parseLocalAccess(access)
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(host, miotLocalPort))
	if err != nil {
		return fmt.Errorf("connect local MIoT device: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	if _, err := connection.Write(miotHelloPacket); err != nil {
		return fmt.Errorf("send local MIoT handshake: %w", err)
	}
	buffer := make([]byte, miotLocalMaximumPacket)
	read, err := connection.Read(buffer)
	if err != nil {
		return fmt.Errorf("receive local MIoT handshake: %w", err)
	}
	deviceID, timestamp, err := parseMIoTHandshake(buffer[:read])
	if err != nil {
		return err
	}
	requestID := c.sequence.Add(1)
	if requestID == 0 || requestID > 9999 {
		c.sequence.Store(1)
		requestID = 1
	}
	payload, err := json.Marshal(map[string]any{"id": requestID, "method": method, "params": parameters})
	if err != nil {
		return err
	}
	payload = append(payload, 0)
	packet, err := buildMIoTPacket(deviceID, timestamp+1, token, payload)
	if err != nil {
		return err
	}
	if _, err := connection.Write(packet); err != nil {
		return fmt.Errorf("send local MIoT request: %w", err)
	}
	read, err = connection.Read(buffer)
	if err != nil {
		return fmt.Errorf("receive local MIoT response: %w", err)
	}
	responsePayload, err := parseMIoTPacket(buffer[:read], token)
	if err != nil {
		return err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		return fmt.Errorf("decode local MIoT response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("local MIoT error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if output != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, output); err != nil {
			return fmt.Errorf("decode local MIoT result: %w", err)
		}
	}
	return nil
}

func validLocalAccess(host, token string) bool {
	_, _, err := parseLocalAccess(miotLocalAccess{Host: host, Token: token})
	return err == nil
}

func parseLocalAccess(access miotLocalAccess) (string, []byte, error) {
	host := strings.TrimSpace(access.Host)
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || (!ip.IsPrivate() && !ip.IsLinkLocalUnicast()) {
		return "", nil, errors.New("local MIoT address must be a private or link-local IP")
	}
	tokenText := strings.TrimSpace(access.Token)
	token, err := hex.DecodeString(tokenText)
	if err != nil || len(token) != 16 {
		token, err = base64.StdEncoding.DecodeString(tokenText)
	}
	if err != nil || len(token) != 16 {
		return "", nil, errors.New("local MIoT token must contain 16 bytes")
	}
	return ip.String(), token, nil
}

func parseMIoTHandshake(packet []byte) ([4]byte, uint32, error) {
	var deviceID [4]byte
	if len(packet) < 32 || binary.BigEndian.Uint16(packet[0:2]) != 0x2131 || binary.BigEndian.Uint16(packet[2:4]) != 32 {
		return deviceID, 0, errors.New("invalid local MIoT handshake")
	}
	copy(deviceID[:], packet[8:12])
	return deviceID, binary.BigEndian.Uint32(packet[12:16]), nil
}

func buildMIoTPacket(deviceID [4]byte, timestamp uint32, token, plaintext []byte) ([]byte, error) {
	encrypted, err := encryptMIoTPayload(token, plaintext)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 32+len(encrypted))
	binary.BigEndian.PutUint16(packet[0:2], 0x2131)
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[8:12], deviceID[:])
	binary.BigEndian.PutUint32(packet[12:16], timestamp)
	checksum := miotChecksum(packet[:16], token, encrypted)
	copy(packet[16:32], checksum[:])
	copy(packet[32:], encrypted)
	return packet, nil
}

func parseMIoTPacket(packet, token []byte) ([]byte, error) {
	if len(packet) < 32 || binary.BigEndian.Uint16(packet[0:2]) != 0x2131 {
		return nil, errors.New("invalid local MIoT response header")
	}
	length := int(binary.BigEndian.Uint16(packet[2:4]))
	if length != len(packet) || length == 32 {
		return nil, errors.New("invalid local MIoT response length")
	}
	encrypted := packet[32:]
	checksum := miotChecksum(packet[:16], token, encrypted)
	if subtle.ConstantTimeCompare(packet[16:32], checksum[:]) != 1 {
		return nil, errors.New("local MIoT checksum mismatch; token may be invalid")
	}
	plaintext, err := decryptMIoTPayload(token, encrypted)
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(plaintext, "\x00"), nil
}

func miotChecksum(header, token, encrypted []byte) [16]byte {
	value := make([]byte, 0, len(header)+len(token)+len(encrypted))
	value = append(value, header...)
	value = append(value, token...)
	value = append(value, encrypted...)
	return md5.Sum(value) // #nosec G401 -- the miIO wire protocol requires MD5.
}

func encryptMIoTPayload(token, plaintext []byte) ([]byte, error) {
	block, iv, err := miotCipher(token)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for index := len(plaintext); index < len(padded); index++ {
		padded[index] = byte(padding)
	}
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return encrypted, nil
}

func decryptMIoTPayload(token, encrypted []byte) ([]byte, error) {
	block, iv, err := miotCipher(token)
	if err != nil {
		return nil, err
	}
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, errors.New("invalid local MIoT encrypted payload length")
	}
	plaintext := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, encrypted)
	padding := int(plaintext[len(plaintext)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plaintext) {
		return nil, errors.New("invalid local MIoT payload padding; token may be invalid")
	}
	for _, value := range plaintext[len(plaintext)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid local MIoT payload padding; token may be invalid")
		}
	}
	return plaintext[:len(plaintext)-padding], nil
}

func miotCipher(token []byte) (cipher.Block, []byte, error) {
	if len(token) != 16 {
		return nil, nil, errors.New("local MIoT token must contain 16 bytes")
	}
	key := md5.Sum(token) // #nosec G401 -- the miIO wire protocol requires MD5.
	ivInput := append(append([]byte(nil), key[:]...), token...)
	iv := md5.Sum(ivInput) // #nosec G401 -- the miIO wire protocol requires MD5.
	block, err := aes.NewCipher(key[:])
	return block, iv[:], err
}
