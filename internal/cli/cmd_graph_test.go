package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

func TestWhoamiShowsTheUserAndTheirScopes(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/me", Reply: httpfake.JSON(map[string]any{
		"id": "u1", "displayName": "Ana Lopez", "jobTitle": "Engineer"})}))
	out := h.ok("graph", "whoami")
	contains(t, out, "Profile       dev (azure-cli)", "Signed in as  ana@corp.example", "Kind          user (delegated)",
		"Name          Ana Lopez", "Job title     Engineer", "Scopes        Directory.Read.All, User.Read.All")
	if asked := h.tokens.Asked(); len(asked) == 0 || asked[0] != "https://graph.microsoft.com "+testTenant {
		t.Errorf("asked for %v", asked)
	}
}

func TestWhoamiAsJSONHasTheRecord(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/me", Reply: httpfake.JSON(map[string]any{"id": "u1"})}))
	var record map[string]any
	if err := json.Unmarshal([]byte(h.ok("graph", "whoami", "-o", "json")), &record); err != nil {
		t.Fatal(err)
	}
	if record["kind"] != "user" || record["profile"] != "dev" || record["tenant_id"] != testTenant ||
		record["expires_at"] != "2026-09-24T13:00:00Z" {
		t.Errorf("record %v", record)
	}
	if roles, _ := record["roles"].([]any); roles == nil || len(roles) != 0 {
		t.Errorf("roles should be an empty list: %v", record["roles"])
	}
}

func TestWhoamiForAnAppFindsItsServicePrincipal(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/servicePrincipals", Func: func(r *http.Request) httpfake.Reply {
		if !strings.Contains(r.URL.Query().Get("$filter"), "appId eq '44444444-4444-4444-4444-444444444444'") {
			t.Errorf("filter %q", r.URL.Query().Get("$filter"))
		}
		return httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "sp1", "displayName": "Reader"}}})
	}}))
	h.tokens.Claims = appClaims(testTenant, "Device.Read.All")
	out := h.ok("graph", "whoami")
	contains(t, out, "Kind          app (application)", "Name          Reader", "Roles         Device.Read.All")
}

func TestWhoamiWarnsWhenGraphWillNotSay(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/me",
		Reply: httpfake.GraphError(403, "Authorization_RequestDenied", "Insufficient privileges")}))
	h.ok("graph", "whoami")
	contains(t, h.err.String(), "warning: could not read the user")
}

func TestGetACollectionIsATableOfItsFamiliarColumns(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/users", Reply: httpfake.JSON(map[string]any{
		"value": []any{
			map[string]any{"id": "u1", "displayName": "Ana", "userPrincipalName": "ana@corp.example", "city": "Leeds"},
			map[string]any{"id": "u2", "displayName": "Ben", "userPrincipalName": "ben@corp.example"},
		},
		"@odata.nextLink": "https://graph.microsoft.com/v1.0/users?$skiptoken=x",
	})}))
	out := h.ok("graph", "get", "users")
	contains(t, out, "ID  DISPLAYNAME  USERPRINCIPALNAME", "u1  Ana          ana@corp.example")
	if strings.Contains(out, "CITY") {
		t.Errorf("an unfamiliar column is shown:\n%s", out)
	}
	contains(t, h.err.String(), "2 item(s); more exist (--all, or --limit N)")
}

func TestGetFollowsEveryPageWithAll(t *testing.T) {
	pages := 0
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/users", Func: func(r *http.Request) httpfake.Reply {
		pages++
		if r.URL.Query().Get("$skiptoken") == "" {
			return httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "u1"}},
				"@odata.nextLink": "https://graph.microsoft.com/v1.0/users?$skiptoken=2"})
		}
		return httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "u2"}}})
	}}))
	out := h.ok("graph", "get", "users", "--all", "-o", "json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil || len(items) != 2 {
		t.Fatalf("items %v (%v)", items, err)
	}
	// One GET to see a collection, then its pages.
	if pages != 3 {
		t.Errorf("%d GETs", pages)
	}
}

func TestGetSendsTheQueryOptions(t *testing.T) {
	var seen *http.Request
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /beta/users", Func: func(r *http.Request) httpfake.Reply {
		seen = r
		return httpfake.JSON(map[string]any{"value": []any{}, "@odata.count": 0})
	}}))
	h.ok("graph", "get", "users", "--beta", "--search", "displayName:ana", "--count", "--select", "id,displayName", "--top", "5")
	query := seen.URL.Query()
	if query.Get("$search") != `"displayName:ana"` || query.Get("$count") != "true" || query.Get("$top") != "5" ||
		query.Get("$select") != "id,displayName" {
		t.Errorf("query %v", query)
	}
	if seen.Header.Get("ConsistencyLevel") != "eventual" {
		t.Error("no ConsistencyLevel")
	}
	contains(t, h.err.String(), "0 item(s) of 0")
}

func TestGetAnObjectIsItsFields(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/organization/o1", Reply: httpfake.JSON(map[string]any{
		"@odata.context": "x", "id": "o1", "displayName": "Corp", "verifiedDomains": []any{"corp.example"}, "onPremisesSyncEnabled": true})}))
	out := h.ok("graph", "get", "organization/o1")
	contains(t, out, "id                     o1\ndisplayName            Corp", `verifiedDomains        ["corp.example"]`,
		"onPremisesSyncEnabled  true")
	if strings.Contains(out, "@odata") {
		t.Errorf("@odata shown:\n%s", out)
	}
}

func TestGetRefusesAPathThatClimbs(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "graph", "get", "users/../../me"), "is not a Graph path")
	if len(h.transport.Seen()) != 0 {
		t.Error("a request was sent")
	}
}

func TestGetRefusesAnotherHost(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "graph", "get", "https://graph.example.test/v1.0/users"), "refusing to send a token")
}

func TestGetLimitMustBePositive(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, usageError(h.fails(2, "graph", "get", "users", "--limit", "0")), "invalid value for --limit")
}

func TestGetUserByUPN(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/users/ana@corp.example",
		Reply: httpfake.JSON(map[string]any{"id": "u1", "userPrincipalName": "ana@corp.example"})}))
	contains(t, h.ok("graph", "get-user", "ana@corp.example"), "userPrincipalName  ana@corp.example")
}

func TestGetGroupByNameIsAmbiguousWhenTwoShareIt(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/groups", Reply: httpfake.JSON(map[string]any{
		"value": []any{map[string]any{"id": "g1"}, map[string]any{"id": "g2"}}})}))
	stderr := h.fails(1, "graph", "get-group", "Admins")
	contains(t, stderr, "2 groups are named 'Admins'", "use an id instead: g1, g2")
}

func TestGetDeviceShowsEveryStaleRegistration(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
		if strings.Contains(r.URL.Query().Get("$filter"), "'web01.corp.example'") {
			return httpfake.JSON(map[string]any{"value": []any{}})
		}
		return httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "d1", "displayName": "web01"},
			map[string]any{"id": "d2", "displayName": "web01"}}})
	}}))
	out := h.ok("graph", "get-device", "web01.corp.example")
	contains(t, out, "d1", "d2")
	contains(t, h.err.String(), "2 devices are named 'web01.corp.example'")
}

func TestGetDeviceNotFoundNamesBothNames(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/devices", Reply: httpfake.JSON(map[string]any{"value": []any{}})}))
	contains(t, h.fails(1, "graph", "get-device", "web01.corp.example"), "no device is named 'web01.corp.example' or 'web01'")
}

func TestHuntKeepsTheSchemaColumnOrder(t *testing.T) {
	var body map[string]any
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery", Func: func(r *http.Request) httpfake.Reply {
		_ = json.NewDecoder(r.Body).Decode(&body)
		return httpfake.JSON(map[string]any{
			"schema":  []any{map[string]any{"name": "Timestamp"}, map[string]any{"name": "DeviceName"}},
			"results": []any{map[string]any{"DeviceName": "web01", "Timestamp": "2026-09-24T10:00:00Z"}},
		})
	}}))
	out := h.ok("graph", "hunt", "DeviceInfo | take 1", "--timespan", "7d")
	contains(t, out, "Timestamp             DeviceName\n")
	if body["Query"] != "DeviceInfo | take 1" || body["Timespan"] != "P7D" {
		t.Errorf("body %v", body)
	}
	contains(t, h.err.String(), "1 row(s)")
}

func TestHuntReadsTheQueryFromStdin(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery",
		Reply: httpfake.JSON(map[string]any{"schema": []any{}, "results": []any{}})}))
	h.stdin = "DeviceInfo\n"
	h.ok("graph", "hunt")
	if !strings.Contains(h.transport.Seen()[0].Body, `"Query":"DeviceInfo"`) {
		t.Errorf("body %s", h.transport.Seen()[0].Body)
	}
}

func TestHuntRefusedSaysWhichPermission(t *testing.T) {
	h := newHarness(t, httpfake.Routes(httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery",
		Reply: httpfake.GraphError(403, "Forbidden", "Missing role permissions")}))
	contains(t, h.fails(1, "graph", "hunt", "DeviceInfo"), "ThreatHunting.Read.All")
}

func TestHuntWithoutAQuery(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "graph", "hunt"), "no query given")
}

func TestAnUnknownGraphCommandIsUsage(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, usageError(h.fails(2, "graph", "bogus")), `unknown command "bogus"`)
}
