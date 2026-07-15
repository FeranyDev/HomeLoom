package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Server struct {
	address        string
	echo           *echo.Echo
	settings       *application.SettingsService
	audit          *application.AuditService
	exports        *application.ExportService
	profiles       *application.ProfileService
	auth           *application.AuthService
	maintenance    *application.MaintenanceService
	logins         *loginLimiter
	trustedProxies []*net.IPNet
}

const apiVersionHeader = "HomeLoom-API-Version"

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

type confirmationRequest struct {
	Confirmation string `json:"confirmation"`
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

type simulationRequest struct {
	Availability *device.Availability `json:"availability"`
	Online       *bool                `json:"online"`
	Power        *bool                `json:"power"`
	Temperature  *float64             `json:"temperature"`
	Humidity     *float64             `json:"humidity"`
	Value        *float64             `json:"value"`
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
	ID        string                       `json:"id"`
	Type      string                       `json:"type"`
	Name      string                       `json:"name"`
	Enabled   bool                         `json:"enabled"`
	Address   string                       `json:"address"`
	Pin       string                       `json:"pin"`
	SetupID   string                       `json:"setupId"`
	StorePath string                       `json:"storePath"`
	DeviceIDs []string                     `json:"deviceIds"`
	Devices   []domaintarget.VirtualDevice `json:"devices"`
}

func (r targetRequest) domain(id string) domaintarget.Config {
	if id == "" {
		id = r.ID
	}
	return domaintarget.Config{
		ID: id, Type: r.Type, Name: r.Name, Enabled: r.Enabled, Address: r.Address,
		Pin: r.Pin, SetupID: r.SetupID, StorePath: r.StorePath, DeviceIDs: r.DeviceIDs, Devices: r.Devices,
	}
}

func NewServer(address string, devices *application.DeviceService, targets *application.TargetService, logger *slog.Logger, providerServices ...*application.ProviderService) *Server {
	e := echo.New()
	server := &Server{address: address, echo: e, logins: newLoginLimiter()}
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
			logger.Error("http error response failed", "request_id", requestID, "error", writeErr)
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
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod:    true,
		LogStatus:    true,
		LogURI:       true,
		LogError:     true,
		LogRequestID: true,
		LogRemoteIP:  true,
		LogValuesFunc: func(_ echo.Context, values middleware.RequestLoggerValues) error {
			logger.Info("http request", "request_id", values.RequestID, "remote_ip", values.RemoteIP, "method", values.Method, "uri", values.URI, "status", values.Status, "error", values.Error)
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
			}
			if _, auditErr := server.audit.Record(c.Request().Context(), event); auditErr != nil {
				logger.Error("audit event persistence failed", "request_id", event.CorrelationID, "method", event.Method, "route", event.Route, "error", auditErr)
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
	e.GET("/api/v1/diagnostics", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.Metrics()})
	})
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
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, (256<<20)+(1<<20))
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
	e.GET("/api/v1/events/audit", func(c echo.Context) error {
		if server.audit == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "audit log is unavailable")
		}
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		events := make(chan domainaudit.Event, 32)
		unsubscribe := server.audit.Subscribe(func(item domainaudit.Event) {
			select {
			case events <- item:
			default:
			}
		})
		defer unsubscribe()
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case item := <-events:
				payload, err := json.Marshal(item)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: audit\ndata: %s\n\n", payload); err != nil {
					return nil
				}
				flusher.Flush()
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
	e.GET("/api/v1/events/devices", func(c echo.Context) error {
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		events := make(chan device.Device, 16)
		unsubscribe := devices.Subscribe(func(item device.Device) {
			select {
			case events <- item:
			default:
			}
		})
		defer unsubscribe()
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case item := <-events:
				payload, err := json.Marshal(item)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: device\ndata: %s\n\n", payload); err != nil {
					return nil
				}
				flusher.Flush()
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
	e.GET("/api/v1/events/commands", func(c echo.Context) error {
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		events := make(chan domaincommand.Command, 32)
		unsubscribe := devices.SubscribeCommands(func(item domaincommand.Command) {
			select {
			case events <- item:
			default:
			}
		})
		defer unsubscribe()
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case item := <-events:
				payload, err := json.Marshal(item)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: command\ndata: %s\n\n", payload); err != nil {
					return nil
				}
				flusher.Flush()
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
	e.GET("/api/v1/events/states", func(c echo.Context) error {
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		deviceID := c.QueryParam("deviceId")
		events := make(chan domainstate.StateValue, 32)
		unsubscribe := devices.SubscribeStates(func(item domainstate.StateValue) {
			if deviceID != "" && item.Key.DeviceID != deviceID {
				return
			}
			select {
			case events <- item:
			default:
			}
		})
		defer unsubscribe()
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case item := <-events:
				payload, err := json.Marshal(item)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: state\ndata: %s\n\n", payload); err != nil {
					return nil
				}
				flusher.Flush()
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
	e.DELETE("/api/v1/providers/:id", func(c echo.Context) error {
		if providers == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider management is unavailable")
		}
		if err := providers.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.NoContent(http.StatusNoContent)
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
		if err := c.Bind(&input); err != nil || (input.Availability == nil && input.Online == nil && input.Power == nil && input.Temperature == nil && input.Humidity == nil && input.Value == nil && input.Contact == nil && input.Motion == nil && input.Active == nil && input.Speed == nil && input.Mode == nil && input.FilterLife == nil && input.FilterChange == nil && input.Position == nil && input.Sequence == nil && input.Repeat == 0) {
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
			capabilityID, propertyID := "temperature", "current-temperature"
			if simulatedType == device.TypeSinglePropertySensor {
				capabilityID, propertyID = "sensor", "value"
			}
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID, Value: device.NumberValue(*input.Temperature)})
		}
		if input.Humidity != nil {
			capabilityID, propertyID := "humidity", "current-humidity"
			if simulatedType == device.TypeSinglePropertySensor {
				capabilityID, propertyID = "sensor", "value"
			}
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID, Value: device.NumberValue(*input.Humidity)})
		}
		if input.Value != nil {
			request.Properties = append(request.Properties, providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "sensor", PropertyID: "value", Value: device.NumberValue(*input.Value)})
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
	e.GET("/api/v1/events/targets", func(c echo.Context) error {
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set(echo.HeaderCacheControl, "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming is unsupported")
		}
		events := make(chan application.TargetInfo, 16)
		unsubscribe := targets.Subscribe(func(item application.TargetInfo) {
			select {
			case events <- item:
			default:
			}
		})
		defer unsubscribe()
		if _, err := response.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case item := <-events:
				payload, err := json.Marshal(item)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(response, "event: target\ndata: %s\n\n", payload); err != nil {
					return nil
				}
				flusher.Flush()
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
	e.GET("/api/v1/targets/:id/pairing-qr", func(c echo.Context) error {
		png, err := targets.QR(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "pairing QR code not found")
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

	return server
}

func (s *Server) SetSettingsService(settings *application.SettingsService) { s.settings = settings }

func (s *Server) SetAuditService(audit *application.AuditService) { s.audit = audit }

func (s *Server) SetExportService(exports *application.ExportService) { s.exports = exports }

func (s *Server) SetProfileService(profiles *application.ProfileService) { s.profiles = profiles }

func (s *Server) SetAuthService(auth *application.AuthService) { s.auth = auth }

func (s *Server) SetMaintenanceService(maintenance *application.MaintenanceService) {
	s.maintenance = maintenance
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
	case "/api/v1/system/version", "/api/v1/auth/status", "/api/v1/auth/setup", "/api/v1/auth/login":
		return false
	default:
		return true
	}
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
	if errors.Is(err, application.ErrCustomModelPropertyNotFound) || errors.Is(err, application.ErrCustomModelNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "custom model property not found")
	}
	if errors.Is(err, application.ErrProfileExists) || errors.Is(err, application.ErrProfileBuiltIn) || errors.Is(err, application.ErrProfileInUse) || errors.Is(err, application.ErrBindingExists) || errors.Is(err, application.ErrCustomModelPropertyExists) || errors.Is(err, application.ErrCustomModelExists) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "mapping profile operation failed").SetInternal(err)
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
