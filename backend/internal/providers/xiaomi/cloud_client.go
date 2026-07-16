package xiaomi

import (
	"context"
	"crypto/md5" // #nosec G501 -- Xiaomi account protocol requires an MD5 password digest.
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- Xiaomi request signing protocol requires SHA-1.
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const cloudUserAgent = "Android-7.1.1-1.0.0-ONEPLUS A3010-136-HOMELOOM APP/xiaomi.smarthome APPV/62830"

var errCloudAuthExpired = errors.New("Xiaomi cloud session expired")

type cloudProperty struct {
	DID   string `json:"did"`
	SIID  int    `json:"siid"`
	PIID  int    `json:"piid"`
	Value any    `json:"value,omitempty"`
	Code  int    `json:"code,omitempty"`
}

type cloudAction struct {
	DID   string `json:"did"`
	SIID  int    `json:"siid"`
	AIID  int    `json:"aiid"`
	Input []any  `json:"in"`
}

type miotCloudClient interface {
	Login(context.Context) error
	DeviceList(context.Context) ([]HubDevice, error)
	GetProperties(context.Context, []cloudProperty) ([]cloudProperty, error)
	SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error)
	Action(context.Context, cloudAction) error
}

type httpMiotCloudClient struct {
	config      CloudConfig
	http        *http.Client
	accountBase string
	apiBase     string

	mu           sync.Mutex
	userID       string
	ssecurity    string
	serviceToken string
}

func newHTTPMiotCloudClient(config CloudConfig) *httpMiotCloudClient {
	jar, _ := cookiejar.New(nil)
	base := "https://api.io.mi.com/app"
	if config.Region != "" && config.Region != "cn" {
		base = "https://" + config.Region + ".api.io.mi.com/app"
	}
	return &httpMiotCloudClient{
		config: config, http: &http.Client{Jar: jar, Timeout: config.requestTimeout()},
		accountBase: "https://account.xiaomi.com", apiBase: base,
		userID: config.UserID, ssecurity: config.Ssecurity, serviceToken: config.ServiceToken,
	}
}

func (c *httpMiotCloudClient) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.userID != "" && c.ssecurity != "" && c.serviceToken != "" {
		return nil
	}
	if c.config.Username == "" || c.config.Password == "" {
		return errors.New("Xiaomi cloud session is incomplete and account password is unavailable")
	}
	step1, err := c.accountRequest(ctx, http.MethodGet, "/pass/serviceLogin", url.Values{"sid": {"xiaomiio"}, "_json": {"true"}}, nil)
	if err != nil {
		return fmt.Errorf("Xiaomi cloud login step 1: %w", err)
	}
	var auth struct {
		Sign            string `json:"_sign"`
		QS              string `json:"qs"`
		Callback        string `json:"callback"`
		Location        string `json:"location"`
		UserID          any    `json:"userId"`
		Ssecurity       string `json:"ssecurity"`
		NotificationURL string `json:"notificationUrl"`
		Code            int    `json:"code"`
		Description     string `json:"description"`
	}
	if err := decodeXiaomiJSON(step1, &auth); err != nil {
		return fmt.Errorf("decode Xiaomi cloud login step 1: %w", err)
	}
	if auth.Location == "" {
		digest := md5.Sum([]byte(c.config.Password)) // #nosec G401 -- required by Xiaomi account protocol.
		form := url.Values{
			"user": {c.config.Username}, "hash": {strings.ToUpper(hex.EncodeToString(digest[:]))},
			"callback": {auth.Callback}, "sid": {"xiaomiio"}, "qs": {auth.QS}, "_sign": {auth.Sign}, "_json": {"true"},
		}
		step2, requestErr := c.accountRequest(ctx, http.MethodPost, "/pass/serviceLoginAuth2", nil, form)
		if requestErr != nil {
			return fmt.Errorf("Xiaomi cloud login step 2: %w", requestErr)
		}
		if err := decodeXiaomiJSON(step2, &auth); err != nil {
			return fmt.Errorf("decode Xiaomi cloud login step 2: %w", err)
		}
	}
	if auth.Location == "" {
		if auth.NotificationURL != "" {
			return fmt.Errorf("Xiaomi account requires identity verification: %s", auth.NotificationURL)
		}
		return fmt.Errorf("Xiaomi cloud login rejected (code %d): %s", auth.Code, auth.Description)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.Location, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", cloudUserAgent)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Xiaomi cloud login step 3: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	c.collectSessionCookies(response.Cookies())
	if c.http.Jar != nil {
		cookieURLs := make([]*url.URL, 0, 4)
		if response.Request != nil && response.Request.URL != nil {
			cookieURLs = append(cookieURLs, response.Request.URL)
		}
		for _, endpoint := range []string{auth.Location, c.apiBase, strings.TrimRight(c.apiBase, "/") + "/", strings.TrimRight(c.apiBase, "/") + "/home/device_list", c.accountBase} {
			if parsed, parseErr := url.Parse(endpoint); parseErr == nil {
				cookieURLs = append(cookieURLs, parsed)
			}
		}
		for _, endpoint := range cookieURLs {
			c.collectSessionCookies(c.http.Jar.Cookies(endpoint))
		}
	}
	if c.userID == "" {
		c.userID = fmt.Sprint(auth.UserID)
	}
	c.ssecurity = auth.Ssecurity
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Xiaomi cloud login redirect returned HTTP %d", response.StatusCode)
	}
	missing := make([]string, 0, 3)
	if c.userID == "" || c.userID == "<nil>" {
		missing = append(missing, "userId")
	}
	if c.ssecurity == "" {
		missing = append(missing, "ssecurity")
	}
	if c.serviceToken == "" {
		missing = append(missing, "serviceToken")
	}
	if len(missing) > 0 {
		return fmt.Errorf("Xiaomi cloud login did not return a complete session: missing %s (HTTP %d)", strings.Join(missing, ", "), response.StatusCode)
	}
	return nil
}

func (c *httpMiotCloudClient) collectSessionCookies(cookies []*http.Cookie) {
	for _, cookie := range cookies {
		switch cookie.Name {
		case "serviceToken", "yetAnotherServiceToken":
			if cookie.Value != "" {
				c.serviceToken = cookie.Value
			}
		case "userId":
			if cookie.Value != "" {
				c.userID = cookie.Value
			}
		}
	}
}

func (c *httpMiotCloudClient) accountRequest(ctx context.Context, method, path string, query, form url.Values) ([]byte, error) {
	endpoint := c.accountBase + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", cloudUserAgent)
	request.AddCookie(&http.Cookie{Name: "sdkVersion", Value: "3.8.6"})
	request.AddCookie(&http.Cookie{Name: "deviceId", Value: "homeloom"})
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return data, nil
}

func decodeXiaomiJSON(data []byte, output any) error {
	data = []byte(strings.TrimPrefix(strings.TrimSpace(string(data)), "&&&START&&&"))
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	return decoder.Decode(output)
}

func (c *httpMiotCloudClient) DeviceList(ctx context.Context) ([]HubDevice, error) {
	var result struct {
		List []struct {
			DID      any    `json:"did"`
			Name     string `json:"name"`
			Model    string `json:"model"`
			RoomID   any    `json:"room_id"`
			RoomName string `json:"room_name"`
			SpecType string `json:"spec_type"`
			Online   *bool  `json:"isOnline"`
		} `json:"list"`
	}
	if err := c.request(ctx, "home/device_list", map[string]any{"getVirtualModel": true, "getHuamiDevices": 1, "get_split_device": false, "support_smart_home": true}, &result, true); err != nil {
		return nil, err
	}
	items := make([]HubDevice, 0, len(result.List))
	for _, raw := range result.List {
		did := strings.TrimSpace(fmt.Sprint(raw.DID))
		if did == "" || did == "<nil>" {
			continue
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = raw.Model
		}
		items = append(items, HubDevice{DID: did, Name: name, Model: raw.Model, RoomID: strings.TrimSpace(fmt.Sprint(raw.RoomID)), RoomName: raw.RoomName, SpecType: raw.SpecType, Online: raw.Online})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name || items[i].Name == items[j].Name && items[i].DID < items[j].DID
	})
	return items, nil
}

func (c *httpMiotCloudClient) GetProperties(ctx context.Context, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.request(ctx, "miotspec/prop/get", map[string]any{"params": input}, &result, true)
	return result, err
}

func (c *httpMiotCloudClient) SetProperties(ctx context.Context, input []cloudProperty) ([]cloudProperty, error) {
	var result []cloudProperty
	err := c.request(ctx, "miotspec/prop/set", map[string]any{"params": input}, &result, true)
	return result, err
}

func (c *httpMiotCloudClient) Action(ctx context.Context, input cloudAction) error {
	var result json.RawMessage
	return c.request(ctx, "miotspec/action", input, &result, true)
}

func (c *httpMiotCloudClient) request(ctx context.Context, api string, payload, output any, retry bool) error {
	if err := c.Login(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	userID, security, token := c.userID, c.ssecurity, c.serviceToken
	c.mu.Unlock()
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(c.apiBase, "/") + "/" + strings.TrimLeft(api, "/")
	parameters, signedNonce, err := cloudRC4Parameters(http.MethodPost, endpoint, map[string]string{"data": string(data)}, security)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(parameters.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", cloudUserAgent)
	request.Header.Set("MIOT-ENCRYPT-ALGORITHM", "ENCRYPT-RC4")
	request.Header.Set("Accept-Encoding", "identity")
	for name, value := range map[string]string{"userId": userID, "serviceToken": token, "yetAnotherServiceToken": token, "locale": "zh_CN", "timezone": "GMT+08:00", "channel": "MI_APP_STORE"} {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusUnauthorized {
		err = errCloudAuthExpired
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = fmt.Errorf("Xiaomi cloud API returned HTTP %d", response.StatusCode)
	} else {
		trimmed := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			decoded, decodeErr := base64.StdEncoding.DecodeString(trimmed)
			if decodeErr == nil {
				raw = rc4CryptDrop1024(signedNonce, decoded)
			}
		}
		var envelope struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Result  json.RawMessage `json:"result"`
		}
		if decodeErr := json.Unmarshal(raw, &envelope); decodeErr != nil {
			err = fmt.Errorf("decode Xiaomi cloud API response: %w", decodeErr)
		} else if envelope.Code == 2 || envelope.Code == 3 || strings.Contains(strings.ToLower(envelope.Message), "auth err") || envelope.Message == "SERVICETOKEN_EXPIRED" {
			err = errCloudAuthExpired
		} else if envelope.Code != 0 {
			err = fmt.Errorf("Xiaomi cloud API error %d: %s", envelope.Code, envelope.Message)
		} else if output != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
			err = json.Unmarshal(envelope.Result, output)
		}
	}
	if errors.Is(err, errCloudAuthExpired) && retry && c.config.Username != "" && c.config.Password != "" {
		c.mu.Lock()
		c.userID, c.ssecurity, c.serviceToken = "", "", ""
		c.mu.Unlock()
		if loginErr := c.Login(ctx); loginErr != nil {
			return loginErr
		}
		return c.request(ctx, api, payload, output, false)
	}
	return err
}

func cloudRC4Parameters(method, endpoint string, input map[string]string, security string) (url.Values, []byte, error) {
	nonceBytes := make([]byte, 12)
	if _, err := rand.Read(nonceBytes[:8]); err != nil {
		return nil, nil, err
	}
	binary.BigEndian.PutUint32(nonceBytes[8:], uint32(time.Now().Unix()/60))
	nonce := base64.StdEncoding.EncodeToString(nonceBytes)
	securityBytes, err := base64.StdEncoding.DecodeString(security)
	if err != nil {
		return nil, nil, errors.New("invalid Xiaomi ssecurity")
	}
	sum := sha256.Sum256(append(securityBytes, nonceBytes...))
	signedNonce := sum[:]
	parameters := make(map[string]string, len(input)+1)
	for key, value := range input {
		parameters[key] = value
	}
	parameters["rc4_hash__"] = cloudSHA1Signature(method, endpoint, parameters, base64.StdEncoding.EncodeToString(signedNonce))
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded := make(url.Values, len(parameters)+3)
	for _, key := range keys {
		encrypted := rc4CryptDrop1024(signedNonce, []byte(parameters[key]))
		encoded.Set(key, base64.StdEncoding.EncodeToString(encrypted))
	}
	signatureInput := make(map[string]string, len(encoded))
	for key := range encoded {
		signatureInput[key] = encoded.Get(key)
	}
	encoded.Set("signature", cloudSHA1Signature(method, endpoint, signatureInput, base64.StdEncoding.EncodeToString(signedNonce)))
	encoded.Set("ssecurity", security)
	encoded.Set("_nonce", nonce)
	return encoded, signedNonce, nil
}

func cloudSHA1Signature(method, endpoint string, parameters map[string]string, nonce string) string {
	parsed, _ := url.Parse(endpoint)
	path := strings.TrimPrefix(parsed.Path, "/app")
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{strings.ToUpper(method), path}
	for _, key := range keys {
		parts = append(parts, key+"="+parameters[key])
	}
	parts = append(parts, nonce)
	digest := sha1.Sum([]byte(strings.Join(parts, "&"))) // #nosec G401 -- required by Xiaomi cloud protocol.
	return base64.StdEncoding.EncodeToString(digest[:])
}

func rc4CryptDrop1024(key, input []byte) []byte {
	state := make([]byte, 256)
	for index := range state {
		state[index] = byte(index)
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + int(state[i]) + int(key[i%len(key)])) & 255
		state[i], state[j] = state[j], state[i]
	}
	i, j := 0, 0
	next := func() byte {
		i = (i + 1) & 255
		j = (j + int(state[i])) & 255
		state[i], state[j] = state[j], state[i]
		return state[(int(state[i])+int(state[j]))&255]
	}
	for discard := 0; discard < 1024; discard++ {
		_ = next()
	}
	output := make([]byte, len(input))
	for index, value := range input {
		output[index] = value ^ next()
	}
	return output
}
