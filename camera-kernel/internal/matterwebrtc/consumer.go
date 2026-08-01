package matterwebrtc

import (
	"sync"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type consumer struct {
	core.Connection
	tracks   map[string]*webrtc.TrackLocalStaticRTP
	keyframe chan struct{}
	keyOnce  sync.Once
}

func newConsumer(tracks map[string]*webrtc.TrackLocalStaticRTP) *consumer {
	medias := make([]*core.Media, 0, len(tracks))
	if tracks[core.KindVideo] != nil {
		medias = append(medias, &core.Media{
			Kind: core.KindVideo, Direction: core.DirectionSendonly,
			Codecs: []*core.Codec{{
				Name: core.CodecH264, ClockRate: 90000, PayloadType: 96,
				FmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			}},
		})
	}
	if tracks[core.KindAudio] != nil {
		medias = append(medias, &core.Media{
			Kind: core.KindAudio, Direction: core.DirectionSendonly,
			Codecs: []*core.Codec{{
				Name: core.CodecOpus, ClockRate: 48000, Channels: 2, PayloadType: 111,
				FmtpLine: "minptime=10;useinbandfec=1",
			}},
		})
	}
	return &consumer{
		Connection: core.Connection{
			ID: core.NewID(), FormatName: "matter-webrtc", Protocol: "webrtc", Medias: medias,
		},
		tracks: tracks, keyframe: make(chan struct{}),
	}
}

func (c *consumer) AddTrack(media *core.Media, codec *core.Codec, receiver *core.Receiver) error {
	local := c.tracks[media.Kind]
	sender := core.NewSender(media, codec)
	if media.Kind == core.KindVideo {
		detect := h264.RTPDepay(codec, func(packet *rtp.Packet) {
			if h264.IsKeyframe(packet.Payload) {
				c.keyOnce.Do(func() { close(c.keyframe) })
			}
		})
		sender.Handler = func(packet *rtp.Packet) {
			detect(packet.Clone())
			_ = local.WriteRTP(packet)
		}
	} else {
		sender.Handler = func(packet *rtp.Packet) {
			_ = local.WriteRTP(packet)
		}
	}
	sender.HandleRTP(receiver)
	c.Senders = append(c.Senders, sender)
	return nil
}
