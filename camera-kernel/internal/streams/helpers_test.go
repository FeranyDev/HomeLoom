package streams

import "testing"

func TestDemoteHardwareURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{
			in:   "ffmpeg:cam#video=h264#hardware=videotoolbox#width=1280#starttimeout=8",
			want: "ffmpeg:cam#video=h264#width=1280#starttimeout=8",
		},
		{
			in:   "ffmpeg:cam#video=h264/videotoolbox#audio=opus/16000",
			want: "ffmpeg:cam#video=h264#audio=opus/16000",
		},
		{
			in:   "ffmpeg:cam#video=h264#audio=opus/16000",
			want: "ffmpeg:cam#video=h264#audio=opus/16000",
		},
		{
			in:   "ffmpeg:cam#video=h264/vaapi#bitrate=299K",
			want: "ffmpeg:cam#video=h264#bitrate=299K",
		},
	}
	for _, test := range tests {
		if got := demoteHardwareURL(test.in); got != test.want {
			t.Fatalf("demoteHardwareURL(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestHardwareRetryPolicy(t *testing.T) {
	tests := []struct {
		source string
		retry  int
		limit  int
		demote bool
	}{
		{source: "ffmpeg:cam#video=h264#hardware=vaapi", retry: 0, limit: 0, demote: true},
		{source: "ffmpeg:cam#video=h264#hardware=vaapi#hardware_retry=3", retry: 0, limit: 3, demote: false},
		{source: "ffmpeg:cam#video=h264#hardware=vaapi#hardware_retry=3", retry: 2, limit: 3, demote: false},
		{source: "ffmpeg:cam#video=h264#hardware=vaapi#hardware_retry=3", retry: 3, limit: 3, demote: true},
		{source: "ffmpeg:cam#video=h264#hardware=vaapi#hardware_retry=bad", retry: 0, limit: 0, demote: true},
	}
	for _, test := range tests {
		if got := hardwareRetryLimit(test.source); got != test.limit {
			t.Fatalf("hardwareRetryLimit(%q) = %d, want %d", test.source, got, test.limit)
		}
		if got := shouldDemoteHardware(test.source, test.retry); got != test.demote {
			t.Fatalf("shouldDemoteHardware(%q, %d) = %v, want %v", test.source, test.retry, got, test.demote)
		}
	}
}
