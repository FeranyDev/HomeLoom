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
