package cli

import (
	"net/http"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

func intuneDevicesFake() httpfake.Handler {
	return httpfake.Routes(httpfake.Route{Match: "GET /v1.0/deviceManagement/managedDevices", Func: func(r *http.Request) httpfake.Reply {
		if r.URL.Query().Get("$filter") == "deviceName eq 'laptop-042'" {
			return httpfake.JSON(map[string]any{"value": []any{
				map[string]any{"deviceName": "laptop-042", "complianceState": "compliant", "lastSyncDateTime": "2026-09-24T10:00:00Z",
					"operatingSystem": "Windows", "osVersion": "10.0", "userPrincipalName": "ana@corp.example", "serialNumber": "SN1"},
				map[string]any{"deviceName": "laptop-042", "complianceState": "noncompliant", "lastSyncDateTime": "2026-08-01T10:00:00Z"},
			}})
		}
		return httpfake.JSON(map[string]any{"value": []any{}})
	}})
}

func TestIntuneDevices(t *testing.T) {
	h := newHarness(t, intuneDevicesFake())
	if code := h.run("intune", "devices", "laptop-042", "laptop-099"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "laptop-042      compliant", "  older record", "laptop-099      not enrolled", "ana@corp.example  -")
	contains(t, h.err.String(), "1 of 2 enrolled in Intune")
	h.ok("intune", "devices", "laptop-042")
}
