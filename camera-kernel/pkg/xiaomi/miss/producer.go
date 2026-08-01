package miss

import (
	"encoding/binary"
	"fmt"
	"net/url"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/h264/annexb"
	"github.com/AlexxIT/go2rtc/pkg/h265"
	"github.com/pion/rtp"
)

type Producer struct {
	core.Connection
	client *Client
}

func Dial(rawURL string) (core.Producer, error) {
	client, err := NewClient(rawURL)
	if err != nil {
		return nil, err
	}

	u, _ := url.Parse(rawURL)
	query := u.Query()

	err = client.StartMedia(query.Get("channel"), query.Get("subtype"), query.Get("audio"))
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	medias, err := probe(client, query.Get("audio") != "0")
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	return &Producer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "xiaomi/miss",
			Protocol:   client.Protocol(),
			RemoteAddr: client.RemoteAddr().String(),
			UserAgent:  client.Version(),
			Medias:     medias,
			Transport:  client,
		},
		client: client,
	}, nil
}

func probe(client *Client, audio bool) ([]*core.Media, error) {
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))

	var vcodec, acodec *core.Codec
	var videoVPS, videoSPS, videoPPS []byte

	for {
		pkt, err := client.ReadPacket()
		if err != nil {
			if vcodec != nil {
				err = fmt.Errorf("no audio")
			} else if acodec != nil {
				err = fmt.Errorf("no video")
			}
			return nil, fmt.Errorf("xiaomi: probe: %w", err)
		}

		switch pkt.CodecID {
		case codecH264:
			buf := annexb.EncodeToAVCC(pkt.Payload)
			updateProbedVideoCodec(pkt.CodecID, buf, &vcodec, &videoVPS, &videoSPS, &videoPPS)
		case codecH265:
			buf := annexb.EncodeToAVCC(pkt.Payload)
			updateProbedVideoCodec(pkt.CodecID, buf, &vcodec, &videoVPS, &videoSPS, &videoPPS)
		case codecPCMA:
			if acodec == nil {
				acodec = &core.Codec{Name: core.CodecPCMA, ClockRate: 8000}
			}
		case codecOPUS:
			if acodec == nil {
				acodec = &core.Codec{Name: core.CodecOpus, ClockRate: 48000, Channels: 2}
			}
		}

		if vcodec != nil && (acodec != nil || !audio) {
			break
		}
	}

	_ = client.SetDeadline(time.Time{})

	medias := []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionRecvonly,
			Codecs:    []*core.Codec{vcodec},
		},
	}

	if acodec != nil {
		medias = append(medias, &core.Media{
			Kind:      core.KindAudio,
			Direction: core.DirectionRecvonly,
			Codecs:    []*core.Codec{acodec},
		})

		medias = append(medias, &core.Media{
			Kind:      core.KindAudio,
			Direction: core.DirectionSendonly,
			Codecs:    []*core.Codec{acodec.Clone()},
		})
	}

	return medias, nil
}

const timestamp40ms = 48000 * 0.040

// videoTSNormalizer keeps RTP video timestamps monotonic across MISS
// reconnects, wall-clock rewrites, and large source jumps so FFmpeg does not
// invent thousands of duplicated frames to fill a fake time gap.
type videoTSNormalizer struct {
	initialized   bool
	sourceBase    int64
	outputBase    int64
	lastSource    int64
	lastOutput    int64
	expectedDelta int64
}

func (n *videoTSNormalizer) Normalize(sourceMS int64, clockRate uint64) uint32 {
	const expectedFPS = 20
	if n.expectedDelta == 0 {
		n.expectedDelta = int64(clockRate) / expectedFPS
	}
	if !n.initialized {
		n.initialized = true
		n.sourceBase = sourceMS
		n.outputBase = 0
		n.lastSource = sourceMS
		n.lastOutput = 0
		return 0
	}
	srcDelta := sourceMS - n.lastSource
	// Treat large backward/forward jumps as discontinuity (reconnect, wrap, or
	// camera wall-clock rewrite) and continue from the last output timestamp.
	if srcDelta < -1000 || srcDelta > 2000 {
		n.sourceBase = sourceMS
		n.outputBase = n.lastOutput + n.expectedDelta
		n.lastSource = sourceMS
		n.lastOutput = n.outputBase
		return uint32(n.lastOutput)
	}
	if srcDelta < 0 {
		srcDelta = 0
	}
	out := n.lastOutput + srcDelta*int64(clockRate)/1000
	if out <= n.lastOutput {
		out = n.lastOutput + n.expectedDelta
	}
	n.lastSource = sourceMS
	n.lastOutput = out
	return uint32(out)
}

func (p *Producer) Start() error {
	var audioTS uint32
	var videoTS videoTSNormalizer
	var vps, sps, pps []byte
	// probe() may have consumed the only parameter-set packets before Start
	// begins. Seed the cache from the codec's SDP so the first forwarded IDR
	// still carries VPS/SPS/PPS even when the camera does not repeat them.
	for _, media := range p.Medias {
		if media == nil || media.Kind != core.KindVideo || len(media.Codecs) == 0 || media.Codecs[0] == nil {
			continue
		}
		switch media.Codecs[0].Name {
		case core.CodecH264:
			sps, pps = h264.GetParameterSet(media.Codecs[0].FmtpLine)
		case core.CodecH265:
			vps, sps, pps = h265.GetParameterSet(media.Codecs[0].FmtpLine)
		}
		break
	}
	// probe() intentionally consumes packets while discovering the codec. The
	// next packet is therefore not guaranteed to be the beginning of a GOP.
	// Do not forward P/B pictures from that partial GOP: FFmpeg would conceal
	// the missing references and encode the gray/mosaic frame that Apple Home
	// displays when a live view is opened. Start each MISS connection at the
	// first complete IDR access unit instead.
	videoSynced := false

	for {
		_ = p.client.SetDeadline(time.Now().Add(10 * time.Second))
		pkt, err := p.client.ReadPacket()
		if err != nil {
			return err
		}

		p.Recv += len(pkt.Payload)

		var name string
		var pkt2 *core.Packet

		switch pkt.CodecID {
		case codecH264, codecH265:
			payload := annexb.EncodeToAVCC(pkt.Payload)
			if pkt.CodecID == codecH264 {
				name = core.CodecH264
				cacheH264ParameterSets(payload, &sps, &pps)
				if !acceptInitialVideoAccessUnit(pkt.CodecID, payload, &videoSynced) {
					continue
				}
				if h264.IsKeyframe(payload) && (len(sps) > 0 || len(pps) > 0) {
					payload = h264.Join(h264.JoinNALU(sps, pps), payload)
				}
			} else {
				name = core.CodecH265
				cacheH265ParameterSets(payload, &vps, &sps, &pps)
				if !acceptInitialVideoAccessUnit(pkt.CodecID, payload, &videoSynced) {
					continue
				}
				if h265.IsKeyframe(payload) && (len(vps) > 0 || len(sps) > 0 || len(pps) > 0) {
					payload = h264.Join(h264.JoinNALU(vps, sps, pps), payload)
				}
			}
			pkt2 = &core.Packet{
				Header: rtp.Header{
					SequenceNumber: uint16(pkt.Sequence),
					Timestamp:      videoTS.Normalize(int64(pkt.Timestamp), 90000),
				},
				Payload: payload,
			}
		case codecPCMA:
			name = core.CodecPCMA
			pkt2 = &core.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         true,
					SequenceNumber: uint16(pkt.Sequence),
					Timestamp:      audioTS,
				},
				Payload: pkt.Payload,
			}
			audioTS += uint32(len(pkt.Payload))
		case codecOPUS:
			name = core.CodecOpus
			pkt2 = &core.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         true,
					SequenceNumber: uint16(pkt.Sequence),
					Timestamp:      audioTS,
				},
				Payload: pkt.Payload,
			}
			// known cameras sends packets with 40ms long
			audioTS += timestamp40ms
		}

		for _, recv := range p.Receivers {
			if recv.Codec.Name == name {
				recv.WriteRTP(pkt2)
				break
			}
		}
	}
}

func (p *Producer) Stop() error {
	_ = p.client.StopMedia()
	return p.Connection.Stop()
}

// TimeToRTP convert time in milliseconds to RTP time
func TimeToRTP(timeMS, clockRate uint64) uint32 {
	return uint32(timeMS * clockRate / 1000)
}

func updateProbedVideoCodec(codecID uint32, payload []byte, codec **core.Codec, vps, sps, pps *[]byte) {
	if codec == nil || *codec != nil {
		return
	}
	switch codecID {
	case codecH264:
		cacheH264ParameterSets(payload, sps, pps)
		// Do not publish an SDP with only SPS. HomeKit/FFmpeg can join
		// between parameter-set packets and then decode the first IDR with
		// the wrong PPS, producing the gray startup frame.
		if len(*sps) > 0 && len(*pps) > 0 {
			*codec = h264.AVCCToCodec(h264.JoinNALU(*sps, *pps))
		}
	case codecH265:
		cacheH265ParameterSets(payload, vps, sps, pps)
		if len(*vps) > 0 && len(*sps) > 0 && len(*pps) > 0 {
			*codec = h265.AVCCToCodec(h264.JoinNALU(*vps, *sps, *pps))
		}
	}
}

func acceptInitialVideoAccessUnit(codecID uint32, payload []byte, synced *bool) bool {
	if synced == nil || *synced {
		return true
	}
	if len(payload) < 5 {
		return false
	}
	var keyframe bool
	switch codecID {
	case codecH264:
		keyframe = h264.IsKeyframe(payload)
	case codecH265:
		keyframe = h265.IsKeyframe(payload)
	default:
		return false
	}
	if !keyframe {
		return false
	}
	*synced = true
	return true
}

func cacheH264ParameterSets(avcc []byte, sps, pps *[]byte) {
	for len(avcc) >= 4 {
		size := 4 + int(binary.BigEndian.Uint32(avcc))
		if size > len(avcc) {
			return
		}
		nalu := avcc[4:size]
		switch nalu[0] & 0x1F {
		case h264.NALUTypeSPS:
			*sps = append([]byte(nil), nalu...)
		case h264.NALUTypePPS:
			*pps = append([]byte(nil), nalu...)
		}
		avcc = avcc[size:]
	}
}

func cacheH265ParameterSets(avcc []byte, vps, sps, pps *[]byte) {
	for len(avcc) >= 4 {
		size := 4 + int(binary.BigEndian.Uint32(avcc))
		if size > len(avcc) {
			return
		}
		nalu := avcc[4:size]
		switch h265.NALUType(avcc) {
		case h265.NALUTypeVPS:
			*vps = append([]byte(nil), nalu...)
		case h265.NALUTypeSPS:
			*sps = append([]byte(nil), nalu...)
		case h265.NALUTypePPS:
			*pps = append([]byte(nil), nalu...)
		}
		avcc = avcc[size:]
	}
}
