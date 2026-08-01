package homekit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexxIT/go2rtc/internal/app"
)

const pairingsFilename = "homekit-pairings.json"

type pairingsDocument struct {
	SchemaVersion int      `json:"schemaVersion"`
	StreamID      string   `json:"streamId"`
	Pairings      []string `json:"pairings"`
}

// pairingsStorePath keeps HomeKit controller pairings next to the publisher
// config, outside the YAML that Core's embedded media runtime rewrites on restart.
func pairingsStorePath(streamID string) string {
	if app.ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(app.ConfigPath), pairingsFilename)
}

func loadDurablePairings(streamID string, seed []string) []string {
	path := pairingsStorePath(streamID)
	if path == "" {
		return append([]string(nil), seed...)
	}
	doc, err := readPairingsDocument(path)
	if err == nil && doc.SchemaVersion == 1 {
		if doc.StreamID == "" || doc.StreamID == streamID {
			if len(doc.Pairings) > 0 {
				return append([]string(nil), doc.Pairings...)
			}
			// Empty durable file is authoritative only when YAML also has none.
			if len(seed) == 0 {
				return nil
			}
		}
	}
	if len(seed) == 0 {
		return nil
	}
	// Migrate YAML pairings into the durable store once.
	_ = writePairingsDocument(path, streamID, seed)
	return append([]string(nil), seed...)
}

func persistDurablePairings(streamID string, pairings []string) error {
	path := pairingsStorePath(streamID)
	if path == "" {
		return errors.New("homekit pairings store path unavailable")
	}
	if err := writePairingsDocument(path, streamID, pairings); err != nil {
		return err
	}
	// Keep YAML in sync as a compatibility mirror for inspection tools.
	if err := app.PatchConfig([]string{"homekit", streamID, "pairings"}, pairings); err != nil {
		return err
	}
	return nil
}

func readPairingsDocument(path string) (pairingsDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return pairingsDocument{}, err
	}
	var doc pairingsDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return pairingsDocument{}, err
	}
	clean := make([]string, 0, len(doc.Pairings))
	for _, pairing := range doc.Pairings {
		pairing = strings.TrimSpace(pairing)
		if pairing != "" {
			clean = append(clean, pairing)
		}
	}
	doc.Pairings = clean
	return doc, nil
}

func writePairingsDocument(path, streamID string, pairings []string) error {
	clean := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		pairing = strings.TrimSpace(pairing)
		if pairing != "" {
			clean = append(clean, pairing)
		}
	}
	raw, err := json.Marshal(pairingsDocument{SchemaVersion: 1, StreamID: streamID, Pairings: clean})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}
