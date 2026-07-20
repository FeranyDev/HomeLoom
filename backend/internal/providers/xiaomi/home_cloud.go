package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const (
	xiaomiDeviceListPath = "/app/v2/home/device_list_page"
	xiaomiGetPropsPath   = "/app/v2/miotspec/prop/get"
	xiaomiSetPropsPath   = "/app/v2/miotspec/prop/set"
	xiaomiActionPath     = "/app/v2/miotspec/action"
)

// homeCloudClient is the optional official-cloud route owned by a Xiaomi
// central-hub Provider. One client is shared by all configured devices. MQTT
// remains the preferred local transport; these HTTP calls are used for devices
// the gateway cannot control and as an auto-mode fallback.
type homeCloudClient interface {
	DeviceList(context.Context) ([]HubDevice, error)
	GetProperties(context.Context, []cloudProperty) ([]cloudProperty, error)
	SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error)
	Action(context.Context, cloudAction) error
	UpdateOAuth(OAuthConfig)
}

type httpHomeCloudClient struct {
	mu    sync.RWMutex
	oauth OAuthConfig
	http  *http.Client
}

func newHTTPHomeCloudClient(oauth OAuthConfig, client *http.Client) *httpHomeCloudClient {
	if client == nil {
		client = &http.Client{}
	}
	return &httpHomeCloudClient{oauth: oauth, http: client}
}

func (c *httpHomeCloudClient) UpdateOAuth(oauth OAuthConfig) {
	c.mu.Lock()
	c.oauth = oauth
	c.mu.Unlock()
}

func (c *httpHomeCloudClient) snapshot() (oauthClient, string, error) {
	c.mu.RLock()
	oauth := c.oauth
	c.mu.RUnlock()
	if strings.TrimSpace(oauth.AccessToken) == "" {
		return oauthClient{}, "", errors.New("Xiaomi OAuth access token is unavailable")
	}
	client := oauthClient{config: OAuthStartRequest{
		ClientID: oauth.ClientID, Region: oauth.Region, RedirectURL: oauth.RedirectURL,
		OAuthUUID: oauth.OAuthUUID, VirtualDID: oauth.VirtualDID,
	}, http: c.http}
	return client, oauth.AccessToken, nil
}

func (c *httpHomeCloudClient) post(ctx context.Context, path string, payload, result any) error {
	client, token, err := c.snapshot()
	if err != nil {
		return err
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := client.post(ctx, token, path, payload, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return fmt.Errorf("Xiaomi Home cloud API %s returned code %d: %s", path, envelope.Code, envelope.Message)
	}
	if result == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode Xiaomi Home cloud API %s result: %w", path, err)
	}
	return nil
}

type homeCloudLocation struct {
	HomeID, HomeName, RoomID, RoomName string
}

func (c *httpHomeCloudClient) DeviceList(ctx context.Context) ([]HubDevice, error) {
	locations, dids, err := c.homeDirectory(ctx)
	if err != nil {
		return nil, err
	}
	if len(dids) == 0 {
		return []HubDevice{}, nil
	}
	devices := make(map[string]HubDevice, len(dids))
	for start := 0; start < len(dids); start += 150 {
		end := start + 150
		if end > len(dids) {
			end = len(dids)
		}
		if err := c.deviceListPages(ctx, dids[start:end], "", devices); err != nil {
			return nil, err
		}
	}
	result := make([]HubDevice, 0, len(devices))
	for did, item := range devices {
		location := locations[did]
		item.HomeID, item.HomeName = location.HomeID, location.HomeName
		item.RoomID, item.RoomName = location.RoomID, location.RoomName
		item.CloudAvailable = true
		result = append(result, item)
	}
	sortHubDevices(result)
	return result, nil
}

func (c *httpHomeCloudClient) homeDirectory(ctx context.Context) (map[string]homeCloudLocation, []string, error) {
	type room struct {
		ID   json.RawMessage   `json:"id"`
		Name string            `json:"name"`
		DIDs []json.RawMessage `json:"dids"`
	}
	type home struct {
		ID       json.RawMessage   `json:"id"`
		Name     string            `json:"name"`
		DIDs     []json.RawMessage `json:"dids"`
		RoomList []room            `json:"roomlist"`
	}
	var result struct {
		HomeList      []home `json:"homelist"`
		ShareHomeList []home `json:"share_home_list"`
	}
	payload := map[string]any{"limit": 150, "fetch_share": true, "fetch_share_dev": true, "plat_form": 0, "app_ver": 9}
	if err := c.post(ctx, xiaomiHomeInfoPath, payload, &result); err != nil {
		return nil, nil, err
	}
	locations := make(map[string]homeCloudLocation)
	for _, current := range append(result.HomeList, result.ShareHomeList...) {
		homeID := cloudID(current.ID)
		homeName := strings.TrimSpace(current.Name)
		for _, rawDID := range current.DIDs {
			did := cloudID(rawDID)
			if did != "" {
				locations[did] = homeCloudLocation{HomeID: homeID, HomeName: homeName, RoomID: homeID, RoomName: homeName}
			}
		}
		for _, currentRoom := range current.RoomList {
			roomID := cloudID(currentRoom.ID)
			for _, rawDID := range currentRoom.DIDs {
				did := cloudID(rawDID)
				if did != "" {
					locations[did] = homeCloudLocation{HomeID: homeID, HomeName: homeName, RoomID: roomID, RoomName: strings.TrimSpace(currentRoom.Name)}
				}
			}
		}
	}
	dids := make([]string, 0, len(locations))
	for did := range locations {
		dids = append(dids, did)
	}
	sort.Strings(dids)
	return locations, dids, nil
}

func (c *httpHomeCloudClient) deviceListPages(ctx context.Context, dids []string, startDID string, output map[string]HubDevice) error {
	var result struct {
		List []struct {
			DID      json.RawMessage `json:"did"`
			Name     string          `json:"name"`
			Model    string          `json:"model"`
			SpecType string          `json:"spec_type"`
			LocalIP  string          `json:"local_ip"`
			LocalIP2 string          `json:"localip"`
			Token    string          `json:"token"`
			Online   *bool           `json:"isOnline"`
		} `json:"list"`
		HasMore      bool   `json:"has_more"`
		NextStartDID string `json:"next_start_did"`
	}
	payload := map[string]any{"limit": 200, "get_split_device": true, "get_third_device": true, "dids": dids}
	if startDID != "" {
		payload["start_did"] = startDID
	}
	if err := c.post(ctx, xiaomiDeviceListPath, payload, &result); err != nil {
		return err
	}
	for _, raw := range result.List {
		did := cloudID(raw.DID)
		if did == "" || (strings.TrimSpace(raw.Model) == "" && strings.TrimSpace(raw.SpecType) == "") {
			continue
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = raw.Model
		}
		localIP := strings.TrimSpace(raw.LocalIP)
		if localIP == "" {
			localIP = strings.TrimSpace(raw.LocalIP2)
		}
		output[did] = HubDevice{DID: did, Name: name, Model: raw.Model, SpecType: raw.SpecType, LocalIP: localIP, Token: strings.TrimSpace(raw.Token), Local: validLocalAccess(localIP, raw.Token), Online: raw.Online, CloudAvailable: true}
	}
	if result.HasMore && result.NextStartDID != "" && result.NextStartDID != startDID {
		return c.deviceListPages(ctx, dids, result.NextStartDID, output)
	}
	return nil
}

func (c *httpHomeCloudClient) GetProperties(ctx context.Context, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.post(ctx, xiaomiGetPropsPath, map[string]any{"datasource": 1, "params": input}, &result)
	return result, err
}

func (c *httpHomeCloudClient) SetProperties(ctx context.Context, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.post(ctx, xiaomiSetPropsPath, map[string]any{"params": input}, &result)
	return result, err
}

func (c *httpHomeCloudClient) Action(ctx context.Context, input cloudAction) error {
	var result struct {
		Code int `json:"code"`
	}
	if err := c.post(ctx, xiaomiActionPath, map[string]any{"params": input}, &result); err != nil {
		return err
	}
	if result.Code != 0 && result.Code != 1 {
		return fmt.Errorf("Xiaomi Home cloud action returned code %d", result.Code)
	}
	return nil
}

func sortHubDevices(items []HubDevice) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].HomeName != items[j].HomeName {
			return items[i].HomeName < items[j].HomeName
		}
		if items[i].RoomName != items[j].RoomName {
			return items[i].RoomName < items[j].RoomName
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].DID < items[j].DID
	})
}
