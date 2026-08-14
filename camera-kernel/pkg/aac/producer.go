package aac

import (
	"bufio"
	"errors"
	"io"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
)

type Producer struct {
	core.Connection
	rd *bufio.Reader
}

func Open(r io.Reader) (*Producer, error) {
	rd := bufio.NewReader(r)

	b, err := rd.Peek(ADTSHeaderSize)
	if err != nil {
		return nil, err
	}

	codec := ADTSToCodec(b)
	if codec == nil {
		return nil, errors.New("adts: wrong header")
	}
	codec.PayloadType = core.PayloadTypeRAW

	medias := []*core.Media{
		{
			Kind:      core.KindAudio,
			Direction: core.DirectionRecvonly,
			Codecs:    []*core.Codec{codec},
		},
	}
	return &Producer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "adts",
			Medias:     medias,
			Transport:  r,
		},
		rd: rd,
	}, nil
}

func (c *Producer) Start() error {
	for {
		// read ADTS header
		adts := make([]byte, ADTSHeaderSize)
		if _, err := io.ReadFull(c.rd, adts); err != nil {
			return err
		}

		frameSize, err := adtsFrameSize(adts)
		if err != nil {
			return err
		}
		payloadSize := frameSize - ADTSHeaderSize

		if HasCRC(adts) {
			// skip CRC after header
			if _, err := c.rd.Discard(2); err != nil {
				return err
			}
			payloadSize -= 2
		}

		// read AAC payload after header
		payload := make([]byte, payloadSize)
		if _, err := io.ReadFull(c.rd, payload); err != nil {
			return err
		}

		c.Recv += payloadSize

		if len(c.Receivers) == 0 {
			continue
		}

		pkt := &rtp.Packet{
			Header:  rtp.Header{Timestamp: core.Now90000()},
			Payload: payload,
		}
		c.Receivers[0].WriteRTP(pkt)
	}
}
