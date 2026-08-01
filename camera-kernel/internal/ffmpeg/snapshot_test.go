package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

var testJPEG = makeTestJPEG()

func TestSnapshotHandlerReturnsBoundedJPEG(t *testing.T) {
	var capturedStream string
	handler := snapshotHandler(
		func(_ context.Context, streamID string) ([]byte, string, error) {
			capturedStream = streamID
			return testJPEG, core.CodecJPEG, nil
		},
		func(context.Context, []byte, int, int) ([]byte, error) {
			t.Fatal("JPEG input must not be transcoded")
			return nil, nil
		},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=camera-1&width=640&height=360", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || capturedStream != "camera-1" {
		t.Fatalf("status=%d stream=%q", response.Code, capturedStream)
	}
	if response.Header().Get("Content-Type") != "image/jpeg" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected headers: %#v", response.Header())
	}
	if string(response.Body.Bytes()) != string(testJPEG) {
		t.Fatalf("payload = %x", response.Body.Bytes())
	}
}

func TestSnapshotHandlerTranscodesVideoAtRequestedSize(t *testing.T) {
	var width, height int
	handler := snapshotHandler(
		func(context.Context, string) ([]byte, string, error) {
			return []byte("annex-b"), core.CodecH264, nil
		},
		func(_ context.Context, _ []byte, requestedWidth, requestedHeight int) ([]byte, error) {
			width, height = requestedWidth, requestedHeight
			return testJPEG, nil
		},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=camera-1&width=1920&height=1080", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || width != 1920 || height != 1080 {
		t.Fatalf("status=%d dimensions=%dx%d", response.Code, width, height)
	}
}

func TestSnapshotHandlerRejectsInvalidRequestsAndHidesCaptureErrors(t *testing.T) {
	capture := func(context.Context, string) ([]byte, string, error) {
		return nil, "", errors.New("rtsp://user:secret@camera/private")
	}
	handler := snapshotHandler(capture, JPEGWithScaleContext)

	for _, rawURL := range []string{
		"/api/frame.jpeg",
		"/api/frame.jpeg?src=camera-1&width=0",
		"/api/frame.jpeg?src=camera-1&height=4097",
		"/api/frame.jpeg?src=camera-1&width=abc",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", rawURL, response.Code)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=camera-1", nil))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}

	notFound := snapshotHandler(
		func(context.Context, string) ([]byte, string, error) {
			return nil, "", errSnapshotNotFound
		},
		JPEGWithScaleContext,
	)
	response = httptest.NewRecorder()
	notFound.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d", response.Code)
	}
}

func TestSnapshotHandlerRejectsNonJPEGEncoderOutput(t *testing.T) {
	handler := snapshotHandler(
		func(context.Context, string) ([]byte, string, error) {
			return []byte("annex-b"), core.CodecH264, nil
		},
		func(context.Context, []byte, int, int) ([]byte, error) {
			return []byte("not-jpeg"), nil
		},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=camera-1", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestBoundedSnapshotBufferRejectsOversizeWrite(t *testing.T) {
	buffer := &boundedSnapshotBuffer{remaining: 3}
	if _, err := buffer.Write([]byte("four")); !errors.Is(err, errSnapshotTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestSnapshotHandlerLimitsConcurrentTranscodes(t *testing.T) {
	snapshotTranscodeSlot <- struct{}{}
	defer func() { <-snapshotTranscodeSlot }()
	handler := snapshotHandler(
		func(context.Context, string) ([]byte, string, error) {
			return []byte("annex-b"), core.CodecH264, nil
		},
		func(context.Context, []byte, int, int) ([]byte, error) {
			t.Fatal("busy snapshot encoder must not start")
			return nil, nil
		},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/frame.jpeg?src=camera-1", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Code)
	}
}

func makeTestJPEG() []byte {
	var output bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.Black)
	if err := jpeg.Encode(&output, pixel, nil); err != nil {
		panic(err)
	}
	return output.Bytes()
}
