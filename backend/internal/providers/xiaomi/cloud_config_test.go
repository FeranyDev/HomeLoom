package xiaomi

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func TestDecodeCloudConfigSupportsAccountOrImportedSession(t *testing.T) {
	account, err := decodeCloudConfig(providerconfig.Config{Config: []byte(`{"username":"owner@example.com","password":"secret","devices":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if account.Region != "cn" || account.PollIntervalSec != defaultCloudPollInterval {
		t.Fatalf("account defaults = %#v", account)
	}
	security := base64.StdEncoding.EncodeToString(make([]byte, 16))
	session, err := decodeCloudConfig(providerconfig.Config{Config: []byte(`{"region":"sg","userId":"123","ssecurity":"` + security + `","serviceToken":"token","devices":[]}`)})
	if err != nil || session.Region != "sg" {
		t.Fatalf("session config = %#v, %v", session, err)
	}
	_, err = decodeCloudConfig(providerconfig.Config{Config: []byte(`{"username":"owner@example.com","devices":[]}`)})
	if err == nil || !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("incomplete account error = %v", err)
	}
}

func TestCloudConfigUsesDistinctThirdPartyProviderIdentity(t *testing.T) {
	if XiaomiMIoTCloudProviderType != "xiaomi-miot-cloud" {
		t.Fatalf("provider type = %q", XiaomiMIoTCloudProviderType)
	}
	if XiaomiHomeCloudProviderType != "xiaomi-home-cloud" || XiaomiHomeCloudProviderType == XiaomiMIoTCloudProviderType {
		t.Fatalf("official cloud reservation = %q", XiaomiHomeCloudProviderType)
	}
}
