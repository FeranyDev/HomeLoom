package matterwebrtc

import (
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
)

type streamsSource struct{}

func (streamsSource) Attach(streamID string, consumer core.Consumer) (func(), error) {
	stream := streams.Get(streamID)
	if stream == nil {
		return nil, ErrSourceNotFound
	}
	if err := stream.AddConsumer(consumer); err != nil {
		return nil, err
	}
	return func() { stream.RemoveConsumer(consumer) }, nil
}
