package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const redactedSecret = "********"

func redactProviderConfig(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(`{}`)
	}
	redactSecretValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func redactSecretValue(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if sensitiveConfigKey(key) && child != nil {
				current[key] = redactedSecret
				continue
			}
			redactSecretValue(child)
		}
	case []any:
		for _, child := range current {
			redactSecretValue(child)
		}
	}
}

func restoreProviderSecrets(next map[string]any, previous json.RawMessage) error {
	var old map[string]any
	if len(previous) > 0 {
		_ = json.Unmarshal(previous, &old)
	}
	return restoreSecretValue(next, old, "config")
}

func restoreSecretValue(next, previous any, path string) error {
	switch current := next.(type) {
	case map[string]any:
		old, _ := previous.(map[string]any)
		for key, child := range current {
			childPath := path + "." + key
			if sensitiveConfigKey(key) && child == redactedSecret {
				prior, exists := old[key]
				if !exists || prior == redactedSecret {
					return fmt.Errorf("%s contains a redacted placeholder without a stored secret", childPath)
				}
				current[key] = prior
				continue
			}
			var prior any
			if old != nil {
				prior = old[key]
			}
			if err := restoreSecretValue(child, prior, childPath); err != nil {
				return err
			}
		}
	case []any:
		old, _ := previous.([]any)
		for index, child := range current {
			prior := previousArrayValue(old, index, child)
			if err := restoreSecretValue(child, prior, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func previousArrayValue(previous []any, index int, next any) any {
	if nextObject, ok := next.(map[string]any); ok {
		if id, ok := nextObject["id"].(string); ok && id != "" {
			for _, candidate := range previous {
				oldObject, ok := candidate.(map[string]any)
				if ok && oldObject["id"] == id {
					return oldObject
				}
			}
		}
	}
	if index < len(previous) {
		return previous[index]
	}
	return nil
}

func sensitiveConfigKey(key string) bool {
	normalized := strings.Map(func(value rune) rune {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return unicode.ToLower(value)
		}
		return -1
	}, key)
	if normalized == "pwd" || normalized == "passwd" || normalized == "authorization" || normalized == "credential" || normalized == "credentials" || normalized == "encryptionkey" || normalized == "pin" || normalized == "pairingcode" || normalized == "setupuri" || normalized == "ssecurity" || normalized == "usercode" {
		return true
	}
	for _, suffix := range []string{"password", "secret", "token", "apikey", "privatekey", "devicekey"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
