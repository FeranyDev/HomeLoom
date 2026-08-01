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

type cloudLoginAuth struct {
	Sign            string `json:"_sign"`
	QS              string `json:"qs"`
	Callback        string `json:"callback"`
	Location        string `json:"location"`
	UserID          any    `json:"userId"`
	Ssecurity       string `json:"ssecurity"`
	PassToken       string `json:"passToken"`
	NotificationURL string `json:"notificationUrl"`
	CaptchaURL      string `json:"captchaUrl"`
	Code            int    `json:"code"`
	Description     string `json:"description"`
}

// IdentityVerificationRequiredError indicates that Xiaomi requires a
// short-lived phone or email verification before password login can finish.
// The client and its cookie jar must be retained while the code is entered.
type IdentityVerificationRequiredError struct {
	URL string
}

func (e *IdentityVerificationRequiredError) Error() string {
	return "Xiaomi account requires identity verification: " + e.URL
}

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
	passToken    string
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
		userID: config.UserID, ssecurity: config.Ssecurity, serviceToken: config.ServiceToken, passToken: config.PassToken,
	}
}

func (c *httpMiotCloudClient) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx, false)
}

func (c *httpMiotCloudClient) loginLocked(ctx context.Context, acceptStepOneSession bool) error {
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
	var auth cloudLoginAuth
	if err := decodeXiaomiJSON(step1, &auth); err != nil {
		return fmt.Errorf("decode Xiaomi cloud login step 1: %w", err)
	}
	// serviceLogin can return a login-page location without an authenticated
	// session. Password login must still complete serviceLoginAuth2; only the
	// separate identity-verification flow may consume a verified location
	// directly, and that flow is not represented by this method.
	if !acceptStepOneSession || auth.Location == "" {
		digest := md5.Sum([]byte(c.config.Password)) // #nosec G401 -- required by Xiaomi account protocol.
		form := url.Values{
			"user": {c.config.Username}, "hash": {strings.ToUpper(hex.EncodeToString(digest[:]))},
			"callback": {auth.Callback}, "sid": {"xiaomiio"}, "qs": {auth.QS}, "_sign": {auth.Sign}, "_json": {"true"},
		}
		step2, requestErr := c.accountRequest(ctx, http.MethodPost, "/pass/serviceLoginAuth2", url.Values{"_json": {"true"}}, form)
		if requestErr != nil {
			return fmt.Errorf("Xiaomi cloud login step 2: %w", requestErr)
		}
		auth.Location, auth.UserID, auth.Ssecurity = "", nil, ""
		auth.NotificationURL, auth.CaptchaURL, auth.Code, auth.Description = "", "", 0, ""
		if err := decodeXiaomiJSON(step2, &auth); err != nil {
			return fmt.Errorf("decode Xiaomi cloud login step 2: %w", err)
		}
	}
	if auth.Location == "" {
		if auth.NotificationURL != "" {
			verificationURL, validationErr := c.validAccountURL(auth.NotificationURL)
			if validationErr != nil {
				return validationErr
			}
			return &IdentityVerificationRequiredError{URL: verificationURL.String()}
		}
		if auth.CaptchaURL != "" {
			return fmt.Errorf("Xiaomi account requires captcha verification: %s", c.accountURL(auth.CaptchaURL))
		}
		return fmt.Errorf("Xiaomi cloud login rejected (code %d): %s", auth.Code, auth.Description)
	}
	if auth.PassToken != "" {
		c.passToken = auth.PassToken
	}
	return c.completeLoginLocked(ctx, auth)
}

func (c *httpMiotCloudClient) completeLoginLocked(ctx context.Context, auth cloudLoginAuth) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.accountURL(auth.Location), nil)
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

func (c *httpMiotCloudClient) session() (string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.userID, c.ssecurity, c.serviceToken
}

func (c *httpMiotCloudClient) mediaSession() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.userID, c.passToken
}

func (c *httpMiotCloudClient) VerifyIdentity(ctx context.Context, verificationURL, ticket string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return errors.New("Xiaomi identity verification code is required")
	}
	verification, err := c.validAccountURL(verificationURL)
	if err != nil {
		return err
	}
	marker := "/fe/service/identity/authStart"
	if !strings.Contains(verification.Path, marker) {
		return errors.New("Xiaomi returned an unsupported identity verification URL")
	}
	verification.Path = strings.Replace(verification.Path, marker, "/identity/list", 1)
	data, responseCookies, err := c.accountRequestURL(ctx, http.MethodGet, verification.String(), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("load Xiaomi identity verification methods: %w", err)
	}
	var identity struct {
		Flag        int    `json:"flag"`
		Options     []int  `json:"options"`
		Code        int    `json:"code"`
		Description string `json:"description"`
	}
	if err := decodeXiaomiJSON(data, &identity); err != nil {
		return fmt.Errorf("decode Xiaomi identity verification methods: %w", err)
	}
	if len(identity.Options) == 0 && identity.Flag != 0 {
		identity.Options = []int{identity.Flag}
	}
	identitySession := cookieNamed(responseCookies, "identity_session")
	if identitySession == "" && c.http.Jar != nil {
		identitySession = cookieNamed(c.http.Jar.Cookies(verification), "identity_session")
	}
	if identitySession == "" {
		return errors.New("Xiaomi identity verification session cookie is missing; reopen the verification page and request a new code")
	}
	endpoint, flag := "", 0
	methods := append([]int{identity.Flag}, identity.Options...)
	for _, option := range methods {
		switch option {
		case 4:
			endpoint, flag = "/identity/auth/verifyPhone", option
		case 8:
			endpoint, flag = "/identity/auth/verifyEmail", option
		}
		if endpoint != "" {
			break
		}
	}
	if endpoint == "" {
		return fmt.Errorf("Xiaomi identity verification method is unsupported (flag %d, options %v)", identity.Flag, identity.Options)
	}
	query := url.Values{"_dc": {fmt.Sprint(time.Now().UnixMilli())}}
	form := url.Values{"_flag": {fmt.Sprint(flag)}, "ticket": {ticket}, "trust": {"true"}, "_json": {"true"}}
	verifyData, _, err := c.accountRequestURL(ctx, http.MethodPost, c.accountURL(endpoint), query, form, []*http.Cookie{{Name: "identity_session", Value: identitySession}})
	if err != nil {
		return fmt.Errorf("submit Xiaomi identity verification code: %w", err)
	}
	var verified cloudLoginAuth
	if err := decodeXiaomiJSON(verifyData, &verified); err != nil {
		return fmt.Errorf("decode Xiaomi identity verification result: %w", err)
	}
	if verified.Code != 0 || verified.Location == "" {
		return fmt.Errorf("Xiaomi identity verification code was rejected (code %d): %s", verified.Code, verified.Description)
	}
	verifiedURL, err := c.validAccountURL(verified.Location)
	if err != nil {
		return fmt.Errorf("invalid Xiaomi identity verification redirect: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, verifiedURL.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", cloudUserAgent)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("complete Xiaomi identity verification: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("Xiaomi identity verification redirect returned HTTP %d", response.StatusCode)
	}
	return c.loginLocked(ctx, true)
}

func cookieNamed(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

func (c *httpMiotCloudClient) validAccountURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(c.accountURL(value))
	if err != nil {
		return nil, fmt.Errorf("invalid Xiaomi account URL: %w", err)
	}
	base, err := url.Parse(c.accountBase)
	if err != nil || parsed.Scheme != base.Scheme || !strings.EqualFold(parsed.Host, base.Host) {
		return nil, errors.New("Xiaomi identity verification URL is outside the account service")
	}
	return parsed, nil
}

func (c *httpMiotCloudClient) accountURL(value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return strings.TrimRight(c.accountBase, "/") + "/" + strings.TrimLeft(value, "/")
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
		case "passToken":
			if cookie.Value != "" {
				c.passToken = cookie.Value
			}
		}
	}
}

func (c *httpMiotCloudClient) accountRequest(ctx context.Context, method, path string, query, form url.Values) ([]byte, error) {
	data, _, err := c.accountRequestURL(ctx, method, c.accountURL(path), query, form, nil)
	return data, err
}

func (c *httpMiotCloudClient) accountRequestURL(ctx context.Context, method, endpoint string, query, form url.Values, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	if len(query) > 0 {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		endpoint += separator + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("User-Agent", cloudUserAgent)
	request.AddCookie(&http.Cookie{Name: "sdkVersion", Value: "3.8.6"})
	request.AddCookie(&http.Cookie{Name: "deviceId", Value: "homeloom"})
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, response.Cookies(), err
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return nil, response.Cookies(), fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return data, response.Cookies(), nil
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
			DID        json.RawMessage `json:"did"`
			Name       string          `json:"name"`
			Model      string          `json:"model"`
			HomeID     json.RawMessage `json:"home_id"`
			HomeName   string          `json:"home_name"`
			RoomID     json.RawMessage `json:"room_id"`
			RoomName   string          `json:"room_name"`
			LocalIP    string          `json:"localip"`
			LocalIPAlt string          `json:"local_ip"`
			Token      string          `json:"token"`
			SpecType   string          `json:"spec_type"`
			Online     *bool           `json:"isOnline"`
		} `json:"list"`
	}
	if err := c.request(ctx, "home/device_list", map[string]any{"getVirtualModel": true, "getHuamiDevices": 1, "get_split_device": false, "support_smart_home": true}, &result, true); err != nil {
		return nil, err
	}
	directory := c.homeDirectory(ctx)
	items := make([]HubDevice, 0, len(result.List))
	for _, raw := range result.List {
		did := cloudID(raw.DID)
		if did == "" {
			continue
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = raw.Model
		}
		localIP := strings.TrimSpace(raw.LocalIP)
		if localIP == "" {
			localIP = strings.TrimSpace(raw.LocalIPAlt)
		}
		item := HubDevice{DID: did, Name: name, Model: raw.Model, HomeID: cloudID(raw.HomeID), HomeName: strings.TrimSpace(raw.HomeName), RoomID: cloudID(raw.RoomID), RoomName: strings.TrimSpace(raw.RoomName), LocalIP: localIP, Token: strings.TrimSpace(raw.Token), SpecType: raw.SpecType, Online: raw.Online}
		item.Local = validLocalAccess(item.LocalIP, item.Token)
		directory.merge(&item)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].HomeName != items[j].HomeName {
			return items[i].HomeName < items[j].HomeName
		}
		if items[i].RoomName != items[j].RoomName {
			return items[i].RoomName < items[j].RoomName
		}
		return items[i].Name < items[j].Name || items[i].Name == items[j].Name && items[i].DID < items[j].DID
	})
	return items, nil
}

type cloudDeviceLocation struct {
	HomeID   string
	HomeName string
	RoomID   string
	RoomName string
}

type cloudHomeDirectory struct {
	byDID    map[string]cloudDeviceLocation
	byHomeID map[string]cloudDeviceLocation
	byRoomID map[string]cloudDeviceLocation
}

type cloudHome struct {
	ID       json.RawMessage   `json:"id"`
	Name     string            `json:"name"`
	DIDs     []json.RawMessage `json:"dids"`
	RoomList []cloudRoom       `json:"roomlist"`
}

type cloudRoom struct {
	ID   json.RawMessage   `json:"id"`
	Name string            `json:"name"`
	DIDs []json.RawMessage `json:"dids"`
}

func (c *httpMiotCloudClient) homeDirectory(ctx context.Context) cloudHomeDirectory {
	directory := cloudHomeDirectory{byDID: map[string]cloudDeviceLocation{}, byHomeID: map[string]cloudDeviceLocation{}, byRoomID: map[string]cloudDeviceLocation{}}
	var result struct {
		Devices       json.RawMessage `json:"devices"`
		HomeList      []cloudHome     `json:"homelist"`
		ShareHomeList []cloudHome     `json:"share_home_list"`
		ShareHomes    []cloudHome     `json:"share_homelist"`
	}
	payload := map[string]any{"fg": true, "fetch_share": true, "fetch_share_dev": true, "fetch_cariot": true, "limit": 300, "app_ver": 7, "plat_form": 0}
	if err := c.request(ctx, "v2/homeroom/gethome_merged", payload, &result, true); err != nil {
		// Location metadata is supplementary. Keep device discovery usable for
		// regions/accounts where Xiaomi temporarily rejects this directory API.
		return directory
	}
	directory.addDeviceMetadata(result.Devices)
	homes := append(append(result.HomeList, result.ShareHomeList...), result.ShareHomes...)
	for _, home := range homes {
		homeLocation := cloudDeviceLocation{HomeID: cloudID(home.ID), HomeName: strings.TrimSpace(home.Name)}
		if homeLocation.HomeID != "" {
			directory.byHomeID[homeLocation.HomeID] = homeLocation
		}
		for _, did := range home.DIDs {
			directory.putDID(cloudID(did), homeLocation)
		}
		for _, room := range home.RoomList {
			location := homeLocation
			location.RoomID, location.RoomName = cloudID(room.ID), strings.TrimSpace(room.Name)
			if location.RoomID != "" {
				directory.byRoomID[location.RoomID] = location
			}
			for _, did := range room.DIDs {
				directory.putDID(cloudID(did), location)
			}
		}
	}
	return directory
}

func (d *cloudHomeDirectory) addDeviceMetadata(raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	type metadata struct {
		DID      json.RawMessage `json:"did"`
		HomeID   json.RawMessage `json:"home_id"`
		HomeName string          `json:"home_name"`
		RoomID   json.RawMessage `json:"room_id"`
		RoomName string          `json:"room_name"`
	}
	var keyed map[string]metadata
	if json.Unmarshal(raw, &keyed) == nil && keyed != nil {
		for did, item := range keyed {
			d.putDID(did, cloudDeviceLocation{HomeID: cloudID(item.HomeID), HomeName: strings.TrimSpace(item.HomeName), RoomID: cloudID(item.RoomID), RoomName: strings.TrimSpace(item.RoomName)})
		}
		return
	}
	var list []metadata
	if json.Unmarshal(raw, &list) == nil {
		for _, item := range list {
			d.putDID(cloudID(item.DID), cloudDeviceLocation{HomeID: cloudID(item.HomeID), HomeName: strings.TrimSpace(item.HomeName), RoomID: cloudID(item.RoomID), RoomName: strings.TrimSpace(item.RoomName)})
		}
	}
}

func (d *cloudHomeDirectory) putDID(did string, location cloudDeviceLocation) {
	if did == "" {
		return
	}
	current := d.byDID[did]
	mergeCloudLocation(&current, location)
	d.byDID[did] = current
}

func (d cloudHomeDirectory) merge(item *HubDevice) {
	location := cloudDeviceLocation{HomeID: item.HomeID, HomeName: item.HomeName, RoomID: item.RoomID, RoomName: item.RoomName}
	mergeCloudLocation(&location, d.byDID[item.DID])
	if home := d.byHomeID[location.HomeID]; location.HomeID != "" {
		mergeCloudLocation(&location, home)
	}
	if room := d.byRoomID[location.RoomID]; location.RoomID != "" {
		mergeCloudLocation(&location, room)
	}
	item.HomeID, item.HomeName = location.HomeID, location.HomeName
	item.RoomID, item.RoomName = location.RoomID, location.RoomName
}

func mergeCloudLocation(target *cloudDeviceLocation, source cloudDeviceLocation) {
	if source.HomeID != "" {
		target.HomeID = source.HomeID
	}
	if source.HomeName != "" {
		target.HomeName = source.HomeName
	}
	if source.RoomID != "" {
		target.RoomID = source.RoomID
	}
	if source.RoomName != "" {
		target.RoomName = source.RoomName
	}
}

func cloudID(value any) string {
	if raw, ok := value.(json.RawMessage); ok {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			return ""
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return strings.TrimSpace(text)
		}
		return trimmed
	}
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
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

func (c *httpMiotCloudClient) AcquireMISSAuthorization(ctx context.Context, did, clientPublic string) (xiaomiMISSAuthorization, error) {
	var result struct {
		Vendor struct {
			ID     byte `json:"vendor"`
			Params struct {
				UID string `json:"p2p_id"`
			} `json:"vendor_params"`
		} `json:"vendor"`
		PublicKey string `json:"public_key"`
		Sign      string `json:"sign"`
	}
	err := c.request(ctx, "v2/device/miss_get_vendor", map[string]any{
		"app_pubkey":      clientPublic,
		"did":             did,
		"support_vendors": "TUTK_CS2_MTP",
	}, &result, true)
	if err != nil {
		return xiaomiMISSAuthorization{}, err
	}
	vendor, err := xiaomiMISSVendorName(result.Vendor.ID)
	if err != nil {
		return xiaomiMISSAuthorization{}, err
	}
	if result.PublicKey == "" || result.Sign == "" {
		return xiaomiMISSAuthorization{}, errors.New("Xiaomi MISS authorization response is incomplete")
	}
	return xiaomiMISSAuthorization{
		DevicePublic: result.PublicKey,
		Sign:         result.Sign,
		Vendor:       vendor,
		UID:          result.Vendor.Params.UID,
	}, nil
}

func xiaomiMISSVendorName(value byte) (string, error) {
	switch value {
	case 1:
		return "tutk", nil
	case 3:
		return "agora", nil
	case 4:
		return "cs2", nil
	case 6:
		return "mtp", nil
	default:
		return "", fmt.Errorf("Xiaomi MISS returned unsupported vendor %d", value)
	}
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
