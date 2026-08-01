package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/magic"
)

const (
	defaultSnapshotWidth  = 1280
	defaultSnapshotHeight = 720
	maxSnapshotDimension  = 4096
	maxSnapshotBytes      = 8 << 20
	snapshotTimeout       = 10 * time.Second
)

var (
	errSnapshotTooLarge   = errors.New("snapshot exceeds size limit")
	errSnapshotTimeout    = errors.New("snapshot keyframe timeout")
	errSnapshotNotFound   = errors.New("snapshot stream not found")
	snapshotTranscodeSlot = make(chan struct{}, 1)
)

type snapshotCaptureFunc func(context.Context, string) ([]byte, string, error)
type snapshotEncodeFunc func(context.Context, []byte, int, int) ([]byte, error)

func snapshotHandler(capture snapshotCaptureFunc, encode snapshotEncodeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		width, height, err := snapshotDimensions(r)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		streamID := r.URL.Query().Get("src")
		if streamID == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
		defer cancel()
		payload, codec, err := capture(ctx, streamID)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, errSnapshotNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, errSnapshotTimeout) || errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			http.Error(w, http.StatusText(status), status)
			return
		}

		switch codec {
		case core.CodecJPEG, core.CodecRAW:
		default:
			select {
			case snapshotTranscodeSlot <- struct{}{}:
				defer func() { <-snapshotTranscodeSlot }()
			default:
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			payload, err = encode(ctx, payload, width, height)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
				return
			}
		}
		if !validJPEG(payload) || len(payload) > maxSnapshotBytes {
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}

		header := w.Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Content-Length", strconv.Itoa(len(payload)))
		header.Set("Content-Type", "image/jpeg")
		header.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}
}

func snapshotDimensions(r *http.Request) (int, int, error) {
	query := r.URL.Query()
	width, err := positiveDimension(query.Get("width"), defaultSnapshotWidth)
	if err != nil {
		return 0, 0, err
	}
	height, err := positiveDimension(query.Get("height"), defaultSnapshotHeight)
	if err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func positiveDimension(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > maxSnapshotDimension {
		return 0, errors.New("invalid snapshot dimension")
	}
	return number, nil
}

func captureSnapshotKeyframe(ctx context.Context, streamID string) ([]byte, string, error) {
	stream := streams.Get(streamID)
	if stream == nil {
		return nil, "", errSnapshotNotFound
	}
	consumer := magic.NewKeyframe()
	if err := stream.AddConsumer(consumer); err != nil {
		return nil, "", err
	}
	defer stream.RemoveConsumer(consumer)

	writer := &boundedSnapshotBuffer{remaining: maxSnapshotBytes}
	done := make(chan error, 1)
	go func() {
		_, err := consumer.WriteTo(writer)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, "", err
		}
		return writer.Bytes(), consumer.CodecName(), nil
	case <-ctx.Done():
		_ = consumer.Stop()
		<-done
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, "", errSnapshotTimeout
		}
		return nil, "", ctx.Err()
	}
}

type boundedSnapshotBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *boundedSnapshotBuffer) Write(payload []byte) (int, error) {
	if len(payload) > b.remaining {
		return 0, errSnapshotTooLarge
	}
	n, err := b.Buffer.Write(payload)
	b.remaining -= n
	return n, err
}

func validJPEG(payload []byte) bool {
	if len(payload) < 4 ||
		payload[0] != 0xff || payload[1] != 0xd8 ||
		payload[len(payload)-2] != 0xff || payload[len(payload)-1] != 0xd9 {
		return false
	}
	_, err := jpeg.DecodeConfig(bytes.NewReader(payload))
	return err == nil
}

var _ io.Writer = (*boundedSnapshotBuffer)(nil)
