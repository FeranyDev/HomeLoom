package xiaomi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudRC4DropsProtocolPrefixAndRoundTrips(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plain := []byte(`{"params":[{"did":"1","siid":2,"piid":1}]}`)
	encrypted := rc4CryptDrop1024(key, plain)
	if bytes.Equal(encrypted, plain) {
		t.Fatal("RC4 output equals plaintext")
	}
	if decoded := rc4CryptDrop1024(key, encrypted); !bytes.Equal(decoded, plain) {
		t.Fatalf("RC4 round trip = %q", decoded)
	}
}

func TestCloudRC4ParametersCarryEncryptedDataAndSessionNonce(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	parameters, signedNonce, err := cloudRC4Parameters(http.MethodPost, "https://api.io.mi.com/app/miotspec/prop/get", map[string]string{"data": `{"params":[]}`}, security)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"data", "rc4_hash__", "signature", "ssecurity", "_nonce"} {
		if parameters.Get(key) == "" {
			t.Fatalf("missing %s: %v", key, parameters)
		}
	}
	if len(signedNonce) != 32 || parameters.Get("data") == `{"params":[]}` {
		t.Fatalf("signed nonce/data = %d/%q", len(signedNonce), parameters.Get("data"))
	}
}

func TestDecodeXiaomiJSONPreservesLargeUserID(t *testing.T) {
	var result struct {
		UserID any `json:"userId"`
	}
	if err := decodeXiaomiJSON([]byte(`&&&START&&&{"userId":9876543210987654321}`), &result); err != nil {
		t.Fatal(err)
	}
	if value := result.UserID.(interface{ String() string }).String(); value != "9876543210987654321" {
		t.Fatalf("user id = %s", value)
	}
}

func TestHTTPMiotCloudClientEncryptsPropertyRequests(t *testing.T) {
	securityBytes := []byte("0123456789abcdef")
	security := base64.StdEncoding.EncodeToString(securityBytes)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/app/miotspec/prop/get" || request.Header.Get("MIOT-ENCRYPT-ALGORITHM") != "ENCRYPT-RC4" {
			t.Errorf("request = %s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		if got := request.Header.Get("X-XIAOMI-PROTOCAL-FLAG-CLI"); got != "PROTOCAL-HTTP2" {
			t.Errorf("protocol flag = %q", got)
		}
		if cookie, err := request.Cookie("serviceToken"); err != nil || cookie.Value != "token" {
			t.Errorf("service token cookie = %#v, %v", cookie, err)
		}
		for name, want := range map[string]string{
			"yetAnotherServiceToken": "token",
			"locale":                 "zh_CN",
			"timezone":               "GMT+08:00",
			"is_daylight":            "0",
			"dst_offset":             "0",
			"channel":                "MI_APP_STORE",
		} {
			cookie, err := request.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Errorf("%s cookie = %#v, %v", name, cookie, err)
			}
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		nonce, err := base64.StdEncoding.DecodeString(request.Form.Get("_nonce"))
		if err != nil {
			t.Error(err)
		}
		sum := sha256.Sum256(append(append([]byte(nil), securityBytes...), nonce...))
		encrypted, err := base64.StdEncoding.DecodeString(request.Form.Get("data"))
		if err != nil {
			t.Error(err)
		}
		var payload struct {
			Params []cloudProperty `json:"params"`
		}
		if err := json.Unmarshal(rc4CryptDrop1024(sum[:], encrypted), &payload); err != nil {
			t.Errorf("decrypt payload: %v", err)
		}
		if len(payload.Params) != 1 || payload.Params[0].DID != "device-1" || payload.Params[0].SIID != 2 || payload.Params[0].PIID != 1 {
			t.Errorf("payload = %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":0,"result":[{"did":"device-1","siid":2,"piid":1,"value":true,"code":0}]}`))
	}))
	defer server.Close()
	config := CloudConfig{Region: "cn", UserID: "123", Ssecurity: security, ServiceToken: "token", RequestTimeoutSec: 5}
	client := newHTTPMiotCloudClient(config)
	client.apiBase, client.http = server.URL+"/app", server.Client()
	result, err := client.GetProperties(context.Background(), []cloudProperty{{DID: "device-1", SIID: 2, PIID: 1}})
	if err != nil || len(result) != 1 || result[0].Value != true {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestHTTPMiotCloudClientAcquiresStructuredMISSAuthorization(t *testing.T) {
	securityBytes := []byte("0123456789abcdef")
	security := base64.StdEncoding.EncodeToString(securityBytes)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/app/v2/device/miss_get_vendor" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		if _, err := request.Cookie("passToken"); err == nil {
			t.Error("passToken must not be copied into the MIoT API request")
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		nonce, err := base64.StdEncoding.DecodeString(request.Form.Get("_nonce"))
		if err != nil {
			t.Error(err)
		}
		sum := sha256.Sum256(append(append([]byte(nil), securityBytes...), nonce...))
		encrypted, err := base64.StdEncoding.DecodeString(request.Form.Get("data"))
		if err != nil {
			t.Error(err)
		}
		var payload struct {
			ClientPublic string `json:"app_pubkey"`
			DID          string `json:"did"`
			Vendors      string `json:"support_vendors"`
		}
		if err := json.Unmarshal(rc4CryptDrop1024(sum[:], encrypted), &payload); err != nil {
			t.Errorf("decrypt MISS payload: %v", err)
		}
		if payload.ClientPublic != "client-public" || payload.DID != "camera-did" ||
			payload.Vendors != "TUTK_CS2_MTP" {
			t.Errorf("MISS payload = %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":0,"result":{"vendor":{"vendor":4,"vendor_params":{"p2p_id":"camera-uid"}},"public_key":"device-public","sign":"short-lived-sign"}}`))
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", UserID: "123", Ssecurity: security,
		ServiceToken: "service-token", PassToken: "distinct-pass-token", RequestTimeoutSec: 5,
	})
	client.apiBase, client.http = server.URL+"/app", server.Client()
	result, err := client.AcquireMISSAuthorization(context.Background(), "camera-did", "client-public")
	if err != nil {
		t.Fatal(err)
	}
	if result.DevicePublic != "device-public" || result.Sign != "short-lived-sign" ||
		result.Vendor != "cs2" || result.UID != "camera-uid" {
		t.Fatalf("MISS authorization = %#v", result)
	}
}

func TestHTTPMiotCloudClientMergesHomeAndRoomDirectoryIntoDevices(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/app/home/device_list":
			_, _ = response.Write([]byte(`{"code":0,"result":{"list":[{"did":"device-room","name":"客厅空调","model":"xiaomi.aircondition.v1","localip":"192.168.1.20","token":"30313233343536373839616263646566"},{"did":"device-shared","name":"共享灯","model":"vendor.light.v1"},{"did":"device-unassigned","name":"门锁","model":"vendor.lock.v1","home_id":"home-main"}]}}`))
		case "/app/v2/homeroom/gethome_merged":
			_, _ = response.Write([]byte(`{"code":0,"result":{"homelist":[{"id":"home-main","name":"我的家","dids":["device-unassigned"],"roomlist":[{"id":"room-living","name":"客厅","dids":["device-room"]}]}],"share_home_list":[{"id":"home-shared","name":"父母家","roomlist":[{"id":"room-shared","name":"卧室","dids":["device-shared"]}]}]}}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newHTTPMiotCloudClient(CloudConfig{Region: "cn", UserID: "123", Ssecurity: security, ServiceToken: "token", RequestTimeoutSec: 5})
	client.apiBase, client.http = server.URL+"/app", server.Client()

	devices, err := client.DeviceList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byDID := make(map[string]HubDevice, len(devices))
	for _, item := range devices {
		byDID[item.DID] = item
	}
	if item := byDID["device-room"]; item.HomeID != "home-main" || item.HomeName != "我的家" || item.RoomID != "room-living" || item.RoomName != "客厅" {
		t.Fatalf("room device location = %#v", item)
	}
	if item := byDID["device-room"]; item.LocalIP != "192.168.1.20" || !item.Local || item.Token == "" {
		t.Fatalf("room device local access = %#v", item)
	}
	if item := byDID["device-shared"]; item.HomeID != "home-shared" || item.HomeName != "父母家" || item.RoomName != "卧室" {
		t.Fatalf("shared device location = %#v", item)
	}
	if item := byDID["device-unassigned"]; item.HomeName != "我的家" || item.RoomID != "" || item.RoomName != "" {
		t.Fatalf("unassigned device location = %#v", item)
	}
}

func TestHTTPMiotCloudClientKeepsDeviceListWhenHomeDirectoryFails(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/app/home/device_list" {
			_, _ = response.Write([]byte(`{"code":0,"result":{"list":[{"did":"device-1","name":"空调","home_id":"home-1","home_name":"我的家","room_id":"room-1","room_name":"卧室"}]}}`))
			return
		}
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := newHTTPMiotCloudClient(CloudConfig{Region: "cn", UserID: "123", Ssecurity: security, ServiceToken: "token", RequestTimeoutSec: 5})
	client.apiBase, client.http = server.URL+"/app", server.Client()

	devices, err := client.DeviceList(context.Background())
	if err != nil || len(devices) != 1 || devices[0].HomeName != "我的家" || devices[0].RoomName != "卧室" {
		t.Fatalf("devices = %#v, error = %v", devices, err)
	}
}

func TestHTTPMiotCloudClientHTTP426ServiceTokenExpiredRetriesAfterPasswordLogin(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	apiCalls, serviceLoginCalls, serviceLoginAuthCalls, redirectCalls := 0, 0, 0, 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app/miotspec/prop/get":
			apiCalls++
			if apiCalls == 1 {
				response.WriteHeader(http.StatusUpgradeRequired)
				_, _ = response.Write([]byte(`{"code":0,"message":"SERVICETOKEN_EXPIRED"}`))
				return
			}
			_, _ = response.Write([]byte(`{"code":0,"result":[{"did":"device-1","siid":2,"piid":1,"value":true,"code":0}]}`))
		case "/pass/serviceLogin":
			serviceLoginCalls++
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			serviceLoginAuthCalls++
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"42","ssecurity":"` + security + `"}`))
		case "/sts":
			redirectCalls++
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "fresh-service-token", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", Username: "owner@example.com", Password: "password",
		UserID: "stale-user", Ssecurity: security, ServiceToken: "stale-service-token", RequestTimeoutSec: 5,
	})
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	result, err := client.GetProperties(context.Background(), []cloudProperty{{DID: "device-1", SIID: 2, PIID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Value != true {
		t.Fatalf("result = %#v", result)
	}
	if apiCalls != 2 || serviceLoginCalls != 1 || serviceLoginAuthCalls != 1 || redirectCalls != 1 {
		t.Fatalf("api calls=%d serviceLogin=%d auth2=%d redirect=%d", apiCalls, serviceLoginCalls, serviceLoginAuthCalls, redirectCalls)
	}
	if client.serviceToken != "fresh-service-token" || client.userID != "42" {
		t.Fatalf("session after retry user=%q service token=%q", client.userID, client.serviceToken)
	}
}

func TestHTTPMiotCloudClientHTTP426ServiceTokenExpiredReturnsIdentityChallenge(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	apiCalls, serviceLoginCalls, serviceLoginAuthCalls := 0, 0, 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app/miotspec/prop/get":
			apiCalls++
			response.WriteHeader(http.StatusUpgradeRequired)
			_, _ = response.Write([]byte(`{"code":0,"message":"SERVICETOKEN_EXPIRED"}`))
		case "/pass/serviceLogin":
			serviceLoginCalls++
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			serviceLoginAuthCalls++
			response.WriteHeader(http.StatusUpgradeRequired)
			_, _ = response.Write([]byte(`&&&START&&&{"code":81003,"notificationUrl":"` + server.URL + `/fe/service/identity/authStart?context=renewal"}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", Username: "owner@example.com", Password: "password",
		UserID: "stale-user", Ssecurity: security, ServiceToken: "stale-service-token", RequestTimeoutSec: 5,
	})
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	_, err := client.GetProperties(context.Background(), []cloudProperty{{DID: "device-1", SIID: 2, PIID: 1}})
	var required *IdentityVerificationRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want identity verification challenge", err)
	}
	if required.URL != server.URL+"/fe/service/identity/authStart?context=renewal" {
		t.Fatalf("verification URL=%q", required.URL)
	}
	if apiCalls != 1 || serviceLoginCalls != 1 || serviceLoginAuthCalls != 1 {
		t.Fatalf("api calls=%d login=%d auth2=%d", apiCalls, serviceLoginCalls, serviceLoginAuthCalls)
	}
}

func TestHTTPMiotCloudClientHTTP426ServiceTokenExpiredWithoutPasswordRequestsReauthorization(t *testing.T) {
	const serviceToken = "stale-service-token"
	apiCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		apiCalls++
		response.WriteHeader(http.StatusUpgradeRequired)
		_, _ = response.Write([]byte(`{"code":0,"message":"SERVICETOKEN_EXPIRED"}`))
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", UserID: "session-user", Ssecurity: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")),
		ServiceToken: serviceToken, RequestTimeoutSec: 5,
	})
	client.apiBase, client.http = server.URL+"/app", server.Client()
	_, err := client.GetProperties(context.Background(), []cloudProperty{{DID: "device-1", SIID: 2, PIID: 1}})
	if err == nil || !errors.Is(err, errCloudAuthExpired) {
		t.Fatalf("error = %v, want errCloudAuthExpired", err)
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "reauthor") || !strings.Contains(message, "credential") {
		t.Fatalf("error = %q, want reauthorization and credential guidance", err)
	}
	if strings.Contains(err.Error(), serviceToken) {
		t.Fatalf("error leaked service token: %q", err)
	}
	if apiCalls != 1 {
		t.Fatalf("api calls = %d, want one request without password retry", apiCalls)
	}
}

func TestXiaomiCloudEnvelopeAuthExpiredCodes(t *testing.T) {
	for _, body := range []string{
		`{"code":2,"message":"request failed"}`,
		`{"code":3,"message":"request failed"}`,
		`{"code":401,"message":"request failed"}`,
		`{"code":"401","message":"request failed"}`,
		`{"code":0,"message":"SERVICETOKEN_EXPIRED"}`,
	} {
		envelope, err := decodeXiaomiCloudEnvelope([]byte(body))
		if err != nil || !xiaomiCloudEnvelopeAuthExpired(envelope) {
			t.Errorf("body %s: envelope=%#v err=%v", body, envelope, err)
		}
	}
}

func TestHTTPMiotCloudClientHTTP426DiagnosticRedactsTextResponse(t *testing.T) {
	const (
		userID       = "diagnostic-user-426"
		security     = "ZGlhZ25vc3RpYy1zc2VjdXJpdHktNDI2"
		serviceToken = "diagnostic-service-token-426"
		passToken    = "diagnostic-pass-token-426"
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.Header().Set("Server", "xiaomi-gateway")
		response.Header().Set("Upgrade", "h2c")
		response.Header().Set("X-XIAOMI-PROTOCAL-FLAG-SRV", "PROTOCAL-HTTP2")
		response.Header().Set("Location", "https://account.example.test/login?serviceToken="+serviceToken)
		response.WriteHeader(http.StatusUpgradeRequired)
		_, _ = response.Write([]byte("upgrade required\nserviceToken=" + serviceToken + " userId=" + userID + " ssecurity=" + security + " passToken=" + passToken + "\x00"))
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", UserID: userID, Ssecurity: security, ServiceToken: serviceToken,
		PassToken: passToken, RequestTimeoutSec: 5,
	})
	client.apiBase, client.http = server.URL+"/app", server.Client()
	err := client.request(context.Background(), "miotspec/prop/get", map[string]any{"params": []any{}}, nil, false)
	if err == nil {
		t.Fatal("expected HTTP 426 error")
	}
	got := err.Error()
	for _, want := range []string{
		"miotspec/prop/get", "HTTP 426", "Content-Type=", "text/plain", "Server=", "xiaomi-gateway",
		"Upgrade=", "h2c", "X-XIAOMI-PROTOCAL-FLAG-SRV=", "PROTOCAL-HTTP2", "body=", "[REDACTED]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q does not contain %q", got, want)
		}
	}
	for _, forbidden := range []string{serviceToken, userID, security, passToken, "Location", "account.example.test"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("diagnostic leaks %q: %s", forbidden, got)
		}
	}
	if strings.Contains(got, "\\n") || strings.Contains(got, "\\x00") {
		t.Fatalf("diagnostic body is not single-line/clean: %q", got)
	}
}

func TestHTTPMiotCloudClientHTTP426DiagnosticRedactsJSONResponse(t *testing.T) {
	const (
		userID       = "diagnostic-user-json"
		security     = "ZGlhZ25vc3RpYy1zc2VjdXJpdHktanNvbg=="
		serviceToken = "diagnostic-service-token-json"
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusUpgradeRequired)
		_, _ = response.Write([]byte(`{"code":426,"message":"upgrade","userId":"` + userID + `","ssecurity":"` + security + `","serviceToken":"` + serviceToken + `","nested":{"token":"nested-secret"}}`))
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{Region: "cn", UserID: userID, Ssecurity: security, ServiceToken: serviceToken, RequestTimeoutSec: 5})
	client.apiBase, client.http = server.URL+"/app", server.Client()
	err := client.request(context.Background(), "home/device_list", map[string]any{"getVirtualModel": true}, nil, false)
	if err == nil {
		t.Fatal("expected HTTP 426 error")
	}
	got := err.Error()
	for _, forbidden := range []string{userID, security, serviceToken, "nested-secret"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("JSON diagnostic leaks %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "home/device_list") || !strings.Contains(got, "application/json") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("JSON diagnostic missing path/content/redaction: %s", got)
	}
}

func TestXiaomiHTTPBodySummaryTruncatesAndCleansText(t *testing.T) {
	summary := xiaomiHTTPBodySummary([]byte(strings.Repeat("x", xiaomiHTTPBodySummaryLimit+100) + "\n\x00tail"))
	if len(summary) > xiaomiHTTPBodySummaryLimit {
		t.Fatalf("summary length = %d, want <= %d", len(summary), xiaomiHTTPBodySummaryLimit)
	}
	if !strings.HasSuffix(summary, "...") || strings.ContainsAny(summary, "\r\n\x00") {
		t.Fatalf("summary = %q, want truncation marker and clean single line", summary)
	}
}

func TestHTTPMiotCloudClientHTTP426DiagnosticWithoutBodyOrHeadersIsConcise(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", UserID: "user", Ssecurity: "security", ServiceToken: "token", RequestTimeoutSec: 5,
	})
	client.apiBase, client.http = server.URL+"/app", server.Client()
	err := client.request(context.Background(), "miotspec/prop/get", map[string]any{}, nil, false)
	if err == nil {
		t.Fatal("expected HTTP 426 error")
	}
	got := err.Error()
	if got != "Xiaomi cloud API miotspec/prop/get returned HTTP 426" {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestHTTPMiotCloudLoginFindsAppPathCookiesSetDuringRedirect(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			if request.Header.Get("User-Agent") != cloudUserAgent {
				t.Errorf("login user-agent = %q", request.Header.Get("User-Agent"))
			}
			for name, want := range map[string]string{"sdkVersion": "3.8.6", "deviceId": "homeloom"} {
				cookie, err := request.Cookie(name)
				if err != nil || cookie.Value != want {
					t.Errorf("%s cookie = %#v, %v", name, cookie, err)
				}
			}
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("user") != "owner@example.com" || request.Form.Get("hash") == "" {
				t.Errorf("login form = %v", request.Form)
			}
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"987654321","ssecurity":"` + security + `","passToken":"auth-pass-token"}`))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "redirect-token", Path: "/app"})
			http.SetCookie(response, &http.Cookie{Name: "userId", Value: "987654321", Path: "/app"})
			http.SetCookie(response, &http.Cookie{Name: "passToken", Value: "redirect-pass-token", Path: "/app"})
			http.Redirect(response, request, server.URL+"/done", http.StatusFound)
		case "/done":
			_, _ = response.Write([]byte("ok"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	config := CloudConfig{Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5}
	client := newHTTPMiotCloudClient(config)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	client.http.Jar = jar
	client.accountBase, client.apiBase = server.URL, server.URL+"/app"
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.userID != "987654321" || client.ssecurity != security ||
		client.serviceToken != "redirect-token" || client.passToken != "redirect-pass-token" {
		t.Fatalf("session user=%q security=%t token=%q passToken=%t", client.userID, client.ssecurity != "", client.serviceToken, client.passToken != "")
	}
}

func TestHTTPMiotCloudLoginKeepsPassTokenFromPasswordAuthResponse(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"42","ssecurity":"` + security + `","passToken":"password-auth-pass-token"}`))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "service-token", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newHTTPMiotCloudClient(CloudConfig{
		Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5,
	})
	client.accountBase, client.apiBase = server.URL, server.URL+"/app"
	client.http.Transport = server.Client().Transport
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	userID, passToken := client.mediaSession()
	if userID != "42" || passToken != "password-auth-pass-token" ||
		client.serviceToken != "service-token" {
		t.Fatalf("media session user=%q passToken=%t serviceToken=%q", userID, passToken != "", client.serviceToken)
	}
}

func TestHTTPMiotCloudLoginDoesNotTreatStepOneLocationAsAuthenticated(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	auth2Calls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/intermediate","_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			auth2Calls++
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"42","ssecurity":"` + security + `"}`))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "complete-token", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		case "/intermediate":
			t.Error("step-one location was followed before password authentication")
			_, _ = response.Write([]byte("not authenticated"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newHTTPMiotCloudClient(CloudConfig{Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5})
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth2Calls != 1 || client.userID != "42" || client.ssecurity != security || client.serviceToken != "complete-token" {
		t.Fatalf("auth2=%d session user=%q security=%t token=%q", auth2Calls, client.userID, client.ssecurity != "", client.serviceToken)
	}
}

func TestHTTPMiotCloudLoginClearsStepOneLocationWhenVerificationIsRequired(t *testing.T) {
	intermediateCalls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/intermediate","_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			_, _ = response.Write([]byte(`&&&START&&&{"code":81003,"notificationUrl":"/identity/verify"}`))
		case "/intermediate":
			intermediateCalls++
			_, _ = response.Write([]byte("not authenticated"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newHTTPMiotCloudClient(CloudConfig{Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5})
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	err := client.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), server.URL+"/identity/verify") || intermediateCalls != 0 {
		t.Fatalf("error=%v intermediate calls=%d", err, intermediateCalls)
	}
}

func TestHTTPMiotCloudLoginReportsOnlyMissingSessionFieldNames(t *testing.T) {
	client := newHTTPMiotCloudClient(CloudConfig{Username: "owner", Password: "password", RequestTimeoutSec: 5})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + "http://" + request.Host + `/done","userId":"1"}`))
		default:
			_, _ = response.Write([]byte("ok"))
		}
	}))
	defer server.Close()
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	err := client.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing ssecurity, serviceToken") || strings.Contains(err.Error(), "password") {
		t.Fatalf("error = %v", err)
	}
}
