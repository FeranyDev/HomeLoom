package miss

import (
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
)

func TestInitialVideoAccessUnitMustBeKeyframe(t *testing.T) {
	// A P-frame consumed by probe must not be forwarded to the first FFmpeg
	// decoder. Once a complete IDR arrives, subsequent pictures are allowed.
	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x06, 0xe2}
	pframe := h264.JoinNALU([]byte{0x41, 0x01})
	idr := h264.JoinNALU(sps, pps, []byte{0x65, 0x88, 0x80})

	var synced bool
	if acceptInitialVideoAccessUnit(codecH264, pframe, &synced) {
		t.Fatal("P-frame was accepted before the first IDR")
	}
	if !acceptInitialVideoAccessUnit(codecH264, idr, &synced) {
		t.Fatal("IDR was not accepted as the initial video access unit")
	}
	if !acceptInitialVideoAccessUnit(codecH264, pframe, &synced) {
		t.Fatal("P-frame was rejected after initial synchronization")
	}
}

func TestInitialH265VideoAccessUnitMustBeKeyframe(t *testing.T) {
	// HEVC IRAP NAL units 19-21 are the clean restart points for MISS.
	irap := h264.JoinNALU([]byte{0x26, 0x01, 0x80})
	var synced bool
	if !acceptInitialVideoAccessUnit(codecH265, irap, &synced) {
		t.Fatal("HEVC IRAP was not accepted as the initial video access unit")
	}
}

func TestProbePublishesCodecOnlyAfterCompleteParameterSets(t *testing.T) {
	sps := h264.JoinNALU([]byte{0x67, 0x42, 0x00, 0x1f})
	pps := h264.JoinNALU([]byte{0x68, 0xce, 0x06, 0xe2})
	var codec *core.Codec
	var vps, cachedSPS, cachedPPS []byte
	updateProbedVideoCodec(codecH264, sps, &codec, &vps, &cachedSPS, &cachedPPS)
	if codec != nil {
		t.Fatal("probe published H264 codec before PPS arrived")
	}
	updateProbedVideoCodec(codecH264, pps, &codec, &vps, &cachedSPS, &cachedPPS)
	if codec == nil || !strings.Contains(codec.FmtpLine, "sprop-parameter-sets=") {
		t.Fatalf("probe did not publish complete H264 codec: %#v", codec)
	}
}
