package mp4

import (
	"context"
	"io"
	"sync"
	"testing"
)

type blockingKeyframeSession struct {
	stopped chan struct{}
	once    sync.Once
}

func (s *blockingKeyframeSession) WriteTo(io.Writer) (int64, error) {
	<-s.stopped
	return 0, nil
}

func (s *blockingKeyframeSession) Stop() error {
	s.once.Do(func() { close(s.stopped) })
	return nil
}

func TestCaptureKeyframeStopsBlockedSessionWhenRequestIsCancelled(t *testing.T) {
	session := &blockingKeyframeSession{stopped: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if captureKeyframe(ctx, session, io.Discard) {
		t.Fatal("cancelled keyframe capture reported success")
	}
	select {
	case <-session.stopped:
	default:
		t.Fatal("cancelled keyframe capture did not stop the consumer")
	}
}

type immediateKeyframeSession struct{}

func (*immediateKeyframeSession) WriteTo(io.Writer) (int64, error) { return 0, nil }

func (*immediateKeyframeSession) Stop() error { return nil }

func TestCaptureKeyframeReturnsAfterSessionCompletes(t *testing.T) {
	if !captureKeyframe(context.Background(), &immediateKeyframeSession{}, io.Discard) {
		t.Fatal("completed keyframe capture reported cancellation")
	}
}
