package xiaomi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	mipsFieldID byte = iota
	mipsFieldReplyTopic
	mipsFieldPayload
	mipsFieldFrom
)

type mipsMessage struct {
	ID         uint32
	From       string
	ReplyTopic string
	Payload    string
}

func encodeMIPS(message mipsMessage) ([]byte, error) {
	if message.ID == 0 || message.Payload == "" {
		return nil, errors.New("MIPS message requires a non-zero ID and payload")
	}
	var output bytes.Buffer
	id := make([]byte, 4)
	binary.LittleEndian.PutUint32(id, message.ID)
	writeMIPSField(&output, mipsFieldID, id)
	if message.From != "" {
		writeMIPSText(&output, mipsFieldFrom, message.From)
	}
	if message.ReplyTopic != "" {
		writeMIPSText(&output, mipsFieldReplyTopic, message.ReplyTopic)
	}
	writeMIPSText(&output, mipsFieldPayload, message.Payload)
	return output.Bytes(), nil
}

func writeMIPSText(output *bytes.Buffer, kind byte, value string) {
	writeMIPSField(output, kind, append([]byte(value), 0))
}

func writeMIPSField(output *bytes.Buffer, kind byte, value []byte) {
	_ = binary.Write(output, binary.LittleEndian, uint32(len(value)))
	_ = output.WriteByte(kind)
	_, _ = output.Write(value)
}

func decodeMIPS(data []byte) (mipsMessage, error) {
	var message mipsMessage
	for offset := 0; offset < len(data); {
		if len(data)-offset < 5 {
			return mipsMessage{}, fmt.Errorf("truncated MIPS field at byte %d", offset)
		}
		length := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		kind := data[offset+4]
		offset += 5
		if length > len(data)-offset {
			return mipsMessage{}, fmt.Errorf("invalid MIPS field length %d", length)
		}
		field := data[offset : offset+length]
		offset += length
		switch kind {
		case mipsFieldID:
			if len(field) != 4 {
				return mipsMessage{}, errors.New("invalid MIPS ID field")
			}
			message.ID = binary.LittleEndian.Uint32(field)
		case mipsFieldReplyTopic:
			message.ReplyTopic = mipsText(field)
		case mipsFieldPayload:
			message.Payload = mipsText(field)
		case mipsFieldFrom:
			message.From = mipsText(field)
		}
	}
	if message.ID == 0 || message.Payload == "" {
		return mipsMessage{}, errors.New("MIPS envelope is missing ID or payload")
	}
	return message, nil
}

func mipsText(value []byte) string {
	if len(value) > 0 && value[len(value)-1] == 0 {
		value = value[:len(value)-1]
	}
	return string(value)
}
