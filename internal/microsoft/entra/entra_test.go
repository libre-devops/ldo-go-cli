package entra

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func client(t *testing.T, handler httpfake.Handler) (*Client, *httpfake.Transport) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport
}

func TestDevicesAreFoundByFQDNThenShortNameNewestFirst(t *testing.T) {
	c, transport := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
		if strings.Contains(r.URL.Query().Get("$filter"), "'web01.corp.example'") {
			return httpfake.JSON(graphfake.Page())
		}
		return httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", now.Add(-48*time.Hour)),
			graphfake.Device(2, "web01", time.Time{}), graphfake.Device(3, "web01", now.Add(-time.Hour))))
	}}))
	devices, err := c.FindDevices(context.Background(), "web01.corp.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || devices[0].ID != graphfake.ID(3) || devices[2].ID != graphfake.ID(2) {
		t.Errorf("%v", devices)
	}
	if len(transport.Seen()) != 2 {
		t.Errorf("%v", transport.Paths())
	}
}

func TestAShortNameFindsTheFQDNEntraHolds(t *testing.T) {
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
		if strings.HasPrefix(r.URL.Query().Get("$filter"), "startswith") {
			return httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01.corp.example", now), graphfake.Device(2, "web010.corp.example", now)))
		}
		return httpfake.JSON(graphfake.Page())
	}}))
	devices, err := c.FindDevices(context.Background(), "WEB01")
	if err != nil || len(devices) != 1 || devices[0].DisplayName != "web01.corp.example" {
		t.Errorf("%v %v", devices, err)
	}
}

func TestLookUpDevicesChecksGroupMembership(t *testing.T) {
	group := GroupFrom(graphfake.Group(50, "Servers", false))
	c, transport := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/groups/" + group.ID + "/transitiveMembers/microsoft.graph.device",
			Reply: httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", now)))},
		httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
			if strings.Contains(r.URL.Query().Get("$filter"), "'web01'") {
				return httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", now)))
			}
			if strings.Contains(r.URL.Query().Get("$filter"), "'web02'") {
				return httpfake.JSON(graphfake.Page(graphfake.Device(2, "web02", now)))
			}
			return httpfake.JSON(graphfake.Page())
		}},
	))
	lookups, err := c.LookUpDevices(context.Background(), []string{"web01", "web02", "web03"}, []Group{group}, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !lookups[0].InEveryGroup() || lookups[1].InEveryGroup() || lookups[2].Found() {
		t.Errorf("%+v", lookups)
	}
	for _, seen := range transport.Seen() {
		if strings.Contains(seen.URL.Path, "microsoft.graph.device") && seen.Header.Get("ConsistencyLevel") != "eventual" {
			t.Error("a cast was sent without ConsistencyLevel")
		}
	}
}

func TestGroupsByNameMustBeOne(t *testing.T) {
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/groups", Func: func(r *http.Request) httpfake.Reply {
		switch {
		case strings.Contains(r.URL.Query().Get("$filter"), "'Twice'"):
			return httpfake.JSON(graphfake.Page(graphfake.Group(1, "Twice", false), graphfake.Group(2, "Twice", true)))
		case strings.Contains(r.URL.Query().Get("$filter"), "'Once'"):
			return httpfake.JSON(graphfake.Page(graphfake.Group(3, "Once", true)))
		}
		return httpfake.JSON(graphfake.Page())
	}}))
	if _, err := c.GetGroup(context.Background(), "Twice"); !errs.Is(err, errs.Ambiguous) || errs.HintOf(err) == "" {
		t.Errorf("%v", err)
	}
	if _, err := c.GetGroup(context.Background(), "None"); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if group, err := c.GetGroup(context.Background(), "Once"); err != nil || !group.Dynamic {
		t.Errorf("%+v %v", group, err)
	}
}

func TestAnIDThatIsNotThereIsNotFound(t *testing.T) {
	c, _ := client(t, httpfake.Routes())
	if _, err := c.GetGroup(context.Background(), graphfake.ID(9)); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if _, err := c.GetUser(context.Background(), "guest#EXT#@corp.example"); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if _, err := c.GroupMembers(context.Background(), "not-an-id", true, ""); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestAGuestUPNIsEscapedInThePath(t *testing.T) {
	c, transport := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/users/", Reply: httpfake.JSON(graphfake.User(1, "x", "Ana"))}))
	if _, err := c.GetUser(context.Background(), "ana_corp.example#EXT#@tenant.example"); err != nil {
		t.Fatal(err)
	}
	if raw := transport.Seen()[0].URL.EscapedPath(); !strings.Contains(raw, "%23EXT%23@") {
		t.Errorf("%s", raw)
	}
}

func TestMembersOfEveryKindSortByKindThenName(t *testing.T) {
	group := graphfake.ID(5)
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/groups/" + group + "/members", Reply: httpfake.JSON(graphfake.Page(
		map[string]any{"@odata.type": "#microsoft.graph.user", "id": "u", "displayName": "zed", "userPrincipalName": "zed@corp.example"},
		map[string]any{"@odata.type": "#microsoft.graph.device", "id": "d", "displayName": "web01", "operatingSystem": "Linux"},
		map[string]any{"@odata.type": "#microsoft.graph.user", "id": "u2", "displayName": "Ana", "userPrincipalName": "ana@corp.example"},
	))}))
	members, err := c.GroupMembers(context.Background(), group, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if members[0].Kind != "device" || members[0].Detail != "Linux" || members[1].DisplayName != "Ana" || members[2].Detail != "zed@corp.example" {
		t.Errorf("%+v", members)
	}
}

func TestRolesSayWhenPIMCannotBeRead(t *testing.T) {
	user := graphfake.ID(1)
	c, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/users/" + user + "/transitiveMemberOf", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"displayName": "Security Reader", "roleTemplateId": "r1"},
			map[string]any{"displayName": "Global Reader", "roleTemplateId": "r2"}))},
		httpfake.Route{Match: "GET /v1.0/roleManagement", Reply: httpfake.GraphError(403, "PermissionScopeNotGranted", "no")},
	))
	report, err := c.UserRoles(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if report.Active[0].RoleName != "Global Reader" || report.Eligible != nil || report.EligibleError == "" {
		t.Errorf("%+v", report)
	}
}

func TestEligibleRolesComeFromTheirDefinition(t *testing.T) {
	user := graphfake.ID(1)
	c, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/users/", Reply: httpfake.JSON(graphfake.Page())},
		httpfake.Route{Match: "GET /v1.0/roleManagement", Reply: httpfake.JSON(graphfake.Page(map[string]any{
			"roleDefinitionId": "d1", "roleDefinition": map[string]any{"displayName": "Intune Administrator", "templateId": "t1"},
			"scheduleInfo": map[string]any{"expiration": map[string]any{"endDateTime": "2027-01-01T00:00:00Z"}}}))},
	))
	report, err := c.UserRoles(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	role := report.Eligible[0]
	if role.RoleName != "Intune Administrator" || role.Scope != "/" || role.Ends.Year() != 2027 || role.State != "eligible" {
		t.Errorf("%+v", role)
	}
}

func TestSignInsFilterAndStopAtTheLimit(t *testing.T) {
	c, transport := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/auditLogs/signIns", Reply: httpfake.JSON(map[string]any{
		"value": []any{
			map[string]any{"id": "1", "status": map[string]any{"errorCode": 50126, "failureReason": "Invalid password"},
				"location": map[string]any{"city": "Leeds", "countryOrRegion": "GB"}},
			map[string]any{"id": "2", "status": map[string]any{"errorCode": 0}},
		},
		"@odata.nextLink": "https://graph.microsoft.com/v1.0/auditLogs/signIns?$skiptoken=x",
	})}))
	events, err := c.SignIns(context.Background(), SignInQuery{User: "ana@corp.example", Since: now, FailuresOnly: true, Limit: 1})
	if err != nil || len(events) != 1 || events[0].Succeeded() || events[0].Location != "Leeds, GB" {
		t.Fatalf("%+v %v", events, err)
	}
	filter := transport.Seen()[0].URL.Query().Get("$filter")
	if filter != "userPrincipalName eq 'ana@corp.example' and createdDateTime ge 2026-09-24T12:00:00Z and status/errorCode ne 0" {
		t.Error(filter)
	}
	if len(transport.Seen()) != 1 || transport.Seen()[0].URL.Query().Get("$top") != "1" {
		t.Errorf("%v", transport.Paths())
	}
}

func TestCredentialsNearExpiry(t *testing.T) {
	c, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/applications", Reply: httpfake.JSON(graphfake.Page(map[string]any{
			"id": "a", "appId": "app", "displayName": "Payroll",
			"passwordCredentials": []any{map[string]any{"displayName": "ci", "keyId": "k1", "endDateTime": "2026-10-01T00:00:00Z"},
				map[string]any{"hint": "abc", "keyId": "k2", "endDateTime": "2026-09-01T00:00:00Z"}},
			"keyCredentials": []any{map[string]any{"displayName": "cert", "keyId": "k3", "endDateTime": "2028-01-01T00:00:00Z"}},
		}))},
		httpfake.Route{Match: "GET /v1.0/servicePrincipals", Reply: httpfake.JSON(graphfake.Page())},
	))
	credentials, err := c.AppCredentials(context.Background(), true)
	if err != nil || len(credentials) != 3 {
		t.Fatalf("%v %v", credentials, err)
	}
	soon := Expiring(credentials, 30*24*time.Hour, now, true)
	if len(soon) != 2 || soon[0].Name != "abc" || soon[1].Kind != "secret" {
		t.Errorf("%+v", soon)
	}
	if days, _ := soon[0].DaysLeft(now); days != -24 {
		t.Errorf("%d", days)
	}
	if len(Expiring(credentials, 30*24*time.Hour, now, false)) != 1 {
		t.Error("an expired credential was kept")
	}
}

func TestConditionalAccessIsFlattened(t *testing.T) {
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/identity", Reply: httpfake.JSON(graphfake.Page(
		map[string]any{"displayName": "b policy", "state": "enabled", "createdDateTime": "2026-01-01T00:00:00Z",
			"conditions":    map[string]any{"users": map[string]any{"includeUsers": []any{"All"}, "excludeGroups": []any{"g"}}},
			"grantControls": map[string]any{"operator": "OR", "builtInControls": []any{"mfa"}, "termsOfUse": []any{"t"}},
			"sessionControls": map[string]any{"@odata.type": "x", "signInFrequency": map[string]any{"value": 1},
				"persistentBrowser": nil}},
		map[string]any{"displayName": "A policy", "state": "disabled"},
	))}))
	policies, err := c.CAPolicies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policy := policies[1]
	if policies[0].DisplayName != "A policy" || strings.Join(policy.GrantControls, ",") != "mfa,terms of use" ||
		strings.Join(policy.SessionControls, ",") != "signInFrequency" || policy.Modified.Year() != 2026 {
		t.Errorf("%+v", policy)
	}
}

func TestFindPrincipalAcrossKinds(t *testing.T) {
	c, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/groups", Reply: httpfake.JSON(graphfake.Page())},
		httpfake.Route{Match: "GET /v1.0/servicePrincipals", Reply: httpfake.JSON(graphfake.Page(map[string]any{"id": "sp", "appId": "app", "displayName": "Deployer"}))},
		httpfake.Route{Match: "GET /v1.0/users", Reply: httpfake.JSON(graphfake.Page())},
		httpfake.Route{Match: "GET /v1.0/directoryObjects/", Reply: httpfake.JSON(map[string]any{"@odata.type": "#microsoft.graph.group", "id": "g"})},
	))
	found, err := c.FindPrincipal(context.Background(), "Deployer")
	if err != nil || found.Kind != "servicePrincipal" || found.Detail != "app" {
		t.Errorf("%+v %v", found, err)
	}
	byID, err := c.FindPrincipal(context.Background(), graphfake.ID(7))
	if err != nil || byID.Kind != "group" {
		t.Errorf("%+v %v", byID, err)
	}
}
