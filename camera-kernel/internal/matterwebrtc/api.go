package matterwebrtc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/pion/webrtc/v4"
)

const (
	apiPath           = "api/matter/webrtc"
	maxRequestBytes   = 1 << 20
	maxSDPBytes       = 512 << 10
	maxCandidateBytes = 4096
	maxSessionIDBytes = 128
	maxStreamIDBytes  = 256
)

type request struct {
	Operation string                   `json:"operation"`
	SessionID string                   `json:"sessionId,omitempty"`
	StreamID  string                   `json:"streamId,omitempty"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
}

type response struct {
	SessionID string `json:"sessionId,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Closed    bool   `json:"closed,omitempty"`
}

func Init() {
	service, err := NewService(streamsSource{}, Options{})
	if err != nil {
		panic(err)
	}
	api.HandleFunc(apiPath, service.Handler())
}

func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
			http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		var input request
		decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF || !validRequest(input) {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		var (
			output response
			err    error
		)
		switch input.Operation {
		case "open":
			output.SessionID, output.SDP, err = s.Open(r.Context(), input.StreamID, input.SDP)
		case "reoffer":
			output.SDP, err = s.Reoffer(r.Context(), input.SessionID, input.SDP)
			output.SessionID = input.SessionID
		case "addIce":
			err = s.AddICECandidate(input.SessionID, *input.Candidate)
			output.SessionID = input.SessionID
		case "close":
			err = s.Close(input.SessionID)
			output.Closed = err == nil
		default:
			err = errors.New("unsupported Matter WebRTC operation")
		}
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, ErrSessionActive):
				status = http.StatusConflict
			case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrSourceNotFound):
				status = http.StatusNotFound
			case errors.Is(err, ErrUnsupportedOffer):
				status = http.StatusBadRequest
			case errors.Is(err, ErrKeyframeTimeout), errors.Is(err, errGatheringTimedOut):
				status = http.StatusGatewayTimeout
			default:
				status = http.StatusBadGateway
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", api.MimeJSON)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(output)
	}
}

func validRequest(input request) bool {
	if len(input.SessionID) > maxSessionIDBytes || len(input.StreamID) > maxStreamIDBytes ||
		len(input.SDP) > maxSDPBytes {
		return false
	}
	switch input.Operation {
	case "open":
		return input.SessionID == "" && input.StreamID != "" && input.SDP != "" && input.Candidate == nil
	case "reoffer":
		return input.SessionID != "" && input.StreamID == "" && input.SDP != "" && input.Candidate == nil
	case "addIce":
		return input.SessionID != "" && input.StreamID == "" && input.SDP == "" &&
			input.Candidate != nil && validCandidate(*input.Candidate)
	case "close":
		return input.SessionID != "" && input.StreamID == "" && input.SDP == "" && input.Candidate == nil
	default:
		return false
	}
}

func validCandidate(candidate webrtc.ICECandidateInit) bool {
	if candidate.Candidate == "" || len(candidate.Candidate) > maxCandidateBytes {
		return false
	}
	if !strings.Contains(" "+strings.ToLower(candidate.Candidate)+" ", " typ host ") {
		return false
	}
	if candidate.SDPMid != nil && len(*candidate.SDPMid) > maxSessionIDBytes {
		return false
	}
	if candidate.UsernameFragment != nil && len(*candidate.UsernameFragment) > maxSessionIDBytes {
		return false
	}
	return true
}
