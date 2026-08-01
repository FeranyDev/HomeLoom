package exec

import "testing"

func TestUsesHardwareAccel(t *testing.T) {
	if !usesHardwareAccel([]string{"ffmpeg", "-hwaccel", "videotoolbox", "-c:v", "h264_videotoolbox"}) {
		t.Fatal("expected videotoolbox args to count as hardware")
	}
	if usesHardwareAccel([]string{"ffmpeg", "-c:v", "libx264"}) {
		t.Fatal("libx264 should not count as hardware")
	}
}

func TestIsHardwareAccelFailure(t *testing.T) {
	for _, line := range []string{
		"hardware accelerator failed to decode picture",
		"vt decoder cb: output image buffer is null: -17694",
		"Error while decoding stream #0:0: Unknown error occurred",
		"Cannot create a videotoolbox compressed session: -12908",
	} {
		if !isHardwareAccelFailure([]byte(line)) {
			t.Fatalf("expected hardware failure for %q", line)
		}
	}
	if isHardwareAccelFailure([]byte("frame=  10 fps=30")) {
		t.Fatal("progress line should not count as hardware failure")
	}
}
