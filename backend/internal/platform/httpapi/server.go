package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"github.com/feranydev/homeloom/backend/internal/mcpagent"
	"github.com/feranydev/homeloom/backend/internal/platform/subprocesslog"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff"
	sonoffcloud "github.com/feranydev/homeloom/backend/internal/providers/sonoff/cloud"
	"github.com/feranydev/homeloom/backend/internal/providers/tuya"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/webui"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/skip2/go-qrcode"
	"go.uber.org/zap"
)

type Server struct {
	address                    string
	echo                       *echo.Echo
	devices                    *application.DeviceService
	settings                   *application.SettingsService
	audit                      *application.AuditService
	exports                    *application.ExportService
	profiles                   *application.ProfileService
	logicalDevices             *application.LogicalDeviceService
	auth                       *application.AuthService
	maintenance                *application.MaintenanceService
	media                      *application.MediaService
	mcpConfigs                 *application.MCPConfigService
	aiService                  aiService
	aiAutomations              *application.AIAutomationService
	mediaPreview               *http.Client
	mediaPreviewStartupTimeout time.Duration
	mediaRuntimeDir            string
	logins                     *loginLimiter
	cloudLogins                *xiaomi.CloudLoginService
	tuyaOAuth                  *tuya.OAuthService
	tuyaSharingLogin           *tuya.SharingLoginService
	trustedProxies             []*net.IPNet
	subprocessLogs             *subprocesslog.Store
}

type aiService interface {
	AIServiceStatus(context.Context) (mcpagent.AIServiceStatus, error)
	UpdateAIService(context.Context, mcpagent.AIServiceConfig) (mcpagent.AIServiceStatus, error)
	ListAIModels(context.Context) ([]mcpagent.AIModel, error)
	StartAIRun(context.Context, string) (mcpagent.Run, error)
	StreamAIRun(context.Context, mcpagent.RunRequest, func(mcpagent.StreamEvent) error) error
	AIRun(context.Context, string) (mcpagent.Run, error)
	ApproveAIRun(context.Context, string) (mcpagent.Run, error)
}

func runtimeChanges(previousProviders, previousDiagnostics []byte, providers, diagnostics any) (map[string]any, []byte, []byte) {
	delta := make(map[string]any)
	encodedProviders, _ := json.Marshal(providers)
	if !bytes.Equal(encodedProviders, previousProviders) {
		delta["providers"] = providers
	}
	encodedDiagnostics, _ := json.Marshal(diagnostics)
	if !bytes.Equal(encodedDiagnostics, previousDiagnostics) {
		delta["diagnostics"] = diagnostics
	}
	return delta, encodedProviders, encodedDiagnostics
}

const apiVersionHeader = "HomeLoom-API-Version"

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	// Event and AI streaming clients reconnect when this deadline is reached.
	httpWriteTimeout       = 5 * time.Minute
	httpIdleTimeout        = 90 * time.Second
	httpMaxHeaderBytes     = 1 << 20
	requestBodyLimit       = "2M"
	restoreBodyLimit       = (256 << 20) + (1 << 20)
	runtimeLogReplayLimit  = 200
	runtimeLogEventsPerSec = 100
)

// runtimeLogGap tells a client that the live SSE path intentionally omitted
// one or more runtime-log entries. The REST cursor endpoint remains the source
// of truth for recovery: after is the last sequence the stream had delivered
// and before is the first sequence that was not delivered (or the first
// sequence observed after a subscriber-buffer gap).
type runtimeLogGap struct {
	After  uint64 `json:"after"`
	Before uint64 `json:"before"`
}

func runtimeLogCursor(request *http.Request) uint64 {
	value := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(request.URL.Query().Get("logAfter"))
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return cursor
}

func writeSSEEvent(writer io.Writer, name string, payload any, id uint64) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if id > 0 {
		if _, err := fmt.Fprintf(writer, "id: %d\n", id); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", name, encoded)
	return err
}

const (
	sessionCookieName = "homeloom_session"
	csrfCookieName    = "homeloom_csrf"
	csrfHeaderName    = "X-CSRF-Token"
	authSessionKey    = "homeloom.auth.session"
)

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sonoffLoginRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	CountryCode string `json:"countryCode"`
	Region      string `json:"region"`
	Endpoint    string `json:"endpoint"`
	AppID       string `json:"appId"`
	AppSecret   string `json:"appSecret"`
}

type confirmationRequest struct {
	Confirmation string `json:"confirmation"`
}

type masterKeyRotationRequest struct {
	Confirmation string `json:"confirmation"`
	Resume       bool   `json:"resume"`
}

type loginAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	now      func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt), now: time.Now}
}

func (l *loginLimiter) allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	attempt := l.attempts[key]
	if now.Before(attempt.lockedUntil) {
		return false, attempt.lockedUntil.Sub(now)
	}
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= 5*time.Minute {
		delete(l.attempts, key)
	}
	return true, 0
}

func (l *loginLimiter) failed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.attempts) >= 1024 {
		for currentKey, current := range l.attempts {
			if (!current.lockedUntil.IsZero() && !now.Before(current.lockedUntil)) || now.Sub(current.windowStart) >= 5*time.Minute {
				delete(l.attempts, currentKey)
			}
		}
	}
	attempt := l.attempts[key]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= 5*time.Minute {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.failures++
	if attempt.failures >= 5 {
		attempt.lockedUntil = now.Add(5 * time.Minute)
	}
	l.attempts[key] = attempt
	if len(l.attempts) > 2048 {
		for currentKey := range l.attempts {
			if currentKey != key {
				delete(l.attempts, currentKey)
				break
			}
		}
	}
}

func (l *loginLimiter) succeeded(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

type errorResponse struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"requestId"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func errorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	case http.StatusRequestTimeout:
		return "request_timeout"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "client_error"
	}
}

type powerRequest struct {
	Value *bool `json:"value"`
}

type deviceEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

type deviceNameRequest struct {
	Name string `json:"name"`
}

type deviceLocationRequest struct {
	Mode   device.LocationMode `json:"mode"`
	HomeID string              `json:"homeId"`
	RoomID string              `json:"roomId"`
}

type deviceLocationNameRequest struct {
	Name string `json:"name"`
}

func deviceLocationHTTPError(err error) error {
	var validationError *application.ValidationError
	if errors.As(err, &validationError) {
		return validationError
	}
	switch {
	case errors.Is(err, device.ErrLocationNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "configured location not found")
	case errors.Is(err, device.ErrLocationInUse):
		return echo.NewHTTPError(http.StatusConflict, "configured location is in use")
	case errors.Is(err, device.ErrLocationConflict):
		return echo.NewHTTPError(http.StatusConflict, "configured location name already exists")
	default:
		return echo.NewHTTPError(http.StatusServiceUnavailable, "device location catalog is unavailable").SetInternal(err)
	}
}

func aiServiceHTTPError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return echo.NewHTTPError(http.StatusRequestTimeout, "AI 思考超时，请稍后重试或缩短请求")
	}
	var remote *mcpagent.AgentControlError
	if errors.As(err, &remote) {
		switch remote.Status {
		case http.StatusBadRequest:
			return echo.NewHTTPError(http.StatusBadRequest, "AI service configuration was rejected")
		case http.StatusNotFound:
			return echo.NewHTTPError(http.StatusNotFound, "AI run not found")
		case http.StatusConflict:
			return echo.NewHTTPError(http.StatusConflict, "AI run cannot be approved")
		case http.StatusServiceUnavailable:
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service is not configured")
		case http.StatusGatewayTimeout:
			return echo.NewHTTPError(http.StatusRequestTimeout, "AI 思考超时，请稍后重试或缩短请求")
		}
	}
	return echo.NewHTTPError(http.StatusBadGateway, "AI service is unavailable").SetInternal(err)
}

func aiStreamErrorMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "AI 思考超时，请稍后重试或缩短请求"
	}
	if errors.Is(err, context.Canceled) {
		return "AI 请求已取消"
	}
	return "AI 服务暂时不可用"
}

func aiAutomationHTTPError(err error) error {
	switch {
	case errors.Is(err, application.ErrAIAutomationNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "AI automation not found")
	case errors.Is(err, application.ErrAIAutomationDisabled):
		return echo.NewHTTPError(http.StatusConflict, "AI automation is disabled")
	case errors.Is(err, aiautomation.ErrInvalidAutomation):
		return echo.NewHTTPError(http.StatusBadRequest, "AI automation configuration was rejected")
	default:
		return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable").SetInternal(err)
	}
}

type simulationRequest struct {
	Availability *device.Availability `json:"availability"`
	Online       *bool                `json:"online"`
	Power        *bool                `json:"power"`
	Temperature  *float64             `json:"temperature"`
	Humidity     *float64             `json:"humidity"`
	Contact      *bool                `json:"contact"`
	Motion       *bool                `json:"motion"`
	Active       *bool                `json:"active"`
	Speed        *float64             `json:"speed"`
	Mode         *string              `json:"mode"`
	FilterLife   *float64             `json:"filterLife"`
	FilterChange *bool                `json:"filterChange"`
	Position     *int64               `json:"position"`
	Sequence     *uint64              `json:"sequence"`
	Repeat       int                  `json:"repeat"`
}

type commandRequest struct {
	Parameters map[string]device.PropertyValue `json:"parameters"`
}

type targetRequest struct {
	ID           string                       `json:"id"`
	Type         string                       `json:"type"`
	Name         string                       `json:"name"`
	Enabled      bool                         `json:"enabled"`
	Address      string                       `json:"address"`
	Pin          string                       `json:"pin"`
	SetupID      string                       `json:"setupId"`
	StorePath    string                       `json:"storePath"`
	MatterConfig *domaintarget.MatterConfig   `json:"matterConfig"`
	DeviceIDs    []string                     `json:"deviceIds"`
	Devices      []domaintarget.VirtualDevice `json:"devices"`
}

func (r targetRequest) domain(id string) domaintarget.Config {
	if id == "" {
		id = r.ID
	}
	return domaintarget.Config{
		ID: id, Type: r.Type, Name: r.Name, Enabled: r.Enabled, Address: r.Address,
		Pin: r.Pin, SetupID: r.SetupID, StorePath: r.StorePath, MatterConfig: r.MatterConfig,
		DeviceIDs: r.DeviceIDs, Devices: r.Devices,
	}
}

// sonoffLoginHTTPError exposes only stable, non-sensitive failure details.
// eWeLink's response text may echo account input, so it must never be returned
// to the browser. The numeric response code and HTTP status are safe enough to
// distinguish rejected credentials/configuration from a cloud-side failure.
func sonoffLoginHTTPError(err error) *echo.HTTPError {
	var responseCode *sonoffcloud.ResponseCodeError
	if errors.As(err, &responseCode) {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Sonoff/eWeLink 登录被拒绝（错误码 %d）", responseCode.Code)).SetInternal(err)
	}
	var status *sonoffcloud.HTTPStatusError
	if errors.As(err, &status) {
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("Sonoff/eWeLink 服务响应异常（HTTP %d）", status.StatusCode)).SetInternal(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Sonoff/eWeLink 登录超时，请检查 HomeLoom 主机的网络后重试").SetInternal(err)
	}
	return echo.NewHTTPError(http.StatusBadRequest, "Sonoff/eWeLink 登录失败，请确认账号、密码和国家区号").SetInternal(err)
}

func NewServer(address string, devices *application.DeviceService, targets *application.TargetService, logger *zap.Logger, providerServices ...*application.ProviderService) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("module", "http-api"))
	e := echo.New()
	e.Server.ReadHeaderTimeout = httpReadHeaderTimeout
	e.Server.ReadTimeout = httpReadTimeout
	e.Server.WriteTimeout = httpWriteTimeout
	e.Server.IdleTimeout = httpIdleTimeout
	e.Server.MaxHeaderBytes = httpMaxHeaderBytes
	server := &Server{address: address, echo: e, devices: devices, logins: newLoginLimiter(), cloudLogins: xiaomi.NewCloudLoginService(), tuyaOAuth: tuya.NewOAuthService(), tuyaSharingLogin: tuya.NewSharingLoginService()}
	e.IPExtractor = server.clientIP
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		status, message := http.StatusInternalServerError, "internal server error"
		var fields map[string]string
		var validationError *application.ValidationError
		if errors.As(err, &validationError) {
			status, message, fields = http.StatusBadRequest, validationError.Message, validationError.Fields
		}
		var httpError *echo.HTTPError
		if validationError == nil && errors.As(err, &httpError) {
			status = httpError.Code
			if text, ok := httpError.Message.(string); ok && status < http.StatusInternalServerError {
				message = text
			} else if status < http.StatusInternalServerError {
				message = http.StatusText(status)
			}
		}
		requestID := c.Response().Header().Get(echo.HeaderXRequestID)
		if requestID == "" {
			requestID = c.Request().Header.Get(echo.HeaderXRequestID)
		}
		if writeErr := c.JSON(status, errorResponse{Code: errorCode(status), Message: message, RequestID: requestID, Fields: fields}); writeErr != nil {
			logger.Error("http error response failed", zap.String("request_id", requestID), zap.Error(writeErr))
		}
	}
	e.Use(middleware.RequestID())
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			requestID := c.Response().Header().Get(echo.HeaderXRequestID)
			if requestID == "" {
				requestID = c.Request().Header.Get(echo.HeaderXRequestID)
			}
			c.SetRequest(c.Request().WithContext(application.WithCorrelationID(c.Request().Context(), requestID)))
			if strings.HasPrefix(c.Request().URL.Path, "/api/v1/") {
				c.Response().Header().Set(apiVersionHeader, "1")
			}
			return next(c)
		}
	})
	e.Use(middleware.Recover())
	// Most API payloads are compact JSON. Database restore is intentionally the
	// only larger upload endpoint and applies its own hard limit below.
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		Limit: requestBodyLimit,
		Skipper: func(c echo.Context) bool {
			return c.Request().URL.Path == "/api/v1/system/restore"
		},
	}))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod:    true,
		LogStatus:    true,
		LogURI:       false,
		LogError:     true,
		LogRequestID: true,
		LogRemoteIP:  true,
		LogValuesFunc: func(c echo.Context, values middleware.RequestLoggerValues) error {
			path := c.Path()
			if path == "" {
				path = c.Request().URL.EscapedPath()
			}
			logger.Info("http request", zap.String("request_id", values.RequestID), zap.String("remote_ip", values.RemoteIP), zap.String("method", values.Method), zap.String("path", path), zap.Int("status", values.Status), zap.Error(values.Error))
			return nil
		},
	}))
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if server.auth == nil || !requiresAuthentication(c.Request()) {
				return next(c)
			}
			cookie, err := c.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "administrator login required")
			}
			session, err := server.auth.Authenticate(c.Request().Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, application.ErrInvalidSession) {
					server.clearAuthCookies(c)
					return echo.NewHTTPError(http.StatusUnauthorized, "administrator login required")
				}
				return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable").SetInternal(err)
			}
			if auditMethod(c.Request().Method) {
				if err := server.auth.VerifyCSRF(session, c.Request().Header.Get(csrfHeaderName)); err != nil {
					return echo.NewHTTPError(http.StatusForbidden, "invalid CSRF token")
				}
			}
			c.Set(authSessionKey, session)
			return next(c)
		}
	})
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			captureAuditRequestBody(c)
			if server.audit != nil {
				captureAuditPropertyBefore(c, devices)
			}
			err := next(c)
			if server.audit == nil || !auditMethod(c.Request().Method) || !strings.HasPrefix(c.Request().URL.Path, "/api/v1/") || c.Path() == "/api/v1/mapping/preview" {
				return err
			}
			status := c.Response().Status
			if err != nil {
				status = http.StatusInternalServerError
				var httpError *echo.HTTPError
				if errors.As(err, &httpError) {
					status = httpError.Code
				}
				var validationError *application.ValidationError
				if errors.As(err, &validationError) {
					status = http.StatusBadRequest
				}
			}
			action, resourceType, resourceID := auditResource(c)
			outcome := domainaudit.OutcomeSucceeded
			if status >= http.StatusBadRequest {
				outcome = domainaudit.OutcomeFailed
			}
			route := c.Path()
			if route == "" {
				route = c.Request().URL.Path
			}
			actor := "local-api"
			if session, ok := c.Get(authSessionKey).(application.AuthSession); ok {
				actor = session.Username
			}
			event := domainaudit.Event{
				CorrelationID: application.CorrelationID(c.Request().Context()), Actor: actor,
				Action: action, ResourceType: resourceType, ResourceID: resourceID,
				Method: c.Request().Method, Route: route, Status: status, Outcome: outcome,
				Details: auditRequestDetails(c, status),
			}
			if _, auditErr := server.audit.Record(c.Request().Context(), event); auditErr != nil {
				logger.Error("audit event persistence failed", zap.String("request_id", event.CorrelationID), zap.String("method", event.Method), zap.String("route", event.Route), zap.Error(auditErr))
			}
			return err
		}
	})

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	e.GET("/api/versions", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{
			"current": "v1", "supported": []string{"v1"}, "deprecated": []string{},
		}})
	})
	e.GET("/ready", func(c echo.Context) error {
		readiness := devices.Readiness(c.Request().Context())
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, map[string]any{"status": map[bool]string{true: "ready", false: "not_ready"}[readiness.Ready], "checks": readiness})
	})
	e.GET("/api/v1/system/version", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": buildinfo.Current()})
	})
	e.GET("/api/v1/auth/status", func(c echo.Context) error {
		if server.auth == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable")
		}
		token := authCookieValue(c)
		status, err := server.auth.Status(c.Request().Context(), token)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable").SetInternal(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		if token != "" && !status.Authenticated {
			server.clearAuthCookies(c)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": status})
	})
	e.POST("/api/v1/auth/setup", func(c echo.Context) error {
		if server.auth == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable")
		}
		var input authRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid administrator credentials")
		}
		session, err := server.auth.Setup(c.Request().Context(), input.Username, input.Password)
		if errors.Is(err, application.ErrAdminAlreadyInitialized) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		if err != nil {
			return err
		}
		server.setAuthCookies(c, session)
		return c.JSON(http.StatusCreated, map[string]any{"data": application.AuthStatus{Initialized: true, Authenticated: true, Username: session.Username}})
	})
	e.POST("/api/v1/auth/login", func(c echo.Context) error {
		if server.auth == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable")
		}
		key := server.clientIP(c.Request())
		if allowed, retryAfter := server.logins.allowed(key); !allowed {
			c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
			return echo.NewHTTPError(http.StatusTooManyRequests, "too many login attempts; try again later")
		}
		var input authRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid administrator credentials")
		}
		session, err := server.auth.Login(c.Request().Context(), input.Username, input.Password)
		if errors.Is(err, application.ErrInvalidCredentials) {
			server.logins.failed(key)
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid username or password")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable").SetInternal(err)
		}
		server.logins.succeeded(key)
		server.setAuthCookies(c, session)
		return c.JSON(http.StatusOK, map[string]any{"data": application.AuthStatus{Initialized: true, Authenticated: true, Username: session.Username}})
	})
	e.POST("/api/v1/auth/logout", func(c echo.Context) error {
		if server.auth == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable")
		}
		if err := server.auth.Logout(c.Request().Context(), authCookieValue(c)); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "authentication is unavailable").SetInternal(err)
		}
		server.clearAuthCookies(c)
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/device-models", func(c echo.Context) error {
		if server.profiles != nil {
			return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.ModelContracts()})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": device.ModelContracts()})
	})
	e.GET("/api/v1/device-models/custom-models", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom unified models are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.ListCustomModels()})
	})
	e.POST("/api/v1/device-models/custom-models", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom unified models are unavailable")
		}
		var item mapping.CustomModel
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid custom unified model")
		}
		created, err := server.profiles.CreateCustomModel(c.Request().Context(), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": created})
	})
	e.DELETE("/api/v1/device-models/custom-models/:deviceType", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom unified models are unavailable")
		}
		if err := server.profiles.DeleteCustomModel(c.Request().Context(), device.Type(c.Param("deviceType"))); err != nil {
			return profileHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/mapping/catalog", func(c echo.Context) error {
		models := device.ModelContracts()
		if server.profiles != nil {
			models = server.profiles.ModelContracts()
		}
		items, err := devices.ProviderCatalog(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider property catalog is unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{
			"providers": items, "models": models, "consumers": mapping.BuiltInConsumerCatalogs(),
		}})
	})
	e.GET("/api/v1/mapping/consumers", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": mapping.BuiltInConsumerCatalogs()})
	})
	e.GET("/api/v1/device-models/enum-overrides", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "model enum overrides are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.ListModelEnumOverrides()})
	})
	e.PUT("/api/v1/device-models/enum-overrides", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "model enum overrides are unavailable")
		}
		var item mapping.ModelEnumOverride
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid model enum override")
		}
		updated, err := server.profiles.UpsertModelEnumOverride(c.Request().Context(), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.DELETE("/api/v1/device-models/enum-overrides/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "model enum overrides are unavailable")
		}
		if err := server.profiles.DeleteModelEnumOverride(c.Request().Context(), c.Param("id")); err != nil {
			return profileHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/device-models/custom-properties", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom model properties are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.ListCustomModelProperties()})
	})
	e.POST("/api/v1/device-models/custom-properties", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom model properties are unavailable")
		}
		var item mapping.CustomModelProperty
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid custom model property")
		}
		created, err := server.profiles.CreateCustomModelProperty(c.Request().Context(), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": created})
	})
	e.PUT("/api/v1/device-models/custom-properties/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom model properties are unavailable")
		}
		var item mapping.CustomModelProperty
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid custom model property")
		}
		updated, err := server.profiles.UpdateCustomModelProperty(c.Request().Context(), c.Param("id"), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.DELETE("/api/v1/device-models/custom-properties/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "custom model properties are unavailable")
		}
		if err := server.profiles.DeleteCustomModelProperty(c.Request().Context(), c.Param("id")); err != nil {
			return profileHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/system/settings", func(c echo.Context) error {
		if server.settings == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "runtime settings are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.settings.Get()})
	})
	runtimeLogs := func(c echo.Context) error {
		if server.subprocessLogs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "runtime logs are unavailable")
		}
		afterValue := c.QueryParam("after")
		if afterValue == "" {
			afterValue = "0"
		}
		after, err := strconv.ParseUint(afterValue, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "after must be an unsigned sequence")
		}
		limitValue := c.QueryParam("limit")
		if limitValue == "" {
			limitValue = "500"
		}
		limit, err := strconv.Atoi(limitValue)
		if err != nil || limit < 1 || limit > subprocesslog.DefaultCapacity {
			return echo.NewHTTPError(http.StatusBadRequest, "limit must be between 1 and 2000")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": server.subprocessLogs.Snapshot(after, limit)})
	}
	// runtime-logs includes the structured HomeLoom process as well as bounded
	// Camera Kernel and Matter child output. Keep the previous endpoint as a
	// compatibility alias for older web UIs.
	e.GET("/api/v1/system/runtime-logs", runtimeLogs)
	e.GET("/api/v1/system/subprocess-logs", runtimeLogs)
	e.PUT("/api/v1/system/settings", func(c echo.Context) error {
		if server.settings == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "runtime settings are unavailable")
		}
		var input application.RuntimeSettings
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid runtime settings")
		}
		updated, err := server.settings.Save(c.Request().Context(), input)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.GET("/api/v1/ai-service/config", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service configuration is unavailable")
		}
		status, err := server.aiService.AIServiceStatus(c.Request().Context())
		if err != nil {
			return aiServiceHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": status})
	})
	e.PUT("/api/v1/ai-service/config", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service configuration is unavailable")
		}
		var input mcpagent.AIServiceConfig
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid AI service configuration")
		}
		status, err := server.aiService.UpdateAIService(c.Request().Context(), input)
		if err != nil {
			return aiServiceHTTPError(err)
		}
		if server.aiAutomations != nil {
			if err := server.aiAutomations.SetHomeTimeZone(status.HomePreferences.TimeZone); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid household time zone").SetInternal(err)
			}
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": status})
	})
	e.GET("/api/v1/ai-service/models", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service configuration is unavailable")
		}
		models, err := server.aiService.ListAIModels(c.Request().Context())
		if err != nil {
			return aiServiceHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": models})
	})
	e.POST("/api/v1/ai-service/runs", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service is unavailable")
		}
		var input struct {
			Message string `json:"message"`
		}
		if err := c.Bind(&input); err != nil || strings.TrimSpace(input.Message) == "" || len(input.Message) > 16<<10 {
			return echo.NewHTTPError(http.StatusBadRequest, "AI message is required and must not exceed 16384 characters")
		}
		run, err := server.aiService.StartAIRun(c.Request().Context(), strings.TrimSpace(input.Message))
		if err != nil {
			return aiServiceHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": run})
	})
	e.POST("/api/v1/ai-service/runs/stream", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service is unavailable")
		}
		var input struct {
			Message string                      `json:"message"`
			History []mcpagent.ConversationTurn `json:"history,omitempty"`
		}
		if err := c.Bind(&input); err != nil || strings.TrimSpace(input.Message) == "" || len(input.Message) > 16<<10 || len(input.History) > 24 {
			return echo.NewHTTPError(http.StatusBadRequest, "AI message or conversation history is invalid")
		}
		writer := c.Response().Writer
		flusher, ok := writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "response streaming is unavailable")
		}
		writer.Header().Set(echo.HeaderContentType, "text/event-stream; charset=utf-8")
		writer.Header().Set(echo.HeaderCacheControl, "no-cache, no-store")
		writer.Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)
		if err := writeSSEEvent(writer, "ready", mcpagent.StreamEvent{Type: "ready"}, 0); err != nil {
			return nil
		}
		flusher.Flush()
		err := server.aiService.StreamAIRun(c.Request().Context(), mcpagent.RunRequest{Message: strings.TrimSpace(input.Message), Context: mcpagent.RunContext{Source: mcpagent.RunSourceInteractive}, History: input.History}, func(event mcpagent.StreamEvent) error {
			if err := writeSSEEvent(writer, event.Type, event, 0); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			_ = writeSSEEvent(writer, "error", mcpagent.StreamEvent{Type: "error", Error: aiStreamErrorMessage(err)}, 0)
			flusher.Flush()
		}
		return nil
	})
	e.GET("/api/v1/ai-service/runs/:id", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service is unavailable")
		}
		run, err := server.aiService.AIRun(c.Request().Context(), c.Param("id"))
		if err != nil {
			return aiServiceHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": run})
	})
	e.POST("/api/v1/ai-service/runs/:id/approve", func(c echo.Context) error {
		if server.aiService == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI service is unavailable")
		}
		run, err := server.aiService.ApproveAIRun(c.Request().Context(), c.Param("id"))
		if err != nil {
			return aiServiceHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": run})
	})
	e.GET("/api/v1/ai-service/automations", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		items, err := server.aiAutomations.List(c.Request().Context())
		if err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.POST("/api/v1/ai-service/automations", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		var input aiautomation.Automation
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid AI automation configuration")
		}
		item, err := server.aiAutomations.Create(c.Request().Context(), input)
		if err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": item})
	})
	e.PUT("/api/v1/ai-service/automations/:id", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		var input aiautomation.Automation
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid AI automation configuration")
		}
		item, err := server.aiAutomations.Update(c.Request().Context(), c.Param("id"), input)
		if err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.DELETE("/api/v1/ai-service/automations/:id", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		if err := server.aiAutomations.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.POST("/api/v1/ai-service/automations/:id/run", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		item, run, err := server.aiAutomations.RunNow(c.Request().Context(), c.Param("id"))
		if err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"automation": item, "run": run}})
	})
	e.POST("/api/v1/ai-service/automations/:id/runs/:runID/approve", func(c echo.Context) error {
		if server.aiAutomations == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "AI automation is unavailable")
		}
		item, run, err := server.aiAutomations.ApproveRun(c.Request().Context(), c.Param("id"), c.Param("runID"))
		if err != nil {
			return aiAutomationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"automation": item, "run": run}})
	})
	e.GET("/api/v1/diagnostics", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.Metrics()})
	})
	e.GET("/api/v1/media/streams", func(c echo.Context) error {
		if server.media == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable")
		}
		items, err := server.media.List(c.Request().Context())
		if err != nil {
			return mediaHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.POST("/api/v1/media/streams", func(c echo.Context) error {
		if server.media == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable")
		}
		var item domainmedia.StreamSpec
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid media stream")
		}
		created, err := server.media.Create(c.Request().Context(), item)
		if err != nil {
			return mediaHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": created})
	})
	e.PUT("/api/v1/media/streams/:id", func(c echo.Context) error {
		if server.media == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable")
		}
		var item domainmedia.StreamSpec
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid media stream")
		}
		updated, err := server.media.Update(c.Request().Context(), c.Param("id"), item)
		if err != nil {
			return mediaHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.DELETE("/api/v1/media/streams/:id", func(c echo.Context) error {
		if server.media == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable")
		}
		if err := server.media.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return mediaHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/media/health", func(c echo.Context) error {
		if server.media == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]string{"status": server.media.RuntimeStatus()}})
	})
	e.GET("/api/v1/media/devices/:deviceId/preview.mp4", server.serveMediaPreview)
	e.POST("/api/v1/mapping/preview", func(c echo.Context) error {
		var input mapping.PreviewRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping preview")
		}
		if input.ProfileID != "" {
			if server.profiles == nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
			}
			stored, err := server.profiles.Get(input.ProfileID)
			if err != nil {
				return profileHTTPError(err)
			}
			input.Profile = stored.Profile
		}
		result, err := mapping.Preview(input)
		if err != nil {
			var validationError *mapping.ValidationError
			if errors.As(err, &validationError) {
				return application.NewValidationError("invalid mapping preview", validationError.Fields)
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "mapping preview failed").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.GET("/api/v1/mapping/profiles", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.List()})
	})
	e.POST("/api/v1/mapping/profiles", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		var item mapping.Profile
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping profile")
		}
		created, err := server.profiles.Create(c.Request().Context(), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": created})
	})
	e.POST("/api/v1/mapping/profiles/import", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		var input struct {
			Profiles []mapping.Profile `json:"profiles"`
		}
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping profile import")
		}
		items, err := server.profiles.Import(c.Request().Context(), input.Profiles)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/mapping/profiles/export", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		return writeJSONDownload(c, "homeloom-mapping-profiles-"+time.Now().UTC().Format("20060102T150405Z")+".json", map[string]any{"schemaVersion": 1, "profiles": server.profiles.Export()})
	})
	e.GET("/api/v1/mapping/profiles/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		item, err := server.profiles.Get(c.Param("id"))
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.PUT("/api/v1/mapping/profiles/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		var item mapping.Profile
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping profile")
		}
		updated, err := server.profiles.Update(c.Request().Context(), c.Param("id"), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.DELETE("/api/v1/mapping/profiles/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping profiles are unavailable")
		}
		if err := server.profiles.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return profileHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/mapping/bindings", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping bindings are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.profiles.ListBindings()})
	})
	e.POST("/api/v1/mapping/bindings", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping bindings are unavailable")
		}
		var item mapping.Binding
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping binding")
		}
		created, err := server.profiles.CreateBinding(c.Request().Context(), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": created})
	})
	e.GET("/api/v1/mapping/bindings/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping bindings are unavailable")
		}
		item, err := server.profiles.GetBinding(c.Param("id"))
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.PUT("/api/v1/mapping/bindings/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping bindings are unavailable")
		}
		var item mapping.Binding
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid mapping binding")
		}
		updated, err := server.profiles.UpdateBinding(c.Request().Context(), c.Param("id"), item)
		if err != nil {
			return profileHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": updated})
	})
	e.DELETE("/api/v1/mapping/bindings/:id", func(c echo.Context) error {
		if server.profiles == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "mapping bindings are unavailable")
		}
		if err := server.profiles.DeleteBinding(c.Request().Context(), c.Param("id")); err != nil {
			return profileHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/system/config-export", func(c echo.Context) error {
		if server.exports == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "configuration export is unavailable")
		}
		return writeJSONDownload(c, "homeloom-config-"+time.Now().UTC().Format("20060102T150405Z")+".json", server.exports.Configuration())
	})
	e.GET("/api/v1/system/diagnostic-bundle", func(c echo.Context) error {
		if server.exports == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "diagnostic bundle is unavailable")
		}
		bundle, err := server.exports.Diagnostics(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to create diagnostic bundle").SetInternal(err)
		}
		return writeJSONDownload(c, "homeloom-diagnostics-"+time.Now().UTC().Format("20060102T150405Z")+".json", bundle)
	})
	e.POST("/api/v1/system/backup", func(c echo.Context) error {
		if server.maintenance == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database maintenance is unavailable")
		}
		var input confirmationRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid backup confirmation")
		}
		artifact, err := server.maintenance.Backup(c.Request().Context(), input.Confirmation)
		if err != nil {
			return err
		}
		defer artifact.Cleanup()
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
		return c.Attachment(artifact.Path, artifact.Filename)
	})
	e.POST("/api/v1/system/restore", func(c echo.Context) error {
		if server.maintenance == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database maintenance is unavailable")
		}
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, restoreBodyLimit)
		fileHeader, err := c.FormFile("file")
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "restore archive is too large")
			}
			return application.NewValidationError("restore archive is required", map[string]string{"file": "required"})
		}
		file, err := fileHeader.Open()
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "failed to open restore archive").SetInternal(err)
		}
		defer file.Close()
		result, err := server.maintenance.StageRestore(c.Request().Context(), file, c.FormValue("confirmation"))
		if err != nil {
			if strings.Contains(err.Error(), "already pending") {
				return echo.NewHTTPError(http.StatusConflict, err.Error())
			}
			return err
		}
		return c.JSON(http.StatusAccepted, map[string]any{"data": result})
	})
	e.GET("/api/v1/system/master-key", func(c echo.Context) error {
		if server.maintenance == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database maintenance is unavailable")
		}
		status, err := server.maintenance.MasterKeyStatus(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "master key rotation is unavailable").SetInternal(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": status})
	})
	e.POST("/api/v1/system/master-key/rotate", func(c echo.Context) error {
		if server.maintenance == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database maintenance is unavailable")
		}
		var input masterKeyRotationRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid master key rotation confirmation")
		}
		result, err := server.maintenance.RotateMasterKey(c.Request().Context(), input.Confirmation, input.Resume)
		if err != nil {
			var validation *application.ValidationError
			if errors.As(err, &validation) {
				return err
			}
			return echo.NewHTTPError(http.StatusServiceUnavailable, "master key rotation did not complete; inspect status and retry safely").SetInternal(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.GET("/api/v1/audit-events", func(c echo.Context) error {
		if server.audit == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "audit log is unavailable")
		}
		limit := 100
		if raw := c.QueryParam("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 500 {
				return echo.NewHTTPError(http.StatusBadRequest, "limit must be between 1 and 500")
			}
			limit = parsed
		}
		items, err := server.audit.List(c.Request().Context(), limit)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to list audit events").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/metrics", func(c echo.Context) error {
		metrics := devices.Metrics()
		var output strings.Builder
		writeMetric := func(name string, value any) { fmt.Fprintf(&output, "homeloom_%s %v\n", name, value) }
		writeMetric("events_received_total", metrics.EventsReceived)
		writeMetric("events_processed_total", metrics.EventsProcessed)
		writeMetric("events_dropped_total", metrics.EventsDropped)
		writeMetric("event_queue_pending", metrics.EventQueuePending)
		writeMetric("event_queue_capacity", metrics.EventQueueCapacity)
		writeMetric("target_events_dropped_total", metrics.TargetEventsDropped)
		writeMetric("state_events_dropped_total", metrics.StateEventsDropped)
		writeMetric("states_marked_stale_total", metrics.StatesMarkedStale)
		writeMetric("commands_started_total", metrics.CommandsStarted)
		writeMetric("commands_confirmed_total", metrics.CommandsConfirmed)
		writeMetric("commands_rejected_total", metrics.CommandsRejected)
		writeMetric("commands_timed_out_total", metrics.CommandsTimedOut)
		writeMetric("commands_superseded_total", metrics.CommandsSuperseded)
		writeMetric("commands_coalesced_total", metrics.CommandsCoalesced)
		writeMetric("commands_outcome_unknown_total", metrics.CommandsOutcomeUnknown)
		writeMetric("homekit_pushes_total", metrics.HomeKitPushes)
		writeMetric("devices_online", metrics.OnlineDevices)
		writeMetric("devices_offline", metrics.OfflineDevices)
		writeMetric("devices_unknown", metrics.UnknownDevices)
		writeMetric("devices_disabled", metrics.DisabledDevices)
		writeMetric("devices_removed", metrics.RemovedDevices)
		writeMetric("providers_running", metrics.ProvidersRunning)
		writeMetric("provider_retries_total", metrics.ProviderRetries)
		writeMetric("device_subscribers", metrics.DeviceSubscribers)
		writeMetric("state_subscribers", metrics.StateSubscribers)
		writeMetric("command_average_latency_milliseconds", metrics.CommandAverageLatencyMS)
		writeMetric("command_queue_pending", metrics.CommandQueuePending)
		writeMetric("command_queue_max_pending", metrics.CommandQueueMaxPending)
		writeMetric("event_average_latency_milliseconds", metrics.EventAverageLatencyMS)
		writeMetric("event_max_latency_milliseconds", metrics.EventMaxLatencyMS)
		writeMetric("slow_event_handlers_total", metrics.SlowEventHandlers)
		writeMetric("database_operations_total", metrics.DatabaseOperations)
		writeMetric("database_average_latency_milliseconds", metrics.DatabaseAverageLatencyMS)
		writeMetric("database_max_latency_milliseconds", metrics.DatabaseMaxLatencyMS)
		writeMetric("provider_clock_skew_events_total", metrics.ProviderClockSkewEvents)
		writeMetric("provider_max_clock_skew_milliseconds", metrics.ProviderMaxClockSkewMS)
		writeMetric("provider_snapshot_age_events_total", metrics.ProviderSnapshotAgeEvents)
		writeMetric("provider_max_snapshot_age_milliseconds", metrics.ProviderMaxSnapshotAgeMS)
		writeMetric("provider_events_ignored_total", metrics.ProviderEventsIgnored)
		writeMetric("provider_messages_received_total", metrics.ProviderMessagesReceived)
		writeMetric("provider_messages_invalid_total", metrics.ProviderMessagesInvalid)
		writeMetric("provider_messages_dropped_total", metrics.ProviderMessagesDropped)
		writeMetric("provider_commands_published_total", metrics.ProviderCommandsPublished)
		writeMetric("mapping_applied_total", metrics.MappingApplied)
		writeMetric("mapping_errors_total", metrics.MappingErrors)
		writeMetric("go_goroutines", metrics.Goroutines)
		writeMetric("go_heap_alloc_bytes", metrics.HeapAllocBytes)
		writeMetric("go_heap_objects", metrics.HeapObjects)
		return c.String(http.StatusOK, output.String())
	})
	e.GET("/api/v1/devices", func(c echo.Context) error {
		items, err := devices.List(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to list devices").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/logical-devices", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": server.logicalDevices.List()})
	})
	e.GET("/api/v1/logical-devices/candidates", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		items, err := server.logicalDevices.Candidates(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical device candidates are unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.POST("/api/v1/logical-devices", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		var item logicaldevice.Config
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid logical device")
		}
		if err := server.logicalDevices.Save(c.Request().Context(), item); err != nil {
			return logicalDeviceHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": item})
	})
	e.GET("/api/v1/logical-devices/:id", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		for _, item := range server.logicalDevices.List() {
			if item.ID == c.Param("id") {
				return c.JSON(http.StatusOK, map[string]any{"data": item})
			}
		}
		return echo.NewHTTPError(http.StatusNotFound, "logical device not found")
	})
	e.GET("/api/v1/logical-devices/:id/explanations", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		items, err := server.logicalDevices.Explanations(c.Param("id"))
		if err != nil {
			return logicalDeviceHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.PUT("/api/v1/logical-devices/:id", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		var item logicaldevice.Config
		if err := c.Bind(&item); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid logical device")
		}
		if item.ID != "" && item.ID != c.Param("id") {
			return echo.NewHTTPError(http.StatusBadRequest, "logical device id cannot be changed")
		}
		item.ID = c.Param("id")
		if err := server.logicalDevices.Save(c.Request().Context(), item); err != nil {
			return logicalDeviceHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.DELETE("/api/v1/logical-devices/:id", func(c echo.Context) error {
		if server.logicalDevices == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "logical devices are unavailable")
		}
		if err := server.logicalDevices.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return logicalDeviceHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/api/v1/locations", func(c echo.Context) error {
		homes, err := devices.ListDeviceLocationHomes(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "device location catalog is unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": homes})
	})
	e.POST("/api/v1/locations/homes", func(c echo.Context) error {
		var input deviceLocationNameRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid home")
		}
		home, err := devices.SaveDeviceLocationHome(c.Request().Context(), "", input.Name)
		if err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": home})
	})
	e.PUT("/api/v1/locations/homes/:id", func(c echo.Context) error {
		var input deviceLocationNameRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid home")
		}
		home, err := devices.SaveDeviceLocationHome(c.Request().Context(), c.Param("id"), input.Name)
		if err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": home})
	})
	e.DELETE("/api/v1/locations/homes/:id", func(c echo.Context) error {
		if err := devices.DeleteDeviceLocationHome(c.Request().Context(), c.Param("id")); err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.POST("/api/v1/locations/homes/:homeId/rooms", func(c echo.Context) error {
		var input deviceLocationNameRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid room")
		}
		room, err := devices.SaveDeviceLocationRoom(c.Request().Context(), "", c.Param("homeId"), input.Name)
		if err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": room})
	})
	e.PUT("/api/v1/locations/homes/:homeId/rooms/:id", func(c echo.Context) error {
		var input deviceLocationNameRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid room")
		}
		room, err := devices.SaveDeviceLocationRoom(c.Request().Context(), c.Param("id"), c.Param("homeId"), input.Name)
		if err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": room})
	})
	e.DELETE("/api/v1/locations/homes/:homeId/rooms/:id", func(c echo.Context) error {
		if err := devices.DeleteDeviceLocationRoom(c.Request().Context(), c.Param("homeId"), c.Param("id")); err != nil {
			return deviceLocationHTTPError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.PUT("/api/v1/devices/:id/enabled", func(c echo.Context) error {
		var input deviceEnabledRequest
		if err := c.Bind(&input); err != nil || input.Enabled == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "enabled is required")
		}
		item, err := devices.SetDeviceEnabled(c.Request().Context(), c.Param("id"), *input.Enabled)
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "device preferences are unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.PUT("/api/v1/devices/:id/name", func(c echo.Context) error {
		var input deviceNameRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid device name")
		}
		item, err := devices.SetDeviceName(c.Request().Context(), c.Param("id"), input.Name)
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		var validationError *application.ValidationError
		if errors.As(err, &validationError) {
			return validationError
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "device name preferences are unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.DELETE("/api/v1/devices/:id/name", func(c echo.Context) error {
		item, err := devices.ResetDeviceName(c.Request().Context(), c.Param("id"))
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "device name preferences are unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.GET("/api/v1/devices/:id/mcp-config", func(c echo.Context) error {
		if server.mcpConfigs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "MCP configuration is unavailable")
		}
		config, err := server.mcpConfigs.Device(c.Request().Context(), c.Param("id"))
		if errors.Is(err, application.ErrMCPDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to get MCP configuration").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": config})
	})
	e.PUT("/api/v1/devices/:id/mcp-config", func(c echo.Context) error {
		if server.mcpConfigs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "MCP configuration is unavailable")
		}
		var config domainmcp.DeviceConfig
		if err := c.Bind(&config); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid MCP device configuration")
		}
		if config.DeviceID != "" && config.DeviceID != c.Param("id") {
			return echo.NewHTTPError(http.StatusBadRequest, "device id cannot be changed")
		}
		config.DeviceID = c.Param("id")
		saved, err := server.mcpConfigs.SaveDevice(c.Request().Context(), config)
		if errors.Is(err, application.ErrMCPDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, domainmcp.ErrInvalidConfig) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to save MCP configuration").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": saved})
	})
	e.GET("/api/v1/devices/:id/mcp-properties", func(c echo.Context) error {
		if server.mcpConfigs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "MCP configuration is unavailable")
		}
		configs, err := server.mcpConfigs.Properties(c.Request().Context(), c.Param("id"))
		if errors.Is(err, application.ErrMCPDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to list MCP property configurations").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": configs})
	})
	e.PUT("/api/v1/devices/:id/mcp-properties/:endpoint/:capability/:property", func(c echo.Context) error {
		if server.mcpConfigs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "MCP configuration is unavailable")
		}
		var config domainmcp.PropertyConfig
		if err := c.Bind(&config); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid MCP property configuration")
		}
		config.PropertyPath = domainmcp.PropertyPath{DeviceID: c.Param("id"), EndpointID: c.Param("endpoint"), CapabilityID: c.Param("capability"), PropertyID: c.Param("property")}
		saved, err := server.mcpConfigs.SaveProperty(c.Request().Context(), config)
		if errors.Is(err, application.ErrMCPPropertyNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device property not found")
		}
		if errors.Is(err, domainmcp.ErrInvalidConfig) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to save MCP property configuration").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": saved})
	})
	e.DELETE("/api/v1/devices/:id/mcp-properties/:endpoint/:capability/:property", func(c echo.Context) error {
		if server.mcpConfigs == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "MCP configuration is unavailable")
		}
		path := domainmcp.PropertyPath{DeviceID: c.Param("id"), EndpointID: c.Param("endpoint"), CapabilityID: c.Param("capability"), PropertyID: c.Param("property")}
		if err := server.mcpConfigs.DeleteProperty(c.Request().Context(), path); err != nil {
			if errors.Is(err, application.ErrMCPPropertyNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "device property not found")
			}
			if errors.Is(err, domainmcp.ErrInvalidConfig) {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to clear MCP property configuration").SetInternal(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.PUT("/api/v1/devices/:id/location", func(c echo.Context) error {
		var input deviceLocationRequest
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid device location")
		}
		item, err := devices.SetDeviceLocation(c.Request().Context(), c.Param("id"), application.DeviceLocationInput{
			Mode: input.Mode, HomeID: input.HomeID, RoomID: input.RoomID,
		})
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		var validationError *application.ValidationError
		if errors.As(err, &validationError) {
			return validationError
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "device location preferences are unavailable").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	var providers *application.ProviderService
	if len(providerServices) > 0 {
		providers = providerServices[0]
	}
	e.GET("/api/v1/providers", func(c echo.Context) error {
		if providers != nil {
			return c.JSON(http.StatusOK, map[string]any{"data": providers.List()})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": devices.ProviderInfos()})
	})
	getProviderAuthChallenge := func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		challenge, ok := providers.GetAuthChallenge(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusConflict, "Xiaomi provider authentication challenge is missing or expired; start login again")
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": challenge})
	}
	postProviderAuthChallenge := func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		var request struct {
			ChallengeID string `json:"challengeId"`
			Code        string `json:"code"`
		}
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Xiaomi provider verification request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
		defer cancel()
		info, err := providers.VerifyAuthChallenge(ctx, c.Param("id"), request.ChallengeID, request.Code)
		if err != nil {
			message := err.Error()
			status := http.StatusBadRequest
			if strings.Contains(strings.ToLower(message), "missing or expired") || strings.Contains(strings.ToLower(message), "too many") {
				status = http.StatusConflict
			}
			return echo.NewHTTPError(status, message)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	}
	e.GET("/api/v1/providers/:id/auth-challenge", getProviderAuthChallenge)
	e.POST("/api/v1/providers/:id/auth-challenge", postProviderAuthChallenge)
	e.POST("/api/v1/providers/:id/auth-challenge/verify", postProviderAuthChallenge)
	// Keep the Xiaomi-scoped aliases for clients that group Provider actions
	// under the third-party cloud API namespace.
	e.GET("/api/v1/xiaomi-miot-cloud/providers/:id/auth-challenge", getProviderAuthChallenge)
	e.POST("/api/v1/xiaomi-miot-cloud/providers/:id/auth-challenge", postProviderAuthChallenge)
	e.POST("/api/v1/xiaomi-miot-cloud/providers/:id/auth-challenge/verify", postProviderAuthChallenge)
	e.GET("/api/v1/events", func(c echo.Context) error {
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-store")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		type streamEvent struct {
			name    string
			payload any
		}
		events := make(chan streamEvent, 128)
		enqueue := func(name string, payload any) bool {
			select {
			case events <- streamEvent{name: name, payload: payload}:
				return true
			default:
				return false
			}
		}
		unsubscribeDevice := devices.Subscribe(func(item device.Device) { enqueue("device", item) })
		defer unsubscribeDevice()
		unsubscribeDeviceEvent := devices.SubscribeDeviceEvents(func(item providersdk.DeviceEvent) { enqueue("device-event", item) })
		defer unsubscribeDeviceEvent()
		unsubscribeState := devices.SubscribeStates(func(item domainstate.StateValue) { enqueue("state", item) })
		defer unsubscribeState()
		unsubscribeCommand := devices.SubscribeCommands(func(item domaincommand.Command) { enqueue("command", item) })
		defer unsubscribeCommand()
		unsubscribeTarget := targets.Subscribe(func(item application.TargetInfo) { enqueue("target", item) })
		defer unsubscribeTarget()
		unsubscribeAudit := func() {}
		if server.audit != nil {
			unsubscribeAudit = server.audit.Subscribe(func(item domainaudit.Event) { enqueue("audit", item) })
		}
		defer unsubscribeAudit()
		var logEvents <-chan subprocesslog.Entry
		unsubscribeLogs := func() {}
		if server.subprocessLogs != nil {
			logEvents, unsubscribeLogs = server.subprocessLogs.Subscribe()
		}
		defer unsubscribeLogs()

		providerSnapshot := func() any {
			var providerSnapshot any = devices.ProviderInfos()
			if providers != nil {
				providerSnapshot = providers.List()
			}
			return providerSnapshot
		}
		previousProviders, _ := json.Marshal(providerSnapshot())
		previousDiagnostics, _ := json.Marshal(devices.Metrics())
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		// Browser EventSource reconnects with Last-Event-ID. Replaying only the
		// most recent bounded slice keeps this sensitive diagnostics stream from
		// becoming an unbounded catch-up channel. A subscriber is registered
		// before the snapshot; duplicate sequences are harmless to clients and
		// avoid a gap between snapshot and live delivery.
		logCursor := runtimeLogCursor(c.Request())
		lastObservedLogSequence := logCursor
		lastDeliveredLogSequence := logCursor
		if logEvents != nil {
			replayedLogs := server.subprocessLogs.Snapshot(logCursor, runtimeLogReplayLimit)
			if len(replayedLogs) > 0 && replayedLogs[0].Sequence > logCursor+1 {
				if err := writeSSEEvent(response, "runtime-log-gap", runtimeLogGap{After: logCursor, Before: replayedLogs[0].Sequence}, 0); err != nil {
					return nil
				}
			}
			for _, entry := range replayedLogs {
				if err := writeSSEEvent(response, "runtime-log", entry, entry.Sequence); err != nil {
					return nil
				}
				if entry.Sequence > lastObservedLogSequence {
					lastObservedLogSequence = entry.Sequence
				}
				if entry.Sequence > lastDeliveredLogSequence {
					lastDeliveredLogSequence = entry.Sequence
				}
			}
			flusher.Flush()
		}
		runtimeTicker := time.NewTicker(5 * time.Second)
		defer runtimeTicker.Stop()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		logWindowStarted := time.Now()
		logEventsSent := 0
		logGapSent := false
		for {
			select {
			case event := <-events:
				payload, err := json.Marshal(event.payload)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.name, payload); err != nil {
					return nil
				}
				flusher.Flush()
			case entry, open := <-logEvents:
				if !open {
					logEvents = nil
					continue
				}
				now := time.Now()
				if now.Sub(logWindowStarted) >= time.Second {
					logWindowStarted, logEventsSent, logGapSent = now, 0, false
				}
				// The subscription is deliberately bounded. If its producer had to
				// skip entries, emit one control event and let the browser recover
				// with its cursor instead of removing the SSE backpressure bound.
				if entry.Sequence <= lastObservedLogSequence {
					continue
				}
				if lastObservedLogSequence > 0 && entry.Sequence > lastObservedLogSequence+1 {
					if !logGapSent {
						if err := writeSSEEvent(response, "runtime-log-gap", runtimeLogGap{After: lastObservedLogSequence, Before: entry.Sequence}, 0); err != nil {
							return nil
						}
						flusher.Flush()
						logGapSent = true
					}
				}
				lastObservedLogSequence = entry.Sequence
				if logEventsSent >= runtimeLogEventsPerSec {
					if !logGapSent {
						if err := writeSSEEvent(response, "runtime-log-gap", runtimeLogGap{After: lastDeliveredLogSequence, Before: entry.Sequence}, 0); err != nil {
							return nil
						}
						flusher.Flush()
						logGapSent = true
					}
					continue
				}
				logEventsSent++
				if err := writeSSEEvent(response, "runtime-log", entry, entry.Sequence); err != nil {
					return nil
				}
				lastDeliveredLogSequence = entry.Sequence
				flusher.Flush()
			case <-runtimeTicker.C:
				currentProviders := providerSnapshot()
				currentDiagnostics := devices.Metrics()
				delta, encodedProviders, encodedDiagnostics := runtimeChanges(previousProviders, previousDiagnostics, currentProviders, currentDiagnostics)
				if len(delta) > 0 && enqueue("runtime", delta) {
					if _, changed := delta["providers"]; changed {
						previousProviders = encodedProviders
					}
					if _, changed := delta["diagnostics"]; changed {
						previousDiagnostics = encodedDiagnostics
					}
				}
			case <-heartbeat.C:
				if _, err := response.Write([]byte(": keepalive\n\n")); err != nil {
					return nil
				}
				flusher.Flush()
			case <-c.Request().Context().Done():
				return nil
			}
		}
	})
	saveProvider := func(c echo.Context, id string) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		var request providerconfig.Config
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid provider configuration")
		}
		if id != "" {
			request.ID = id
		}
		info, err := providers.Save(c.Request().Context(), request)
		if err != nil {
			var validationError *application.ValidationError
			if errors.As(err, &validationError) {
				return validationError
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	}
	e.POST("/api/v1/providers", func(c echo.Context) error { return saveProvider(c, "") })
	e.PUT("/api/v1/providers/:id", func(c echo.Context) error { return saveProvider(c, c.Param("id")) })
	e.POST("/api/v1/providers/test", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		var request providerconfig.Config
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid provider configuration")
		}
		if err := providers.TestConnection(c.Request().Context(), request); err != nil {
			var validationError *application.ValidationError
			if errors.As(err, &validationError) {
				return validationError
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]bool{"reachable": true}})
	})
	e.POST("/api/v1/providers/scan", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		var request providerconfig.Config
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid provider configuration")
		}
		items, err := providers.Scan(c.Request().Context(), request)
		if err != nil {
			var validationError *application.ValidationError
			if errors.As(err, &validationError) {
				return validationError
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.POST("/api/v1/xiaomi/oauth/start", func(c echo.Context) error {
		var request xiaomi.OAuthStartRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Xiaomi OAuth request")
		}
		result, err := xiaomi.StartOAuth(request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/xiaomi/oauth/complete", func(c echo.Context) error {
		var request xiaomi.OAuthCompleteRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Xiaomi OAuth callback")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
		defer cancel()
		result, err := xiaomi.CompleteOAuth(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/tuya/oauth/start", func(c echo.Context) error {
		var request tuya.OAuthStartRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Tuya OAuth start request")
		}
		result, err := server.tuyaOAuth.Start(request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/tuya/oauth/complete", func(c echo.Context) error {
		var request tuya.OAuthCompleteRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Tuya OAuth callback")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
		defer cancel()
		result, err := server.tuyaOAuth.Complete(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.GET("/api/v1/tuya/oauth/qr", func(c echo.Context) error {
		authorizationURL, ok := server.tuyaOAuth.AuthorizationURL(c.QueryParam("state"))
		if !ok {
			return echo.NewHTTPError(http.StatusGone, "Tuya OAuth state is missing or expired; start login again")
		}
		image, err := qrcode.Encode(authorizationURL, qrcode.Medium, 320)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate Tuya OAuth QR code").SetInternal(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.Blob(http.StatusOK, "image/png", image)
	})
	e.GET("/api/v1/tuya/oauth/callback", func(c echo.Context) error {
		code, state, oauthError := c.QueryParam("code"), c.QueryParam("state"), c.QueryParam("error")
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.HTML(http.StatusOK, tuyaOAuthCallbackPage(code, state, oauthError))
	})
	e.POST("/api/v1/tuya/login/start", func(c echo.Context) error {
		var request tuya.SharingLoginStartRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Tuya QR login request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		result, err := server.tuyaSharingLogin.Start(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/tuya/login/poll", func(c echo.Context) error {
		var request tuya.SharingLoginPollRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Tuya QR login poll request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		result, err := server.tuyaSharingLogin.Poll(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/sonoff/login", func(c echo.Context) error {
		var request sonoffLoginRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Sonoff/eWeLink login request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		result, err := sonoffcloud.Login(ctx, http.DefaultClient, sonoffcloud.LoginCredentials{
			Username: request.Username, Password: request.Password, CountryCode: request.CountryCode,
			Region: request.Region, Endpoint: request.Endpoint, AppID: request.AppID, AppSecret: request.AppSecret,
		}, 30*time.Second)
		if err != nil {
			return sonoffLoginHTTPError(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.GET("/api/v1/tuya/login/qr", func(c echo.Context) error {
		qrData, ok := server.tuyaSharingLogin.QRData(c.QueryParam("state"))
		if !ok {
			return echo.NewHTTPError(http.StatusGone, "Tuya QR login session is missing or expired; start again")
		}
		image, err := qrcode.Encode(qrData, qrcode.Medium, 320)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate Tuya QR login code").SetInternal(err)
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.Blob(http.StatusOK, "image/png", image)
	})
	e.POST("/api/v1/xiaomi-miot-cloud/login/start", func(c echo.Context) error {
		var request xiaomi.CloudLoginStartRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Xiaomi MIoT cloud login request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
		defer cancel()
		result, err := server.cloudLogins.Start(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/xiaomi-miot-cloud/login/verify", func(c echo.Context) error {
		var request xiaomi.CloudLoginVerifyRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Xiaomi MIoT cloud verification request")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
		defer cancel()
		result, err := server.cloudLogins.Verify(ctx, request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.GET("/api/v1/xiaomi/gateways", func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()
		gateways, err := xiaomi.DiscoverGateways(ctx)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": gateways})
	})
	e.GET("/api/v1/xiaomi/providers/:id/devices", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		instance, ok := providers.RuntimeProvider(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusConflict, "Xiaomi provider must be enabled and connected before discovering subdevices")
		}
		live, ok := instance.(*xiaomi.Provider)
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "provider is not a Xiaomi central hub")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		items, err := live.DiscoverHubDevices(ctx)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/xiaomi-miot-cloud/providers/:id/devices", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		instance, ok := providers.RuntimeProvider(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusConflict, "Xiaomi MIoT cloud provider must be enabled and connected before discovering devices")
		}
		live, ok := instance.(*xiaomi.CloudProvider)
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "provider is not a Xiaomi MIoT third-party cloud provider")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		items, err := live.DiscoverCloudDevices(ctx)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/sonoff/providers/:id/devices", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		instance, ok := providers.RuntimeProvider(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusConflict, "Sonoff provider must be enabled and connected before discovering devices")
		}
		live, ok := instance.(*sonoff.Provider)
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "provider is not a Sonoff/eWeLink provider")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		items, err := live.DiscoverCloudDevices(ctx)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/tuya/providers/:id/devices", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		instance, ok := providers.RuntimeProvider(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusConflict, "Tuya provider must be enabled and connected before discovering devices")
		}
		live, ok := instance.(*tuya.Provider)
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "provider is not a Tuya provider")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		items, err := live.DiscoverCloudDevices(ctx)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.DELETE("/api/v1/providers/:id", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		if err := providers.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.POST("/api/v1/providers/:id/credentials/revoke", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		var request struct {
			Confirmation string `json:"confirmation"`
		}
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid credential revocation request")
		}
		id := c.Param("id")
		if strings.TrimSpace(request.Confirmation) != "REVOKE "+id {
			return echo.NewHTTPError(http.StatusBadRequest, "credential revocation confirmation does not match this provider")
		}
		result, err := providers.RevokeXiaomiCredentials(c.Request().Context(), id)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	})
	e.POST("/api/v1/providers/:id/restart", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		info, err := providers.Restart(c.Request().Context(), c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.GET("/api/v1/devices/:id/states", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.States(c.Param("id"))})
	})
	e.PATCH("/api/v1/devices/:id/simulation", func(c echo.Context) error {
		var input simulationRequest
		if err := c.Bind(&input); err != nil || (input.Availability == nil && input.Online == nil && input.Power == nil && input.Temperature == nil && input.Humidity == nil && input.Contact == nil && input.Motion == nil && input.Active == nil && input.Speed == nil && input.Mode == nil && input.FilterLife == nil && input.FilterChange == nil && input.Position == nil && input.Sequence == nil && input.Repeat == 0) {
			return echo.NewHTTPError(http.StatusBadRequest, "at least one simulation value is required")
		}
		request := providersdk.SimulationRequest{DeviceID: c.Param("id"), Online: input.Online, Availability: input.Availability, Sequence: input.Sequence, Repeat: input.Repeat}
		var simulatedType device.Type
		if input.Temperature != nil || input.Humidity != nil || input.Active != nil || input.Speed != nil || input.Mode != nil || input.FilterLife != nil || input.FilterChange != nil || input.Position != nil {
			items, _ := devices.List(c.Request().Context())
			for _, item := range items {
				if item.ID == request.DeviceID {
					simulatedType = item.Type
					break
				}
			}
		}
		if input.Power != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(*input.Power)})
		}
		if input.Temperature != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(*input.Temperature)})
		}
		if input.Humidity != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity", Value: device.NumberValue(*input.Humidity)})
		}
		if input.Contact != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "contact", PropertyID: "contact-detected", Value: device.BoolValue(*input.Contact)})
		}
		if input.Motion != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "motion", PropertyID: "motion-detected", Value: device.BoolValue(*input.Motion)})
		}
		advancedCapability := "fan"
		if simulatedType == device.TypeAirPurifier {
			advancedCapability = "air-purifier"
		}
		if input.Active != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: advancedCapability, PropertyID: "active", Value: device.BoolValue(*input.Active)})
		}
		if input.Speed != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: advancedCapability, PropertyID: "rotation-speed", Value: device.NumberValue(*input.Speed)})
		}
		if input.Mode != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: advancedCapability, PropertyID: "target-state", Value: device.EnumValue(*input.Mode)})
		}
		if input.FilterLife != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "filter", PropertyID: "life-level", Value: device.NumberValue(*input.FilterLife)})
		}
		if input.FilterChange != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "filter", PropertyID: "change-indication", Value: device.BoolValue(*input.FilterChange)})
		}
		if input.Position != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "window-covering", PropertyID: "current-position", Value: device.IntValue(*input.Position)})
		}
		item, err := devices.Simulate(c.Request().Context(), request)
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "simulation is unsupported")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "simulation failed").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item})
	})
	e.GET("/api/v1/targets", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": targets.List()})
	})
	e.GET("/api/v1/targets/:id/pairing-qr", func(c echo.Context) error {
		png, err := targets.QR(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "pairing QR code not found")
		}
		return c.Blob(http.StatusOK, "image/png", png)
	})
	e.GET("/api/v1/targets/:id/commissioning-qr", func(c echo.Context) error {
		png, err := targets.MatterQR(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "Matter commissioning QR code not found")
		}
		return c.Blob(http.StatusOK, "image/png", png)
	})
	saveTarget := func(c echo.Context, id string) error {
		var request targetRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid target configuration")
		}
		info, err := targets.Save(c.Request().Context(), request.domain(id))
		if err != nil {
			var validationError *application.ValidationError
			if errors.As(err, &validationError) {
				return validationError
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	}
	e.POST("/api/v1/targets", func(c echo.Context) error { return saveTarget(c, "") })
	e.PUT("/api/v1/targets/:id", func(c echo.Context) error { return saveTarget(c, c.Param("id")) })
	e.POST("/api/v1/targets/:id/pairing/regenerate", func(c echo.Context) error {
		id := c.Param("id")
		var input confirmationRequest
		if err := c.Bind(&input); err != nil || input.Confirmation != "REGENERATE "+id {
			return application.NewValidationError("pairing regeneration confirmation required", map[string]string{"confirmation": "type REGENERATE " + id + " to confirm"})
		}
		info, err := targets.RegeneratePairing(c.Request().Context(), id)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.DELETE("/api/v1/targets/:id/pairing-identity", func(c echo.Context) error {
		id := c.Param("id")
		var input confirmationRequest
		if err := c.Bind(&input); err != nil || input.Confirmation != "CLEAR "+id {
			return application.NewValidationError("pairing identity confirmation required", map[string]string{"confirmation": "type CLEAR " + id + " to confirm"})
		}
		info, err := targets.ClearPairingIdentity(c.Request().Context(), id)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.POST("/api/v1/targets/:id/commissioning-window", func(c echo.Context) error {
		var input struct {
			DurationSeconds uint32 `json:"durationSeconds"`
			Confirmation    string `json:"confirmation"`
		}
		id := c.Param("id")
		expected := "OPEN COMMISSIONING " + id
		if err := c.Bind(&input); err != nil || input.Confirmation != expected {
			return application.NewValidationError("Matter commissioning window confirmation required", map[string]string{"confirmation": "type " + expected + " to confirm"})
		}
		info, err := targets.OpenMatterCommissioningWindow(c.Request().Context(), id, input.DurationSeconds)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.POST("/api/v1/targets/:id/endpoints/:consumerDeviceId/device-type", func(c echo.Context) error {
		id, consumerDeviceID := c.Param("id"), c.Param("consumerDeviceId")
		var input struct {
			DeviceType   device.Type `json:"deviceType"`
			Confirmation string      `json:"confirmation"`
		}
		if err := c.Bind(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid Matter endpoint device type request")
		}
		expected := "CHANGE ENDPOINT TYPE " + id + " " + consumerDeviceID + " " + string(input.DeviceType)
		if input.Confirmation != expected {
			return application.NewValidationError("Matter endpoint device type confirmation required", map[string]string{"confirmation": "type " + expected + " to confirm"})
		}
		info, err := targets.ConfirmMatterEndpointDeviceType(c.Request().Context(), id, consumerDeviceID, input.DeviceType)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.DELETE("/api/v1/targets/:id/commissioning-window", func(c echo.Context) error {
		info, err := targets.CloseMatterCommissioningWindow(c.Request().Context(), c.Param("id"))
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.DELETE("/api/v1/targets/:id/fabrics/:fabricId", func(c echo.Context) error {
		id, fabricID := c.Param("id"), c.Param("fabricId")
		var input confirmationRequest
		expected := "DELETE FABRIC " + id + " " + fabricID
		if err := c.Bind(&input); err != nil || input.Confirmation != expected {
			return application.NewValidationError("Matter Fabric deletion confirmation required", map[string]string{"confirmation": "type " + expected + " to confirm"})
		}
		info, err := targets.RemoveMatterFabric(c.Request().Context(), id, fabricID)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.POST("/api/v1/targets/:id/factory-reset", func(c echo.Context) error {
		id := c.Param("id")
		var input confirmationRequest
		expected := "FACTORY RESET " + id
		if err := c.Bind(&input); err != nil || input.Confirmation != expected {
			return application.NewValidationError("Matter factory reset confirmation required", map[string]string{"confirmation": "type " + expected + " to confirm"})
		}
		info, err := targets.FactoryResetMatter(c.Request().Context(), id)
		if errors.Is(err, application.ErrTargetNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "target not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"data": info})
	})
	e.DELETE("/api/v1/targets/:id", func(c echo.Context) error {
		if err := targets.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.NoContent(http.StatusNoContent)
	})
	e.PUT("/api/v1/devices/:id/properties/power", func(c echo.Context) error {
		var request powerRequest
		if err := c.Bind(&request); err != nil || request.Value == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "value must be a boolean")
		}
		item, command, err := devices.ExecutePower(c.Request().Context(), c.Param("id"), *request.Value)
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, application.ErrDeviceDisabled) {
			return echo.NewHTTPError(http.StatusConflict, "device is disabled or removed")
		}
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "power is not supported")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
		}
		if errors.Is(err, application.ErrCommandSuperseded) {
			return echo.NewHTTPError(http.StatusConflict, "property write was superseded by a newer value")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return echo.NewHTTPError(http.StatusRequestTimeout, "power write canceled")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to update device").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item, "command": command})
	})
	e.PUT("/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/properties/:property", func(c echo.Context) error {
		var value device.PropertyValue
		if err := c.Bind(&value); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid property value")
		}
		item, command, err := devices.ExecuteProperty(
			c.Request().Context(), c.Param("id"), c.Param("endpoint"),
			c.Param("capability"), c.Param("property"), value,
		)
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, application.ErrDeviceDisabled) {
			return echo.NewHTTPError(http.StatusConflict, "device is disabled or removed")
		}
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "property is not supported")
		}
		if errors.Is(err, providersdk.ErrPropertyInvalid) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid property value")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
		}
		if errors.Is(err, application.ErrCommandSuperseded) {
			return echo.NewHTTPError(http.StatusConflict, "property write was superseded by a newer value")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return echo.NewHTTPError(http.StatusRequestTimeout, "property write canceled")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to update device").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item, "command": command})
	})
	e.GET("/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/properties/:property", func(c echo.Context) error {
		property, err := devices.ReadProperty(c.Request().Context(), c.Param("id"), c.Param("endpoint"), c.Param("capability"), c.Param("property"))
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, application.ErrDeviceDisabled) {
			return echo.NewHTTPError(http.StatusConflict, "device is disabled or removed")
		}
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "property is not supported")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return echo.NewHTTPError(http.StatusRequestTimeout, "property read canceled")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to read property").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": property})
	})
	e.POST("/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/commands/:command", func(c echo.Context) error {
		var body commandRequest
		if c.Request().ContentLength > 0 {
			if err := c.Bind(&body); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid command parameters")
			}
		}
		item, command, err := devices.ExecuteCommand(c.Request().Context(), providersdk.CommandRequest{DeviceID: c.Param("id"), EndpointID: c.Param("endpoint"), CapabilityID: c.Param("capability"), CommandID: c.Param("command"), Parameters: body.Parameters, IdempotencyKey: c.Request().Header.Get("Idempotency-Key")})
		if errors.Is(err, application.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		if errors.Is(err, application.ErrDeviceDisabled) {
			return echo.NewHTTPError(http.StatusConflict, "device is disabled or removed")
		}
		if errors.Is(err, providersdk.ErrCommandUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "command is not supported")
		}
		if errors.Is(err, providersdk.ErrCommandInvalid) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid command parameters")
		}
		if errors.Is(err, commandtracker.ErrIdempotencyConflict) {
			return echo.NewHTTPError(http.StatusConflict, "idempotency key was already used with different parameters")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return echo.NewHTTPError(http.StatusRequestTimeout, "command canceled")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to execute command").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": item, "command": command})
	})
	e.GET("/api/v1/commands", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.Commands()})
	})
	e.GET("/api/v1/commands/:id", func(c echo.Context) error {
		command, ok := devices.Command(c.Param("id"))
		if !ok {
			return echo.NewHTTPError(http.StatusNotFound, "command not found")
		}
		return c.JSON(http.StatusOK, map[string]any{"data": command})
	})
	if assets := webui.Assets(); assets != nil {
		ui := webui.NewHandler(assets)
		serveUI := func(c echo.Context) error {
			if !webui.IsApplicationPath(c.Request().URL.Path) {
				return echo.ErrNotFound
			}
			ui.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		e.GET("/", serveUI)
		e.HEAD("/", serveUI)
		e.GET("/*", serveUI)
		e.HEAD("/*", serveUI)
	}

	return server
}

func (s *Server) SetSettingsService(settings *application.SettingsService) { s.settings = settings }

func (s *Server) SetSubprocessLogs(logs *subprocesslog.Store) { s.subprocessLogs = logs }

func (s *Server) SetAuditService(audit *application.AuditService) { s.audit = audit }

func (s *Server) SetExportService(exports *application.ExportService) { s.exports = exports }

func (s *Server) SetProfileService(profiles *application.ProfileService) { s.profiles = profiles }

func (s *Server) SetLogicalDeviceService(logicalDevices *application.LogicalDeviceService) {
	s.logicalDevices = logicalDevices
}

func (s *Server) SetAuthService(auth *application.AuthService) { s.auth = auth }

func (s *Server) SetMaintenanceService(maintenance *application.MaintenanceService) {
	s.maintenance = maintenance
}

func (s *Server) SetMediaService(media *application.MediaService) { s.media = media }

func (s *Server) SetMCPConfigService(configs *application.MCPConfigService) { s.mcpConfigs = configs }

func (s *Server) SetAIService(service aiService) { s.aiService = service }

func (s *Server) SetAIAutomationService(service *application.AIAutomationService) {
	s.aiAutomations = service
}

func (s *Server) SetMediaPreview(runtimeDir string) {
	root, err := filepath.Abs(runtimeDir)
	if err != nil || root == filepath.Dir(root) {
		return
	}
	s.mediaRuntimeDir = root
	dialer := &net.Dialer{}
	s.mediaPreview = &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			streamID, _, err := net.SplitHostPort(address)
			if err != nil || !device.ValidStableID(streamID) {
				return nil, errors.New("invalid camera preview stream address")
			}
			socket := filepath.Join(root, streamID, "media.sock")
			if filepath.Dir(filepath.Dir(socket)) != root {
				return nil, errors.New("unsafe camera preview socket path")
			}
			return dialer.DialContext(ctx, "unix", socket)
		},
	}}
	s.mediaPreviewStartupTimeout = 10 * time.Second
}

var errMediaPreviewStartupTimeout = errors.New("camera preview did not produce a media fragment")

func readMediaPreviewStartup(ctx context.Context, body io.ReadCloser, timeout time.Duration, limit int) ([]byte, error) {
	type result struct {
		payload []byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		buffer := bytes.NewBuffer(make([]byte, 0, 64<<10))
		chunk := make([]byte, 32<<10)
		for buffer.Len() < limit {
			remaining := limit - buffer.Len()
			if remaining < len(chunk) {
				chunk = chunk[:remaining]
			}
			count, err := body.Read(chunk)
			if count > 0 {
				_, _ = buffer.Write(chunk[:count])
				if bytes.Contains(buffer.Bytes(), []byte("moof")) {
					done <- result{payload: buffer.Bytes()}
					return
				}
			}
			if err != nil {
				done <- result{err: errMediaPreviewStartupTimeout}
				return
			}
		}
		done <- result{err: errMediaPreviewStartupTimeout}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		return outcome.payload, outcome.err
	case <-ctx.Done():
		_ = body.Close()
		return nil, ctx.Err()
	case <-timer.C:
		_ = body.Close()
		return nil, errMediaPreviewStartupTimeout
	}
}

func (s *Server) serveMediaPreview(c echo.Context) error {
	if s.media == nil || s.mediaPreview == nil || s.mediaRuntimeDir == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "camera preview is unavailable")
	}
	streams, err := s.media.List(c.Request().Context())
	if err != nil {
		return mediaHTTPError(err)
	}
	var selected *domainmedia.StreamSpec
	for index := range streams {
		if streams[index].DeviceID == c.Param("deviceId") {
			selected = &streams[index]
			break
		}
	}
	if selected == nil {
		return echo.NewHTTPError(http.StatusNotFound, "camera media stream not found")
	}
	// Device Center is a visual diagnostic surface. Keep its MP4 video-only:
	// Opus-in-MP4 support differs across browsers, and an incompatible audio
	// track can make an otherwise valid H.264 stream remain in HAVE_NOTHING.
	// HomeKit audio continues over its separate SRTP session.
	query := url.Values{"src": {selected.ID}, "video": {"h264"}}
	endpoint := "http://" + selected.ID + "/api/stream.mp4?" + query.Encode()
	request, err := http.NewRequestWithContext(c.Request().Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "camera preview request failed").SetInternal(err)
	}
	request.Header.Set("User-Agent", "HomeLoom-Media-Preview/1")
	response, err := s.mediaPreview.Do(request)
	if err != nil {
		if c.Request().Context().Err() == nil {
			s.reportMediaAvailability(selected.DeviceID, device.AvailabilityOffline)
		}
		return echo.NewHTTPError(http.StatusBadGateway, "camera publisher is unreachable").SetInternal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.reportMediaAvailability(selected.DeviceID, device.AvailabilityOffline)
		return echo.NewHTTPError(http.StatusBadGateway, "camera publisher rejected the preview stream")
	}
	contentType := response.Header.Get(echo.HeaderContentType)
	if !strings.HasPrefix(contentType, "video/mp4") {
		s.reportMediaAvailability(selected.DeviceID, device.AvailabilityOffline)
		return echo.NewHTTPError(http.StatusBadGateway, "camera publisher returned an invalid preview stream")
	}
	startup, err := readMediaPreviewStartup(c.Request().Context(), response.Body, s.mediaPreviewStartupTimeout, 4<<20)
	if err != nil {
		if c.Request().Context().Err() != nil {
			return c.Request().Context().Err()
		}
		s.reportMediaAvailability(selected.DeviceID, device.AvailabilityOffline)
		return echo.NewHTTPError(http.StatusGatewayTimeout, "camera preview timed out waiting for a keyframe").SetInternal(err)
	}
	s.reportMediaAvailability(selected.DeviceID, device.AvailabilityOnline)
	c.Response().Header().Set(echo.HeaderContentType, contentType)
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	c.Response().WriteHeader(http.StatusOK)
	if _, err := c.Response().Writer.Write(startup); err != nil {
		return err
	}
	if _, err := io.Copy(c.Response().Writer, response.Body); err != nil && c.Request().Context().Err() == nil {
		return err
	}
	return nil
}

func (s *Server) reportMediaAvailability(deviceID string, availability device.Availability) {
	if s.devices != nil {
		_, _ = s.devices.ReportDeviceAvailability(deviceID, availability)
	}
}

func (s *Server) SetTrustedProxies(values []string) error {
	ranges := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			ranges = append(ranges, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy %q", value)
		}
		ranges = append(ranges, network)
	}
	s.trustedProxies = ranges
	return nil
}

func requiresAuthentication(request *http.Request) bool {
	path := request.URL.Path
	if path == "/metrics" {
		return true
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	switch path {
	case "/api/v1/system/version", "/api/v1/auth/status", "/api/v1/auth/setup", "/api/v1/auth/login", "/api/v1/tuya/oauth/callback":
		return false
	default:
		return true
	}
}

func tuyaOAuthCallbackPage(code, state, oauthError string) string {
	payload, _ := json.Marshal(map[string]string{
		"type":  "homeloom-tuya-oauth",
		"code":  strings.TrimSpace(code),
		"state": strings.TrimSpace(state),
		"error": strings.TrimSpace(oauthError),
	})
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>HomeLoom · Tuya 授权</title><style>body{font:16px system-ui,sans-serif;max-width:560px;margin:15vh auto;padding:24px;color:#16202a}strong{display:block;margin-bottom:8px}small{color:#64748b}</style></head><body><strong>Tuya 授权结果已返回</strong><small>正在通知 HomeLoom 配置窗口；如果没有自动关闭，请返回原窗口继续。</small><script>const message=` + string(payload) + `;if(window.opener&&window.opener!==window){window.opener.postMessage(message,window.location.origin);window.setTimeout(()=>window.close(),250)}else{document.querySelector('small').textContent='请返回 HomeLoom 原窗口继续完成授权。'}</script></body></html>`
}

func directClientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if request.RemoteAddr != "" {
		return request.RemoteAddr
	}
	return "unknown"
}

func (s *Server) clientIP(request *http.Request) string {
	direct := directClientIP(request)
	directIP := net.ParseIP(direct)
	if directIP == nil || !s.isTrustedProxy(directIP) {
		return direct
	}
	forwarded := request.Header.Values(echo.HeaderXForwardedFor)
	if len(forwarded) == 0 {
		return direct
	}
	values := append(strings.Split(strings.Join(forwarded, ","), ","), direct)
	for index := len(values) - 1; index >= 0; index-- {
		value := strings.TrimSpace(strings.Trim(values[index], "[]"))
		ip := net.ParseIP(value)
		if ip == nil {
			return direct
		}
		if !s.isTrustedProxy(ip) {
			return ip.String()
		}
	}
	return strings.TrimSpace(values[0])
}

func (s *Server) isTrustedProxy(ip net.IP) bool {
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func authCookieValue(c echo.Context) string {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) setAuthCookies(c echo.Context, session application.AuthSession) {
	secure := s.secureCookieRequest(c.Request())
	maxAge := max(1, int(time.Until(session.ExpiresAt).Seconds()))
	c.SetCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token, Path: "/", MaxAge: maxAge, Expires: session.ExpiresAt, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	c.SetCookie(&http.Cookie{Name: csrfCookieName, Value: session.CSRFToken, Path: "/", MaxAge: maxAge, Expires: session.ExpiresAt, Secure: secure, SameSite: http.SameSiteStrictMode})
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
}

func (s *Server) clearAuthCookies(c echo.Context) {
	secure := s.secureCookieRequest(c.Request())
	expired := time.Unix(1, 0).UTC()
	c.SetCookie(&http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, Expires: expired, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	c.SetCookie(&http.Cookie{Name: csrfCookieName, Path: "/", MaxAge: -1, Expires: expired, Secure: secure, SameSite: http.SameSiteStrictMode})
}

func (s *Server) secureCookieRequest(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	directIP := net.ParseIP(directClientIP(request))
	if directIP == nil || !s.isTrustedProxy(directIP) {
		return false
	}
	protocols := strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")
	return len(protocols) > 0 && strings.EqualFold(strings.TrimSpace(protocols[len(protocols)-1]), "https")
}

func profileHTTPError(err error) error {
	if errors.Is(err, application.ErrProfileNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "mapping profile not found")
	}
	if errors.Is(err, application.ErrBindingNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "mapping binding not found")
	}
	if errors.Is(err, application.ErrCustomModelPropertyNotFound) || errors.Is(err, application.ErrCustomModelNotFound) || errors.Is(err, application.ErrModelEnumOverrideNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "custom model property not found")
	}
	if errors.Is(err, application.ErrProfileExists) || errors.Is(err, application.ErrProfileBuiltIn) || errors.Is(err, application.ErrProfileInUse) || errors.Is(err, application.ErrBindingExists) || errors.Is(err, application.ErrCustomModelPropertyExists) || errors.Is(err, application.ErrCustomModelExists) || errors.Is(err, application.ErrModelEnumOverrideExists) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "mapping profile operation failed").SetInternal(err)
}

func logicalDeviceHTTPError(err error) error {
	if errors.Is(err, application.ErrLogicalDeviceNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "logical device not found")
	}
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	return echo.NewHTTPError(http.StatusServiceUnavailable, "logical device configuration operation failed").SetInternal(err)
}

func mediaHTTPError(err error) error {
	switch {
	case errors.Is(err, application.ErrMediaStreamNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "media stream not found")
	case errors.Is(err, application.ErrMediaStreamExists):
		return echo.NewHTTPError(http.StatusConflict, "media stream already exists")
	case errors.Is(err, application.ErrMediaStreamStoreUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration is unavailable").SetInternal(err)
	}
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	return echo.NewHTTPError(http.StatusServiceUnavailable, "media configuration operation failed").SetInternal(err)
}

func writeJSONDownload(c echo.Context, filename string, value any) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", filename))
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.JSON(http.StatusOK, value)
}

func auditMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func auditResource(c echo.Context) (action, resourceType, resourceID string) {
	route := c.Path()
	if route == "" {
		route = c.Request().URL.Path
	}
	if strings.HasPrefix(route, "/api/v1/mapping/bindings") {
		resourceType, resourceID = "mapping-binding", c.Param("id")
		action = strings.ToLower(c.Request().Method)
		return action, resourceType, resourceID
	}
	if strings.HasPrefix(route, "/api/v1/mapping/profiles") {
		resourceType, resourceID = "mapping-profile", c.Param("id")
		action = strings.ToLower(c.Request().Method)
		if strings.HasSuffix(route, "/import") {
			action = "import"
		}
		return action, resourceType, resourceID
	}
	segments := strings.Split(strings.TrimPrefix(route, "/api/v1/"), "/")
	resourceType = strings.TrimSuffix(segments[0], "s")
	resourceID = c.Param("id")
	if resourceID == "" && len(segments) > 1 && !strings.HasPrefix(segments[1], ":") {
		resourceID = segments[1]
	}
	action = strings.ToLower(c.Request().Method)
	if len(segments) > 2 && !strings.HasPrefix(segments[len(segments)-1], ":") {
		action = segments[len(segments)-1]
	} else if resourceID == "" && len(segments) > 1 && !strings.HasPrefix(segments[1], ":") {
		action = segments[1]
	}
	return action, resourceType, resourceID
}

func (s *Server) Address() string { return s.address }

func (s *Server) Handler() http.Handler { return s.echo }

func (s *Server) Start() error {
	err := s.echo.Start(s.address)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error { return s.echo.Shutdown(ctx) }
