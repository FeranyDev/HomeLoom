package aac

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/stretchr/testify/require"
)

func TestConfigToCodec(t *testing.T) {
	s := "profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3;config=F8EC3000"
	s = core.Between(s, "config=", ";")
	src, err := hex.DecodeString(s)
	require.Nil(t, err)

	codec := ConfigToCodec(src)
	require.Equal(t, core.CodecAAC, codec.Name)
	require.Equal(t, uint32(24000), codec.ClockRate)
	require.Equal(t, uint8(1), codec.Channels)

	dst := EncodeConfig(TypeAACELD, 24000, 1, true)
	require.Equal(t, src, dst)
}

func TestADTS(t *testing.T) {
	// FFmpeg MPEG-TS AAC (one packet)
	s := "fff15080021ffc210049900219002380fff15080021ffc212049900219002380" //...
	src, err := hex.DecodeString(s)
	require.Nil(t, err)

	codec := ADTSToCodec(src)
	require.Equal(t, uint32(44100), codec.ClockRate)
	require.Equal(t, uint8(2), codec.Channels)

	size := ReadADTSSize(src)
	require.Equal(t, uint16(16), size)

	dst := CodecToADTS(codec)
	WriteADTSSize(dst, size)

	require.Equal(t, src[:len(dst)], dst)
}

func TestEncodeConfig(t *testing.T) {
	conf := EncodeConfig(TypeAACLC, 48000, 1, false)
	require.Equal(t, "1188", hex.EncodeToString(conf))
	conf = EncodeConfig(TypeAACLC, 16000, 1, false)
	require.Equal(t, "1408", hex.EncodeToString(conf))
	conf = EncodeConfig(TypeAACLC, 8000, 1, false)
	require.Equal(t, "1588", hex.EncodeToString(conf))
}

func TestRejectsInvalidADTSFrameLength(t *testing.T) {
	for _, frameSize := range []uint16{0, ADTSHeaderSize - 1} {
		header := []byte{0xff, 0xf1, 0x50, 0x80, 0x00, 0x1c, 0xfc}
		header[3] = (header[3] & 0xfc) | byte(frameSize>>11)
		header[4] = byte(frameSize >> 3)
		header[5] = (header[5] & 0x1f) | byte(frameSize<<5)
		input := append(header, 0)

		_, err := adtsFrameSize(input)
		require.ErrorIs(t, err, errInvalidADTSFrame)
		require.Zero(t, ADTSTimeSize(input))
		require.Nil(t, ADTStoRTP(input))

		producer, err := Open(bytes.NewReader(input))
		require.NoError(t, err)
		require.ErrorIs(t, producer.Start(), errInvalidADTSFrame)
	}
}
