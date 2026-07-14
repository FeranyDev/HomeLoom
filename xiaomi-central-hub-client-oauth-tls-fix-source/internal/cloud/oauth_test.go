package cloud

import (
	"net/url"
	"testing"
)

func TestAuthorizationURL(t *testing.T) {
	client := OAuthClient{
		ClientID:    "123456789012345678",
		RedirectURL: "http://homeassistant.local:8123/callback",
		Region:      "cn",
		DeviceID:    "ha.0123456789abcdef0123456789abcdef",
	}
	value, err := client.AuthorizationURL([]string{"scope-a", "scope-b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	checks := map[string]string{
		"client_id":     client.ClientID,
		"redirect_uri":  client.RedirectURL,
		"response_type": "code",
		"device_id":     client.DeviceID,
		"state":         client.State(),
		"scope":         "scope-a scope-b",
		"skip_confirm":  "True",
	}
	for key, want := range checks {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
}

func TestAPIHost(t *testing.T) {
	for _, test := range []struct {
		region string
		want   string
	}{
		{region: "cn", want: "ha.api.io.mi.com"},
		{region: "US", want: "us.ha.api.io.mi.com"},
		{region: "de", want: "de.ha.api.io.mi.com"},
	} {
		client := OAuthClient{Region: test.region}
		if got := client.APIHost(); got != test.want {
			t.Errorf("APIHost(%q) = %q, want %q", test.region, got, test.want)
		}
	}
}

func TestGeneratedIdentities(t *testing.T) {
	uuid, err := NewOAuthUUID()
	if err != nil {
		t.Fatal(err)
	}
	if len(uuid) != 32 {
		t.Fatalf("OAuth UUID length = %d, want 32", len(uuid))
	}
	virtualDID, err := NewVirtualDID()
	if err != nil {
		t.Fatal(err)
	}
	if virtualDID == "" || virtualDID == "0" {
		t.Fatalf("invalid virtual DID %q", virtualDID)
	}
}
