package mips

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// FieldType is the one-byte type marker used by the local MIoT Pub/Sub
// envelope carried inside MQTT payloads.
type FieldType byte

const (
	FieldID FieldType = iota
	FieldReplyTopic
	FieldPayload
	FieldFrom
)

// Message is one decoded MIPS envelope.
type Message struct {
	ID         uint32
	From       string
	ReplyTopic string
	Payload    string
}

// Encode builds a MIPS envelope. String fields are NUL terminated and every
// field is encoded as: uint32_le(length), uint8(type), data.
func Encode(msg Message) ([]byte, error) {
	if msg.ID == 0 {
		return nil, errors.New("mips message ID must not be zero")
	}
	if msg.Payload == "" {
		return nil, errors.New("mips payload must not be empty")
	}

	var out bytes.Buffer
	id := make([]byte, 4)
	binary.LittleEndian.PutUint32(id, msg.ID)
	writeField(&out, FieldID, id)

	// Keep the observed gateway-compatible order: ID, FROM, RET_TOPIC, PAYLOAD.
	if msg.From != "" {
		writeCStringField(&out, FieldFrom, msg.From)
	}
	if msg.ReplyTopic != "" {
		writeCStringField(&out, FieldReplyTopic, msg.ReplyTopic)
	}
	writeCStringField(&out, FieldPayload, msg.Payload)
	return out.Bytes(), nil
}

func writeCStringField(out *bytes.Buffer, typ FieldType, value string) {
	data := append([]byte(value), 0)
	writeField(out, typ, data)
}

func writeField(out *bytes.Buffer, typ FieldType, data []byte) {
	_ = binary.Write(out, binary.LittleEndian, uint32(len(data)))
	_ = out.WriteByte(byte(typ))
	_, _ = out.Write(data)
}

// Decode parses a MIPS envelope and rejects truncated or malformed fields.
func Decode(data []byte) (Message, error) {
	var msg Message
	for offset := 0; offset < len(data); {
		if len(data)-offset < 5 {
			return Message{}, fmt.Errorf("truncated mips field header at byte %d", offset)
		}
		length := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		typ := FieldType(data[offset+4])
		offset += 5
		if length < 0 || length > len(data)-offset {
			return Message{}, fmt.Errorf("invalid mips field length %d at byte %d", length, offset-5)
		}
		field := data[offset : offset+length]
		offset += length

		switch typ {
		case FieldID:
			if len(field) != 4 {
				return Message{}, fmt.Errorf("invalid ID field length: %d", len(field))
			}
			msg.ID = binary.LittleEndian.Uint32(field)
		case FieldReplyTopic:
			msg.ReplyTopic = decodeCString(field)
		case FieldPayload:
			msg.Payload = decodeCString(field)
		case FieldFrom:
			msg.From = decodeCString(field)
		default:
			// Unknown fields are skipped to keep forward compatibility.
		}
	}
	if msg.ID == 0 {
		return Message{}, errors.New("mips envelope has no valid ID")
	}
	if msg.Payload == "" {
		return Message{}, errors.New("mips envelope has no payload")
	}
	return msg, nil
}

func decodeCString(data []byte) string {
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	return string(data)
}
