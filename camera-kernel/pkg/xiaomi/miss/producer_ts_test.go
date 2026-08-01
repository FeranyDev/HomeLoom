package miss

import "testing"

func TestVideoTSNormalizerMonotonicAcrossJump(t *testing.T) {
	var n videoTSNormalizer
	t0 := n.Normalize(1_000, 90000)
	t1 := n.Normalize(1_050, 90000) // +50ms → +4500 @ 90kHz
	if t1 <= t0 {
		t.Fatalf("expected increasing timestamps: %d -> %d", t0, t1)
	}
	if delta := int64(t1) - int64(t0); delta < 4000 || delta > 5000 {
		t.Fatalf("50ms delta = %d RTP ticks, want ~4500", delta)
	}
	// Large jump (reconnect / wall-clock rewrite) must not create a multi-second gap.
	t2 := n.Normalize(60_000, 90000)
	if t2 <= t1 {
		t.Fatalf("after jump timestamps not increasing: %d -> %d", t1, t2)
	}
	if delta := int64(t2) - int64(t1); delta > 90000 { // >1s
		t.Fatalf("discontinuity produced large gap %d ticks", delta)
	}
}

func TestVideoTSNormalizerBackwardJump(t *testing.T) {
	var n videoTSNormalizer
	_ = n.Normalize(5_000, 90000)
	t1 := n.Normalize(5_050, 90000)
	t2 := n.Normalize(100, 90000) // backward
	if t2 <= t1 {
		t.Fatalf("backward jump should continue forward: %d -> %d", t1, t2)
	}
}
