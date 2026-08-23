package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"sort"
	"strconv"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/labstack/echo/v4"
)

const (
	auditRequestBodyKey    = "audit-request-body"
	auditPropertyBeforeKey = "audit-property-before"
	auditRequestBodyLimit  = 32 << 10
	auditDetailValueLimit  = 120
)

type auditPropertyBefore struct {
	value device.PropertyValue
	found bool
}

// captureAuditRequestBody keeps a small JSON request copy for the audit
// allow-list and restores the original stream before the endpoint binds it.
// Large uploads (including backup restores) and non-JSON data are not read.
func captureAuditRequestBody(c echo.Context) {
	request := c.Request()
	if request.Body == nil || request.ContentLength == 0 || request.ContentLength > auditRequestBodyLimit {
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, auditRequestBodyLimit+1))
	request.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > auditRequestBodyLimit {
		return
	}
	c.Set(auditRequestBodyKey, body)
}

// captureAuditPropertyBefore reads only the in-memory unified snapshot. It
// never calls a Provider, so auditing a write cannot add control-path latency
// or perform an unexpected device read.
func captureAuditPropertyBefore(c echo.Context, devices *application.DeviceService) {
	if devices == nil {
		return
	}
	route := c.Path()
	endpointID, capabilityID, propertyID := "", "", ""
	switch {
	case route == "/api/v1/devices/:id/properties/power":
		endpointID, capabilityID, propertyID = "main", "switch", "power"
	case isAuditDevicePropertyRoute(route):
		endpointID, capabilityID, propertyID = c.Param("endpoint"), c.Param("capability"), c.Param("property")
	default:
		return
	}
	if isAuditSensitiveName(propertyID) {
		return
	}
	items, err := devices.List(c.Request().Context())
	if err != nil {
		return
	}
	for _, item := range items {
		if item.ID != c.Param("id") {
			continue
		}
		property, found := item.Property(endpointID, capabilityID, propertyID)
		c.Set(auditPropertyBeforeKey, auditPropertyBefore{value: property.Value, found: found})
		return
	}
}

func auditRequestDetails(c echo.Context, status int) []domainaudit.Detail {
	request := auditRequestObject(c)
	route := c.Path()
	details := make([]domainaudit.Detail, 0, 3)
	appendDetail := func(label, value string) {
		if value != "" {
			details = append(details, domainaudit.Detail{Label: label, Value: value})
		}
	}

	switch {
	case route == "/api/v1/devices/:id/enabled":
		if value, ok := request["enabled"].(bool); ok {
			appendDetail("设备状态", map[bool]string{true: "已启用", false: "已禁用"}[value])
		}
	case route == "/api/v1/devices/:id/location":
		appendDetail("位置模式", auditString(request["mode"]))
		appendDetail("家庭", auditString(request["homeId"]))
		appendDetail("房间", auditString(request["roomId"]))
	case route == "/api/v1/devices/:id/properties/power":
		if value, ok := request["value"].(bool); ok {
			appendDetail("属性变更", auditPropertyTransition(c, "power", auditPowerValue(value)))
		}
	case isAuditDevicePropertyRoute(route):
		property := strings.Join([]string{c.Param("endpoint"), c.Param("capability"), c.Param("property")}, ".")
		appendDetail("目标属性", property)
		if !isAuditSensitiveName(c.Param("property")) {
			appendDetail("属性变更", auditPropertyTransition(c, c.Param("property"), auditRequestedPropertyValue(request, c.Param("property"))))
		}
	case isAuditDeviceCommandRoute(route):
		command := strings.Join([]string{c.Param("endpoint"), c.Param("capability"), c.Param("command")}, ".")
		appendDetail("执行命令", command)
		appendDetail("参数", auditCommandParameters(request["parameters"]))
	case route == "/api/v1/devices/:id/simulation":
		appendDetail("模拟变更", auditSimulationChanges(request))
	default:
		appendDetail("提交字段", auditSubmittedFields(request))
	}

	if status >= 400 {
		appendDetail("失败原因", auditFailureReason(status))
	}
	return details
}

func auditRequestObject(c echo.Context) map[string]any {
	body, ok := c.Get(auditRequestBodyKey).([]byte)
	if !ok || len(body) == 0 {
		return nil
	}
	result := make(map[string]any)
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}
	return result
}

func isAuditDevicePropertyRoute(route string) bool {
	return strings.HasPrefix(route, "/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/properties/:property")
}

func isAuditDeviceCommandRoute(route string) bool {
	return strings.HasPrefix(route, "/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/commands/:command")
}

func auditRequestedPropertyValue(request map[string]any, propertyID string) string {
	for _, field := range []string{"bool", "int", "number", "string"} {
		if value, ok := request[field]; ok {
			if boolean, ok := value.(bool); ok && propertyID == "power" {
				return auditPowerValue(boolean)
			}
			return auditValue(value)
		}
	}
	return ""
}

func auditPropertyTransition(c echo.Context, propertyID, next string) string {
	if next == "" {
		return ""
	}
	previous := "未知"
	if captured, ok := c.Get(auditPropertyBeforeKey).(auditPropertyBefore); ok && captured.found {
		previous = auditDevicePropertyValue(captured.value, propertyID)
	}
	return propertyID + ": " + previous + " → " + next
}

func auditDevicePropertyValue(value device.PropertyValue, propertyID string) string {
	if value.Bool != nil {
		if propertyID == "power" {
			return auditPowerValue(*value.Bool)
		}
		return strconv.FormatBool(*value.Bool)
	}
	if value.Int != nil {
		return strconv.FormatInt(*value.Int, 10)
	}
	if value.Number != nil {
		return strconv.FormatFloat(*value.Number, 'f', -1, 64)
	}
	if value.String != nil {
		return auditText(*value.String)
	}
	return "未知"
}

func auditPowerValue(value bool) string {
	return map[bool]string{true: "on", false: "off"}[value]
}

func auditSimulationChanges(request map[string]any) string {
	if len(request) == 0 {
		return ""
	}
	keys := make([]string, 0, len(request))
	for key := range request {
		if !isAuditSensitiveName(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(len(keys), 6))
	for _, key := range keys {
		if len(parts) == 6 {
			break
		}
		if value := auditValue(request[key]); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, "，")
}

func auditCommandParameters(value any) string {
	parameters, ok := value.(map[string]any)
	if !ok || len(parameters) == 0 {
		return ""
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if !isAuditSensitiveName(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(len(keys), 6))
	for _, key := range keys {
		if len(parts) == 6 {
			break
		}
		if item := auditValue(parameters[key]); item != "" {
			parts = append(parts, key+"="+item)
		} else {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, "，")
}

func auditSubmittedFields(request map[string]any) string {
	if len(request) == 0 {
		return ""
	}
	keys := make([]string, 0, len(request))
	for key := range request {
		if !isAuditSensitiveName(key) {
			if key == "config" {
				keys = append(keys, "config（已脱敏）")
			} else {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = append(keys[:8], "…")
	}
	return strings.Join(keys, "、")
}

func auditString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return auditText(text)
}

func auditValue(value any) string {
	switch item := value.(type) {
	case bool:
		return strconv.FormatBool(item)
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case string:
		return auditText(item)
	case nil:
		return "null"
	default:
		return ""
	}
}

func auditText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= auditDetailValueLimit {
		return value
	}
	return value[:auditDetailValueLimit] + "…"
}

func isAuditSensitiveName(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"password", "secret", "token", "credential", "authorization", "pairing", "pin", "code", "private", "key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func auditFailureReason(status int) string {
	switch status {
	case 400:
		return "请求参数无效"
	case 401:
		return "需要管理员登录"
	case 403:
		return "无权执行此操作"
	case 404:
		return "目标资源不存在"
	case 409:
		return "当前状态不允许此操作"
	case 422:
		return "设备或参数不受支持"
	case 408:
		return "操作超时"
	default:
		if status >= 500 {
			return "服务暂时不可用"
		}
		return fmt.Sprintf("请求未完成（HTTP %d）", status)
	}
}
