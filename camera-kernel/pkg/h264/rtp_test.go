package h264

import (
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
)

func fragmentedNALU(naluType byte, start, end bool, sequence uint16, payload []byte) *rtp.Packet {
	const fuAType = 28
	indicator := byte(0x60) | fuAType
	header := naluType
	if start {
		header |= 0x80
	}
	if end {
		header |= 0x40
	}
	return &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: sequence, Marker: end},
		Payload: append([]byte{indicator, header}, payload...),
	}
}

func TestRTPDepaySkipsNALUStartedBeforeConsumerAttach(t *testing.T) {
	codec := &core.Codec{Name: core.CodecH264}
	var packets []*rtp.Packet
	depay := RTPDepay(codec, func(packet *rtp.Packet) {
		clone := packet.Clone()
		clone.Payload = append([]byte(nil), packet.Payload...)
		packets = append(packets, clone)
	})

	depay(fragmentedNALU(NALUTypeIFrame, false, false, 100, []byte{0xaa, 0xbb}))
	depay(fragmentedNALU(NALUTypeIFrame, false, true, 101, []byte{0xcc, 0xdd}))
	if len(packets) != 0 {
		t.Fatalf("orphan FU-A tail produced %d packets", len(packets))
	}

	depay(fragmentedNALU(NALUTypeIFrame, true, false, 102, []byte{0x01, 0x02}))
	depay(fragmentedNALU(NALUTypeIFrame, false, true, 103, []byte{0x03, 0x04}))
	if len(packets) != 1 {
		t.Fatalf("complete fragmented NAL produced %d packets", len(packets))
	}
}

func TestRTPDepayDropsAccessUnitAfterSequenceGap(t *testing.T) {
	codec := &core.Codec{Name: core.CodecH264}
	var packets []*rtp.Packet
	depay := RTPDepay(codec, func(packet *rtp.Packet) {
		packets = append(packets, packet.Clone())
	})

	depay(fragmentedNALU(NALUTypeIFrame, true, false, 200, []byte{0x01}))
	// Sequence 201 is missing. The tail of the incomplete IDR must not be
	// emitted, even though it has the marker bit.
	depay(fragmentedNALU(NALUTypeIFrame, false, true, 202, []byte{0x02}))
	if len(packets) != 0 {
		t.Fatalf("incomplete access unit produced %d packets", len(packets))
	}

	depay(fragmentedNALU(NALUTypeIFrame, true, false, 203, []byte{0x03}))
	depay(fragmentedNALU(NALUTypeIFrame, false, true, 204, []byte{0x04}))
	if len(packets) != 1 {
		t.Fatalf("complete access unit after gap produced %d packets", len(packets))
	}
}
