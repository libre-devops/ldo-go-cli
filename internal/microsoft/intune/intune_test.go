package intune

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

func client(t *testing.T, handler httpfake.Handler) (*Client, *httpfake.Transport) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport
}

func TestDevicesByNameNewestSyncFirst(t *testing.T) {
	c, transport := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/deviceManagement/managedDevices", Func: func(r *http.Request) httpfake.Reply {
		if r.URL.Query().Get("$filter") != "deviceName eq 'laptop-042'" {
			return httpfake.JSON(map[string]any{"value": []any{}})
		}
		return httpfake.JSON(map[string]any{"value": []any{
			map[string]any{"id": "old", "lastSyncDateTime": "2026-09-01T00:00:00Z", "azureADDeviceId": "00000000-0000-0000-0000-000000000000"},
			map[string]any{"id": "new", "lastSyncDateTime": "2026-09-24T00:00:00Z", "complianceState": "Compliant"},
		}})
	}}))
	found, err := c.FindDevices(context.Background(), "laptop-042.corp.example")
	if err != nil || len(found) != 2 || found[0].ID != "new" || !found[0].Compliant() || found[1].AzureADDeviceID != "" {
		t.Errorf("%+v %v", found, err)
	}
	if !strings.Contains(transport.Seen()[0].URL.Query().Get("$select"), "complianceState") {
		t.Error("no $select")
	}
	if _, err := c.ForEntraDevice(context.Background(), "not-a-guid"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if found, err := c.ForEntraDevice(context.Background(), "11111111-1111-1111-1111-111111111111"); err != nil || len(found) != 0 {
		t.Errorf("%v %v", found, err)
	}
}
