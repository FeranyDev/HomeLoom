package h264

import "testing"

func TestPayloaderSTAPUsesMaxNRI(t *testing.T) {
	p := &Payloader{IsAVC: true}
	// SPS (0x67 NRI=3) + PPS (0x68 NRI=3) + IDR (0x65)
	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}
	idr := []byte{0x65, 0x88, 0x80}
	avcc := JoinNALU(sps, pps, idr)
	payloads := p.Payload(1200, avcc)
	if len(payloads) < 2 {
		t.Fatalf("expected STAP-A then IDR, got %d payloads", len(payloads))
	}
	stap := payloads[0]
	if stap[0]&0x1f != 24 {
		t.Fatalf("first payload type = %d, want STAP-A 24", stap[0]&0x1f)
	}
	if nri := stap[0] & 0x60; nri != 0x60 {
		t.Fatalf("STAP-A NRI = 0x%02x, want 0x60 (max of SPS/PPS)", nri)
	}
}
