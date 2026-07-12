package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Server struct {
	address string
	echo    *echo.Echo
}

type powerRequest struct {
	Value *bool `json:"value"`
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

func NewServer(address string, devices *application.DeviceService, targets *application.TargetService, logger *slog.Logger) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod: true,
		LogStatus: true,
		LogURI:    true,
		LogError:  true,
		LogValuesFunc: func(_ echo.Context, values middleware.RequestLoggerValues) error {
			logger.Info("http request", "method", values.Method, "uri", values.URI, "status", values.Status, "error", values.Error)
			return nil
		},
	}))

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	e.GET("/ready", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
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
		writeMetric("states_marked_stale_total", metrics.StatesMarkedStale)
		writeMetric("commands_started_total", metrics.CommandsStarted)
		writeMetric("commands_confirmed_total", metrics.CommandsConfirmed)
		writeMetric("commands_rejected_total", metrics.CommandsRejected)
		writeMetric("commands_timed_out_total", metrics.CommandsTimedOut)
		return c.String(http.StatusOK, output.String())
	})
	e.GET("/api/v1/devices", func(c echo.Context) error {
		items, err := devices.List(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to list devices").SetInternal(err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	})
	e.GET("/api/v1/providers", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.ProviderInfos()})
	})
	e.GET("/api/v1/devices/:id/states", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": devices.States(c.Param("id"))})
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
	saveTarget := func(c echo.Context, id string) error {
		var request targetRequest
		if err := c.Bind(&request); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid target configuration")
		}
		info, err := targets.Save(c.Request().Context(), request.domain(id))
		if err != nil {
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
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to update device").SetInternal(err)
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

	return &Server{address: address, echo: e}
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
