// Package xiaomi registers HomeLoom's strictly preauthorized Xiaomi MISS
// source. It deliberately excludes upstream account login and legacy fallback.
package xiaomi

import (
	"errors"
	"net/url"

	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/xiaomi/miss"
)

func Init() {
	streams.HandleFunc("xiaomi", func(rawURL string) (core.Producer, error) {
		if err := validateMISSURL(rawURL); err != nil {
			return nil, err
		}
		return miss.Dial(rawURL)
	})
}

func validateMISSURL(rawURL string) error {
	source, err := url.Parse(rawURL)
	if err != nil || source.Scheme != "xiaomi" || source.Host == "" {
		return errors.New("xiaomi MISS source is invalid")
	}
	query := source.Query()
	if source.User != nil ||
		query.Get("vendor") == "" ||
		query.Get("client_public") == "" ||
		query.Get("client_private") == "" ||
		query.Get("device_public") == "" ||
		query.Get("sign") == "" {
		return errors.New("xiaomi MISS source is not preauthorized")
	}
	return nil
}
