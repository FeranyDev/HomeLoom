package xiaomi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
		if cookie, err := request.Cookie("serviceToken"); err != nil || cookie.Value != "token" {
			t.Errorf("service token cookie = %#v, %v", cookie, err)
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

func TestHTTPMiotCloudLoginFindsAppPathCookiesSetDuringRedirect(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("user") != "owner@example.com" || request.Form.Get("hash") == "" {
				t.Errorf("login form = %v", request.Form)
			}
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"987654321","ssecurity":"` + security + `"}`))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "redirect-token", Path: "/app"})
			http.SetCookie(response, &http.Cookie{Name: "userId", Value: "987654321", Path: "/app"})
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
	if client.userID != "987654321" || client.ssecurity != security || client.serviceToken != "redirect-token" {
		t.Fatalf("session user=%q security=%t token=%q", client.userID, client.ssecurity != "", client.serviceToken)
	}
}

func TestHTTPMiotCloudLoginReportsOnlyMissingSessionFieldNames(t *testing.T) {
	client := newHTTPMiotCloudClient(CloudConfig{Username: "owner", Password: "password", RequestTimeoutSec: 5})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/pass/serviceLogin" {
			_, _ = response.Write([]byte(`&&&START&&&{"location":"` + "http://" + request.Host + `/done","userId":"1"}`))
			return
		}
		_, _ = response.Write([]byte("ok"))
	}))
	defer server.Close()
	client.accountBase, client.apiBase, client.http = server.URL, server.URL+"/app", server.Client()
	err := client.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing ssecurity, serviceToken") || strings.Contains(err.Error(), "password") {
		t.Fatalf("error = %v", err)
	}
}
