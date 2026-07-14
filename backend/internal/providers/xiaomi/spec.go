package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultSpecBaseURL = "https://miot-spec.org/miot-spec-v2"

type SpecCache interface {
	LoadMIoTSpec(context.Context, string, string) ([]byte, string, time.Time, bool, error)
	SaveMIoTSpec(context.Context, string, string, []byte, time.Time) error
}

type SpecResolver struct {
	cache   SpecCache
	client  *http.Client
	baseURL string
	mu      sync.Mutex
	index   map[string]string
}

type miotSpecDocument struct {
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Services    []miotSpecService `json:"services"`
}

type miotSpecService struct {
	IID         int                `json:"iid"`
	Type        string             `json:"type"`
	Description string             `json:"description"`
	Properties  []miotSpecProperty `json:"properties"`
	Actions     []miotSpecAction   `json:"actions"`
	Events      []miotSpecEvent    `json:"events"`
}

type miotSpecProperty struct {
	IID         int             `json:"iid"`
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Format      string          `json:"format"`
	Access      []string        `json:"access"`
	Unit        string          `json:"unit"`
	ValueRange  []float64       `json:"value-range"`
	ValueList   []miotSpecValue `json:"value-list"`
}

type miotSpecValue struct {
	Value       any    `json:"value"`
	Description string `json:"description"`
}

type miotSpecAction struct {
	IID         int    `json:"iid"`
	Type        string `json:"type"`
	Description string `json:"description"`
	In          []int  `json:"in"`
	Out         []int  `json:"out"`
}

type miotSpecEvent struct {
	IID         int    `json:"iid"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Arguments   []int  `json:"arguments"`
}

func NewSpecResolver(cache SpecCache) *SpecResolver {
	return &SpecResolver{cache: cache, client: &http.Client{Timeout: 12 * time.Second}, baseURL: defaultSpecBaseURL}
}

func (r *SpecResolver) Resolve(ctx context.Context, specType, model string) (miotSpecDocument, time.Time, string, error) {
	specType, model = strings.TrimSpace(specType), strings.TrimSpace(model)
	if r == nil {
		return miotSpecDocument{}, time.Time{}, "", errors.New("MIoT Spec resolver is unavailable")
	}
	if r.cache != nil {
		document, resolvedType, fetchedAt, found, err := r.cache.LoadMIoTSpec(ctx, instanceType(specType), model)
		if err != nil {
			return miotSpecDocument{}, time.Time{}, "", err
		}
		if found {
			parsed, err := decodeSpec(document)
			if err == nil {
				return parsed, fetchedAt, "miot-spec-cache", nil
			}
			_ = resolvedType
		}
	}
	resolvedType := instanceType(specType)
	if resolvedType == "" {
		var err error
		resolvedType, err = r.resolveInstanceType(ctx, specType, model)
		if err != nil {
			return miotSpecDocument{}, time.Time{}, "", err
		}
	}
	if resolvedType == "" {
		return miotSpecDocument{}, time.Time{}, "", fmt.Errorf("MIoT Spec type is unavailable for model %q", model)
	}
	endpoint := strings.TrimRight(r.baseURL, "/") + "/instance?type=" + url.QueryEscape(resolvedType)
	document, err := r.get(ctx, endpoint)
	if err != nil {
		return miotSpecDocument{}, time.Time{}, "", fmt.Errorf("fetch MIoT Spec %q: %w", resolvedType, err)
	}
	parsed, err := decodeSpec(document)
	if err != nil {
		return miotSpecDocument{}, time.Time{}, "", err
	}
	fetchedAt := time.Now().UTC()
	if r.cache != nil {
		if err := r.cache.SaveMIoTSpec(ctx, parsed.Type, model, document, fetchedAt); err != nil {
			return miotSpecDocument{}, time.Time{}, "", err
		}
	}
	return parsed, fetchedAt, "miot-spec.org", nil
}

func instanceType(value string) string {
	if len(strings.Split(value, ":")) >= 7 {
		return value
	}
	return ""
}

func (r *SpecResolver) resolveInstanceType(ctx context.Context, genericType, model string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.index == nil {
		document, err := r.get(ctx, strings.TrimRight(r.baseURL, "/")+"/instances?status=released")
		if err != nil {
			return "", fmt.Errorf("fetch MIoT Spec index: %w", err)
		}
		var envelope struct {
			Instances []struct {
				Model string `json:"model"`
				Type  string `json:"type"`
			} `json:"instances"`
		}
		if err := json.Unmarshal(document, &envelope); err != nil {
			return "", fmt.Errorf("decode MIoT Spec index: %w", err)
		}
		r.index = make(map[string]string, len(envelope.Instances))
		for _, item := range envelope.Instances {
			if item.Model != "" && item.Type != "" {
				r.index[item.Model] = item.Type
			}
		}
	}
	if resolved := r.index[model]; resolved != "" {
		return resolved, nil
	}
	if genericType != "" {
		matches := make([]string, 0, 1)
		for _, candidate := range r.index {
			if strings.HasPrefix(candidate, genericType+":") {
				matches = append(matches, candidate)
			}
		}
		sort.Strings(matches)
		if len(matches) == 1 {
			return matches[0], nil
		}
	}
	return "", nil
}

func (r *SpecResolver) get(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func decodeSpec(document []byte) (miotSpecDocument, error) {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.UseNumber()
	var parsed miotSpecDocument
	if err := decoder.Decode(&parsed); err != nil {
		return miotSpecDocument{}, fmt.Errorf("decode MIoT Spec: %w", err)
	}
	if parsed.Type == "" || len(parsed.Services) == 0 {
		return miotSpecDocument{}, errors.New("MIoT Spec has no type or services")
	}
	return parsed, nil
}

func urnName(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) > 3 {
		return parts[3]
	}
	return value
}
