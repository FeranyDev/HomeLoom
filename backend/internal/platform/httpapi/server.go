package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Server struct {
	address  string
	echo     *echo.Echo
	settings *application.SettingsService
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

type simulationRequest struct {
	Availability *device.Availability `json:"availability"`
	Online       *bool                `json:"online"`
	Power        *bool                `json:"power"`
	Temperature  *float64             `json:"temperature"`
	Humidity     *float64             `json:"humidity"`
	Contact      *bool                `json:"contact"`
	Motion       *bool                `json:"motion"`
}

type commandRequest struct {
	Parameters map[string]device.PropertyValue `json:"parameters"`
}

type targetRequest struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Address   string   `json:"address"`
	Pin       string   `json:"pin"`
	SetupID   string   `json:"setupId"`
	StorePath string   `json:"storePath"`
	DeviceIDs []string `json:"deviceIds"`
}

func (r targetRequest) domain(id string) domaintarget.Config {
	if id == "" {
		id = r.ID
	}
	return domaintarget.Config{
		ID: id, Type: r.Type, Name: r.Name, Enabled: r.Enabled, Address: r.Address,
		Pin: r.Pin, SetupID: r.SetupID, StorePath: r.StorePath, DeviceIDs: r.DeviceIDs,
	}
}

func NewServer(address string, devices *application.DeviceService, targets *application.TargetService, logger *slog.Logger, providerServices ...*application.ProviderService) *Server {
	e := echo.New()
	server := &Server{address: address, echo: e}
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
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod:    true,
		LogStatus:    true,
		LogURI:       true,
		LogError:     true,
		LogRequestID: true,
		LogValuesFunc: func(_ echo.Context, values middleware.RequestLoggerValues) error {
			logger.Info("http request", "request_id", values.RequestID, "method", values.Method, "uri", values.URI, "status", values.Status, "error", values.Error)
			return nil
		},
	}))

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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
		writeMetric("commands_outcome_unknown_total", metrics.CommandsOutcomeUnknown)
		writeMetric("homekit_pushes_total", metrics.HomeKitPushes)
		writeMetric("devices_online", metrics.OnlineDevices)
		writeMetric("devices_offline", metrics.OfflineDevices)
		writeMetric("devices_unknown", metrics.UnknownDevices)
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
		if err := c.Bind(&input); err != nil || (input.Availability == nil && input.Online == nil && input.Power == nil && input.Temperature == nil && input.Humidity == nil && input.Contact == nil && input.Motion == nil) {
			return echo.NewHTTPError(http.StatusBadRequest, "at least one simulation value is required")
		}
		request := providersdk.SimulationRequest{DeviceID: c.Param("id"), Online: input.Online, Availability: input.Availability}
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
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "power is not supported")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
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
		if errors.Is(err, application.ErrPropertyUnsupported) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "property is not supported")
		}
		if errors.Is(err, providersdk.ErrProviderUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "provider unavailable")
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
