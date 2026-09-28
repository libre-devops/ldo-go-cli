package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// entraDirectory answers device, group and user lookups from a small directory.
func entraDirectory() httpfake.Handler {
	servers := graphfake.Group(50, "Servers", false)
	return httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
			filter := r.URL.Query().Get("$filter")
			switch {
			case strings.Contains(filter, "'web01'"):
				return httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", testNow.Add(-time.Hour)),
					graphfake.Device(3, "web01", testNow.Add(-90*24*time.Hour))))
			case strings.Contains(filter, "'web02'"):
				return httpfake.JSON(graphfake.Page(graphfake.Device(2, "web02", testNow.Add(-2*time.Hour))))
			}
			return httpfake.JSON(graphfake.Page())
		}},
		httpfake.Route{Match: "GET /v1.0/groups", Func: func(r *http.Request) httpfake.Reply {
			if strings.Contains(r.URL.Query().Get("$filter"), "'Servers'") {
				return httpfake.JSON(graphfake.Page(servers))
			}
			return httpfake.JSON(graphfake.Page())
		}},
		httpfake.Route{Match: "GET /v1.0/groups/" + graphfake.ID(50) + "/transitiveMembers/microsoft.graph.device",
			Reply: httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", testNow)))},
		httpfake.Route{Match: "GET /v1.0/groups/" + graphfake.ID(50) + "/transitiveMembers",
			Reply: httpfake.JSON(graphfake.Page(map[string]any{"@odata.type": "#microsoft.graph.device", "id": graphfake.ID(1), "displayName": "web01"}))},
		httpfake.Route{Match: "GET /v1.0/devices/" + graphfake.ID(1) + "/transitiveMemberOf",
			Reply: httpfake.JSON(graphfake.Page(servers, graphfake.Group(51, "All devices", true)))},
		httpfake.Route{Match: "GET /v1.0/devices/" + graphfake.ID(3) + "/transitiveMemberOf", Reply: httpfake.JSON(graphfake.Page())},
		httpfake.Route{Match: "GET /v1.0/users/ana@corp.example", Reply: httpfake.JSON(graphfake.User(10, "ana@corp.example", "Ana"))},
		httpfake.Route{Match: "GET /v1.0/users/" + graphfake.ID(10) + "/transitiveMemberOf/microsoft.graph.group",
			Reply: httpfake.JSON(graphfake.Page(servers))},
		httpfake.Route{Match: "GET /v1.0/users/" + graphfake.ID(10) + "/transitiveMemberOf/microsoft.graph.directoryRole",
			Reply: httpfake.JSON(graphfake.Page(map[string]any{"displayName": "Security Reader"}))},
		httpfake.Route{Match: "GET /v1.0/roleManagement", Reply: httpfake.JSON(graphfake.Page(map[string]any{
			"roleDefinition": map[string]any{"displayName": "Intune Administrator"}}))},
	)
}

func TestEntraDevicesFindsEachAndChecksTheGroup(t *testing.T) {
	h := newHarness(t, entraDirectory())
	code := h.run("entra", "devices", "web01,web02", "web03", "--group", "Servers")
	if code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	out := h.out.String()
	contains(t, out, "IN Servers", "web01           web01", "  older record", "web03           not in Entra")
	lines := strings.Split(out, "\n")
	if !strings.HasSuffix(strings.TrimSpace(lines[2]), "yes") || !strings.HasSuffix(strings.TrimSpace(lines[4]), "no") {
		t.Errorf("membership:\n%s", out)
	}
	contains(t, h.err.String(), "2 of 3 in Entra, 1 in every group (profile dev)")
}

func TestEntraDevicesAllFoundExitsZero(t *testing.T) {
	h := newHarness(t, entraDirectory())
	h.ok("entra", "devices", "web01")
}

func TestEntraDevicesAsJSON(t *testing.T) {
	h := newHarness(t, entraDirectory())
	h.run("entra", "devices", "web01", "missing", "-o", "json")
	var records []map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &records); err != nil {
		t.Fatal(err)
	}
	if records[0]["found"] != true || len(records[0]["devices"].([]any)) != 2 || records[1]["found"] != false {
		t.Errorf("%v", records)
	}
}

func TestEntraDevicesFromAWorkbookColumn(t *testing.T) {
	h := newHarness(t, entraDirectory())
	path := filepath.Join(t.TempDir(), "plan.csv")
	_ = os.WriteFile(path, []byte("Host,Date\nweb01,2026-09-24\nweb02,2026-09-25\n"), 0o600)
	h.ok("entra", "devices", "-f", path, "--column", "Host", "--where", "Date=today")
	contains(t, h.err.String(), "1 name(s) from the rows where Date=today")
	if strings.Contains(h.out.String(), "web02") {
		t.Error("a row --where left out was read")
	}
}

func TestEntraDevicesWithoutNames(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "entra", "devices"), "no names given")
	contains(t, usageError(h.fails(2, "entra", "devices", "-f", "/nowhere/plan.csv")), "does not exist")
	contains(t, usageError(h.fails(2, "entra", "devices", "x", "--workers", "99")), "--workers")
}

func TestEntraDeviceGroupsListsEachRegistration(t *testing.T) {
	h := newHarness(t, entraDirectory())
	out := h.ok("entra", "device-groups", "web01")
	contains(t, out, "Servers", "dynamic", "(no group memberships)")
	contains(t, h.err.String(), "2 Entra devices are named 'web01'")
	h.ok("entra", "device-groups", "web01", "-o", "csv")
	contains(t, h.out.String(), "DEVICE,DEVICE OBJECT ID,GROUP")
	contains(t, h.fails(1, "entra", "device-groups", "nope.corp.example"), "no Entra device is named 'nope.corp.example' or 'nope'")
}

func TestEntraGroupDevicesAndMembers(t *testing.T) {
	h := newHarness(t, entraDirectory())
	contains(t, h.ok("entra", "group-devices", "Servers"), "Servers  object "+graphfake.ID(50)+"  assigned  1 device(s)", "web01")
	contains(t, h.ok("entra", "group-members", "Servers"), "device  web01")
	contains(t, usageError(h.fails(2, "entra", "group-members", "Servers", "--kind", "robot")), "--kind must be one of")
	contains(t, h.fails(1, "entra", "group-devices", "Nobody"), "no Entra group is named 'Nobody'")
}

func TestEntraUserGroupsAndRoles(t *testing.T) {
	h := newHarness(t, entraDirectory())
	contains(t, h.ok("entra", "user-groups", "ana@corp.example"), "ana@corp.example  object", "Servers")
	out := h.ok("entra", "user-roles", "ana@corp.example")
	contains(t, out, "Security Reader       active", "Intune Administrator  eligible  /      permanent")
}

func TestEntraSignIns(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/auditLogs/signIns", Func: func(r *http.Request) httpfake.Reply {
		if !strings.Contains(r.URL.Query().Get("$filter"), "createdDateTime ge 2026-09-23T12:00:00Z") {
			t.Errorf("filter %s", r.URL.Query().Get("$filter"))
		}
		return httpfake.JSON(graphfake.Page(map[string]any{"userPrincipalName": "ana@corp.example", "appDisplayName": "Portal",
			"status": map[string]any{"errorCode": 50126, "failureReason": "Invalid password"}}))
	}}))
	contains(t, h.ok("entra", "sign-ins"), "ana@corp.example  Portal  50126 Invalid password")
	contains(t, h.err.String(), "1 sign-in(s) in the last 1d")
	contains(t, usageError(h.fails(2, "entra", "sign-ins", "--since", "soon")), "--since")
}

func TestEntraAppCredentialsExitsThreeWhenAnyEndSoon(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/applications", Reply: httpfake.JSON(graphfake.Page(map[string]any{
		"displayName": "Payroll", "appId": "app",
		"passwordCredentials": []any{map[string]any{"displayName": "ci", "endDateTime": "2026-10-01T12:00:00Z"},
			map[string]any{"displayName": "old", "endDateTime": "2026-09-01T12:00:00Z"}},
		"keyCredentials": []any{map[string]any{"displayName": "cert", "endDateTime": "2028-01-01T00:00:00Z"}},
	}))}))
	if code := h.run("entra", "app-credentials"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "expired 23d ago", "ci ")
	contains(t, h.err.String(), "2 credential(s) end within 30d (of 3 checked)")
	h.run("entra", "app-credentials", "--hide-expired", "-o", "json")
	var records []map[string]any
	_ = json.Unmarshal(h.out.Bytes(), &records)
	if len(records) != 1 || records[0]["days_left"] != float64(7) {
		t.Errorf("%v", records)
	}
	h.run("entra", "app-credentials", "--all")
	contains(t, h.out.String(), "cert")
}

func TestEntraCAPolicies(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/identity/conditionalAccess/policies", Reply: httpfake.JSON(graphfake.Page(
		map[string]any{"displayName": "Require MFA", "state": "enabledForReportingButNotEnforced",
			"conditions": map[string]any{"users": map[string]any{"includeUsers": []any{"All"}, "excludeUsers": []any{graphfake.ID(1)}},
				"applications": map[string]any{"includeApplications": []any{graphfake.ID(2), graphfake.ID(3)}}},
			"grantControls": map[string]any{"operator": "OR", "builtInControls": []any{"mfa", "compliantDevice"}}},
	))}))
	contains(t, h.ok("entra", "ca-policies"), "Require MFA  report-only  All, 1 excluded  2 included  mfa OR compliantDevice")
	h.ok("entra", "ca-policies", "--state", "enabled")
	if strings.Contains(h.out.String(), "Require MFA") || strings.Contains(h.err.String(), "may lack") {
		t.Errorf("%s %s", h.out, h.err)
	}
	contains(t, usageError(h.fails(2, "entra", "ca-policies", "--state", "on")), "--state must be one of")
}
