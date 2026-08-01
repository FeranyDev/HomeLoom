package streams

import (
	"errors"
	"strings"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"go.uber.org/zap"
)

func (s *Stream) AddConsumer(cons core.Consumer) (err error) {
	// support for multiple simultaneous pending from different consumers
	consN := s.pending.Add(1) - 1

	var prodErrors = make([]error, len(s.producers))
	var prodMedias []*core.Media
	var prodStarts []*Producer

	// Step 1. Get consumer medias
	consMedias := cons.GetMedias()
	for _, consMedia := range consMedias {
		log.Debug("checking consumer media", zap.Int32("consumer", consN), zap.Stringer("media", consMedia))

	producers:
		for prodN, prod := range s.producers {
			// check for loop request, ex. `camera1: ffmpeg:camera1`
			if info, ok := cons.(core.Info); ok && prod.url == info.GetSource() {
				log.Debug("skipping producer loop", zap.Int32("consumer", consN), zap.Int("producer", prodN))
				continue
			}

			if prodErrors[prodN] != nil {
				log.Debug("skipping failed producer", zap.Int32("consumer", consN), zap.Int("producer", prodN))
				continue
			}

			if err = prod.Dial(); err != nil {
				log.Debug("producer dial failed", zap.Error(err), zap.Int32("consumer", consN), zap.Int("producer", prodN))
				prodErrors[prodN] = err
				continue
			}

			// Step 2. Get producer medias (not tracks yet)
			for _, prodMedia := range prod.GetMedias() {
				log.Debug("checking producer media", zap.Int32("consumer", consN), zap.Int("producer", prodN), zap.Stringer("media", prodMedia))
				prodMedias = append(prodMedias, prodMedia)

				// Step 3. Match consumer/producer codecs list
				prodCodec, consCodec := prodMedia.MatchMedia(consMedia)
				if prodCodec == nil {
					continue
				}

				var track *core.Receiver

				switch prodMedia.Direction {
				case core.DirectionRecvonly:
					log.Debug("matched producer track", zap.Int32("consumer", consN), zap.Int("producer", prodN))

					// Step 4. Get recvonly track from producer
					if track, err = prod.GetTrack(prodMedia, prodCodec); err != nil {
						log.Info("cannot get track", zap.Error(err), zap.Int32("consumer", consN), zap.Int("producer", prodN))
						prodErrors[prodN] = err
						continue
					}
					// Step 5. Add track to consumer
					if err = cons.AddTrack(consMedia, consCodec, track); err != nil {
						log.Info("cannot add track", zap.Error(err), zap.Int32("consumer", consN), zap.Int("producer", prodN))
						continue
					}

				case core.DirectionSendonly:
					log.Debug("matched backchannel track", zap.Int32("consumer", consN), zap.Int("producer", prodN))

					// Step 4. Get recvonly track from consumer (backchannel)
					if track, err = cons.(core.Producer).GetTrack(consMedia, consCodec); err != nil {
						log.Info("cannot get backchannel track", zap.Error(err), zap.Int32("consumer", consN), zap.Int("producer", prodN))
						continue
					}
					// Step 5. Add track to producer
					if err = prod.AddTrack(prodMedia, prodCodec, track); err != nil {
						log.Info("cannot add backchannel track", zap.Error(err), zap.Int32("consumer", consN), zap.Int("producer", prodN))
						prodErrors[prodN] = err
						continue
					}
				}

				prodStarts = append(prodStarts, prod)

				if !consMedia.MatchAll() {
					break producers
				}
			}
		}
	}

	// stop producers if they don't have readers
	if s.pending.Add(-1) == 0 {
		s.stopProducers()
	}

	if len(prodStarts) == 0 {
		return formatError(consMedias, prodMedias, prodErrors)
	}

	s.mu.Lock()
	s.consumers = append(s.consumers, cons)
	s.mu.Unlock()

	// there may be duplicates, but that's not a problem
	for _, prod := range prodStarts {
		prod.start()
	}

	return nil
}

func formatError(consMedias, prodMedias []*core.Media, prodErrors []error) error {
	// 1. Return errors if any not nil
	var text string

	for _, err := range prodErrors {
		if err != nil {
			text = appendString(text, err.Error())
		}
	}

	if len(text) != 0 {
		return errors.New("streams: " + text)
	}

	// 2. Return "codecs not matched"
	if prodMedias != nil {
		var prod, cons string

		for _, media := range prodMedias {
			if media.Direction == core.DirectionRecvonly {
				for _, codec := range media.Codecs {
					prod = appendString(prod, media.Kind+":"+codec.PrintName())
				}
			}
		}

		for _, media := range consMedias {
			if media.Direction == core.DirectionSendonly {
				for _, codec := range media.Codecs {
					cons = appendString(cons, media.Kind+":"+codec.PrintName())
				}
			}
		}

		return errors.New("streams: codecs not matched: " + prod + " => " + cons)
	}

	// 3. Return unknown error
	return errors.New("streams: unknown error")
}

func appendString(s, elem string) string {
	if strings.Contains(s, elem) {
		return s
	}
	if len(s) == 0 {
		return elem
	}
	return s + ", " + elem
}
