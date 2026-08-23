package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/labstack/echo/v4"
)

func TestAuditRequestDetailsRecordsSafeDevicePropertyWrite(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/devices/switch-1/endpoints/main/capabilities/switch/properties/power", bytes.NewBufferString(`{"type":"bool","bool":true}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	context := e.NewContext(request, httptest.NewRecorder())
	context.SetPath("/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/properties/:property")
	context.SetParamNames("id", "endpoint", "capability", "property")
	context.SetParamValues("switch-1", "main", "switch", "power")

	captureAuditRequestBody(context)
	context.Set(auditPropertyBeforeKey, auditPropertyBefore{value: device.BoolValue(false), found: true})
	details := auditRequestDetails(context, http.StatusOK)
	if got, want := details, []string{"目标属性=main.switch.power", "属性变更=power: off → on"}; !sameAuditDetails(got, want) {
		t.Fatalf("details = %#v, want %#v", got, want)
	}
}

func TestAuditRequestDetailsNeverStoresSensitivePropertyValue(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/devices/lock-1/endpoints/main/capabilities/lock/properties/access-code", bytes.NewBufferString(`{"type":"string","string":"super-secret"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	context := e.NewContext(request, httptest.NewRecorder())
	context.SetPath("/api/v1/devices/:id/endpoints/:endpoint/capabilities/:capability/properties/:property")
	context.SetParamNames("id", "endpoint", "capability", "property")
	context.SetParamValues("lock-1", "main", "lock", "access-code")

	captureAuditRequestBody(context)
	details := auditRequestDetails(context, http.StatusBadRequest)
	for _, detail := range details {
		if bytes.Contains([]byte(detail.Value), []byte("super-secret")) {
			t.Fatalf("secret leaked in audit detail: %#v", details)
		}
	}
	if got, want := details, []string{"目标属性=main.lock.access-code", "失败原因=请求参数无效"}; !sameAuditDetails(got, want) {
		t.Fatalf("details = %#v, want %#v", got, want)
	}
}

func sameAuditDetails(details []domainaudit.Detail, want []string) bool {
	actual := make([]string, 0, len(details))
	for _, detail := range details {
		actual = append(actual, detail.Label+"="+detail.Value)
	}
	return strings.Join(actual, "|") == strings.Join(want, "|")
}
