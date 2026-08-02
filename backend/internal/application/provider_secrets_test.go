package application

import (
	"encoding/json"
	"testing"
)

func TestProviderSecretsRedactAndRestoreTuyaUserCode(t *testing.T) {
	previous := json.RawMessage(`{"userCode":"user-code-1","refreshToken":"refresh-token-1"}`)
	redacted := redactProviderConfig(previous)
	if string(redacted) != `{"refreshToken":"********","userCode":"********"}` {
		t.Fatalf("redacted config = %s", redacted)
	}
	next := map[string]any{"userCode": "********", "refreshToken": "********"}
	if err := restoreProviderSecrets(next, previous); err != nil {
		t.Fatal(err)
	}
	if next["userCode"] != "user-code-1" || next["refreshToken"] != "refresh-token-1" {
		t.Fatalf("restored config = %#v", next)
	}
}
