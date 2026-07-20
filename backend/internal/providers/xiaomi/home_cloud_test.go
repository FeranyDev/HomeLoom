package xiaomi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHomeCloudUsesOAuthDirectoryAndMIoTHTTPRoutes(t *testing.T) {
	requests := make(map[string]int)
	client := &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		requests[request.URL.Path]++
		if request.Header.Get("Authorization") != "Bearerofficial-access" || request.Header.Get("X-Client-AppId") != "1234567890" {
			t.Fatalf("unexpected cloud headers: %#v", request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		switch request.URL.Path {
		case xiaomiHomeInfoPath:
			return oauthResponse(`{"code":0,"result":{"homelist":[{"id":"home-1","name":"我的家","dids":["did-1"],"roomlist":[{"id":"room-1","name":"客厅","dids":["did-1"]}]}]}}`), nil
		case xiaomiDeviceListPath:
			if !strings.Contains(string(body), `"dids":["did-1"]`) {
				t.Fatalf("device list payload = %s", body)
			}
			return oauthResponse(`{"code":0,"result":{"list":[{"did":"did-1","name":"空调","model":"vendor.air.v1","spec_type":"urn:miot-spec-v2:device:air-conditioner:0000A004","isOnline":true}]}}`), nil
		case xiaomiGetPropsPath:
			return oauthResponse(`{"code":0,"result":[{"did":"did-1","siid":2,"piid":1,"value":true,"code":0}]}`), nil
		case xiaomiSetPropsPath:
			return oauthResponse(`{"code":0,"result":[{"did":"did-1","siid":2,"piid":1,"code":0}]}`), nil
		case xiaomiActionPath:
			if !strings.Contains(string(body), `"params"`) || !strings.Contains(string(body), `"aiid":1`) {
				t.Fatalf("action payload = %s", body)
			}
			return oauthResponse(`{"code":0,"result":{"code":0,"out":[]}}`), nil
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
			return nil, nil
		}
	})}
	cloud := newHTTPHomeCloudClient(OAuthConfig{ClientID: "1234567890", Region: "cn", RedirectURL: DefaultOAuthRedirectURL, OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: "1", AccessToken: "official-access"}, client)
	ctx := context.Background()
	items, err := cloud.DeviceList(ctx)
	if err != nil || len(items) != 1 || items[0].HomeName != "我的家" || items[0].RoomName != "客厅" || !items[0].CloudAvailable {
		t.Fatalf("directory = %#v, error = %v", items, err)
	}
	properties, err := cloud.GetProperties(ctx, []cloudProperty{{DID: "did-1", SIID: 2, PIID: 1}})
	if err != nil || len(properties) != 1 || properties[0].Value != true {
		t.Fatalf("properties = %#v, error = %v", properties, err)
	}
	if _, err := cloud.SetProperties(ctx, []cloudProperty{{DID: "did-1", SIID: 2, PIID: 1, Value: false}}); err != nil {
		t.Fatal(err)
	}
	if err := cloud.Action(ctx, cloudAction{DID: "did-1", SIID: 2, AIID: 1}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{xiaomiHomeInfoPath, xiaomiDeviceListPath, xiaomiGetPropsPath, xiaomiSetPropsPath, xiaomiActionPath} {
		if requests[path] != 1 {
			t.Fatalf("requests[%s] = %d", path, requests[path])
		}
	}
}

func TestHomeCloudUpdatesAccessTokenWithoutReplacingClient(t *testing.T) {
	seen := []string{}
	client := &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Get("Authorization"))
		return oauthResponse(`{"code":0,"result":[]}`), nil
	})}
	cloud := newHTTPHomeCloudClient(OAuthConfig{ClientID: "1", Region: "cn", AccessToken: "old"}, client)
	_, _ = cloud.GetProperties(context.Background(), nil)
	cloud.UpdateOAuth(OAuthConfig{ClientID: "1", Region: "cn", AccessToken: "new"})
	_, _ = cloud.GetProperties(context.Background(), nil)
	want, _ := json.Marshal([]string{"Bearerold", "Bearernew"})
	got, _ := json.Marshal(seen)
	if string(got) != string(want) {
		t.Fatalf("authorization headers = %s", got)
	}
}
