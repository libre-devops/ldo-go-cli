package azure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

const sub = armfake.Subscription

func client(t *testing.T, handler httpfake.Handler) (*Client, *httpfake.Transport, *tokenfake.Provider) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	tokens := &tokenfake.Provider{}
	built, err := New(microsoft.API{Tokens: tokens, TenantID: armfake.Tenant, Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport, tokens
}

func TestSubscriptionsInTheTenantFollowNextLink(t *testing.T) {
	c, _, tokens := client(t, func(r *http.Request) httpfake.Reply {
		if r.URL.Query().Get("page") == "" {
			return httpfake.JSON(armfake.Page("https://management.azure.com:443/subscriptions?api-version=x&page=2",
				map[string]any{"subscriptionId": "B", "displayName": "zulu", "tenantId": armfake.Tenant}))
		}
		return httpfake.JSON(armfake.Page("", map[string]any{"subscriptionId": "A", "displayName": "Alpha", "tenantId": armfake.Tenant},
			map[string]any{"subscriptionId": "C", "displayName": "other", "tenantId": "elsewhere"}))
	})
	found, err := c.Subscriptions(context.Background(), strings.ToUpper(armfake.Tenant))
	if err != nil || len(found) != 2 || found[0].Name != "Alpha" || found[1].ID != "b" {
		t.Errorf("%+v %v", found, err)
	}
	if tokens.Asked()[0] != "https://management.azure.com/ "+armfake.Tenant {
		t.Error(tokens.Asked())
	}
}

func TestResourceGraphPagesUpToTheLimit(t *testing.T) {
	var bodies []map[string]any
	c, _, _ := client(t, httpfake.Routes(httpfake.Route{Match: "POST /providers/Microsoft.ResourceGraph/resources", Func: func(r *http.Request) httpfake.Reply {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		return httpfake.JSON(map[string]any{"data": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}, "$skipToken": "more"})
	}}))
	result, err := c.ResourceGraph(context.Background(), "resources", []string{sub}, 3)
	if err != nil || len(result.Rows) != 4 || !result.Truncated {
		t.Fatalf("%+v %v", result, err)
	}
	if bodies[1]["options"].(map[string]any)["$skipToken"] != "more" || bodies[1]["options"].(map[string]any)["$top"] != float64(1) ||
		bodies[0]["subscriptions"].([]any)[0] != sub {
		t.Errorf("%v", bodies)
	}
	for _, bad := range [][]string{{"x"}} {
		if _, err := c.ResourceGraph(context.Background(), "resources", bad, 1); !errs.Is(err, errs.Input) {
			t.Errorf("%v", err)
		}
	}
	if _, err := c.ResourceGraph(context.Background(), " ", nil, 1); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestAWorkspaceByEachOfItsNames(t *testing.T) {
	id := armfake.Group("rg") + "/providers/Microsoft.OperationalInsights/workspaces/law-soc"
	workspace := map[string]any{"id": id, "name": "law-soc", "location": "uksouth", "properties": map[string]any{"customerId": "AAAAAAAA-1111-2222-3333-444444444444"}}
	c, transport, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET " + id, Reply: httpfake.JSON(workspace)},
		httpfake.Route{Match: "GET " + armfake.Group("rg") + "/providers/Microsoft.OperationalInsights/workspaces/gone", Reply: httpfake.Status(404, nil)},
		httpfake.Route{Match: "POST /providers/Microsoft.ResourceGraph/resources", Func: func(r *http.Request) httpfake.Reply {
			data, _ := io.ReadAll(r.Body)
			switch {
			case strings.Contains(string(data), "=~ 'law-soc'"):
				return httpfake.JSON(map[string]any{"data": []any{workspace}})
			case strings.Contains(string(data), "=~ 'twice'"):
				return httpfake.JSON(map[string]any{"data": []any{workspace, workspace}})
			}
			return httpfake.JSON(map[string]any{"data": []any{}})
		}},
	))
	for _, text := range []string{id, "law-soc"} {
		ref, _ := microsoft.WorkspaceRefOf(text)
		found, err := c.FindWorkspace(context.Background(), ref, nil)
		if err != nil || found.WorkspaceID != "aaaaaaaa-1111-2222-3333-444444444444" || found.ResourceGroup != "rg" {
			t.Errorf("%s: %+v %v", text, found, err)
		}
	}
	for text, kind := range map[string]errs.Kind{"twice": errs.Ambiguous, "nowhere": errs.NotFound,
		armfake.Group("rg") + "/providers/Microsoft.OperationalInsights/workspaces/gone": errs.NotFound} {
		ref, _ := microsoft.WorkspaceRefOf(text)
		if _, err := c.FindWorkspace(context.Background(), ref, nil); !errs.Is(err, kind) {
			t.Errorf("%s: %v", text, err)
		}
	}
	if !strings.Contains(transport.Seen()[0].URL.RawQuery, "api-version="+WorkspacesAPI) {
		t.Error(transport.Seen()[0].URL)
	}
}

func TestRoleAssignmentsAreNamedOnceAndSortedByScope(t *testing.T) {
	definition := "/subscriptions/" + sub + "/providers/Microsoft.Authorization/roleDefinitions/acdd72a7"
	assignment := func(id, scope string) map[string]any {
		return map[string]any{"id": id, "properties": map[string]any{"scope": scope, "roleDefinitionId": definition,
			"principalId": "p", "principalType": "Group"}}
	}
	lookups := 0
	c, _, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /subscriptions/" + sub + "/providers/Microsoft.Authorization/roleAssignments", Func: func(r *http.Request) httpfake.Reply {
			if r.URL.Query().Get("$filter") != "assignedTo('33333333-3333-3333-3333-333333333333')" {
				t.Error(r.URL.Query())
			}
			return httpfake.JSON(armfake.Page("", assignment("b", "/subscriptions/"+sub+"/resourceGroups/rg"), assignment("a", "/subscriptions/"+sub),
				assignment("a", "/subscriptions/"+sub)))
		}},
		httpfake.Route{Match: "GET " + definition, Func: func(*http.Request) httpfake.Reply {
			lookups++
			return httpfake.JSON(map[string]any{"properties": map[string]any{"roleName": "Reader"}})
		}},
	))
	found, err := c.RoleAssignments(context.Background(), "33333333-3333-3333-3333-333333333333", []string{sub})
	if err != nil || len(found) != 2 || found[0].ID != "a" || found[1].RoleName != "Reader" || lookups != 1 {
		t.Errorf("%+v %v (%d)", found, err, lookups)
	}
	if _, err := c.RoleAssignments(context.Background(), "ana", []string{sub}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestDefenderForCloud(t *testing.T) {
	base := "/subscriptions/" + sub + "/providers/Microsoft.Security"
	c, _, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET " + base + "/secureScores/ascScore", Reply: httpfake.JSON(map[string]any{"properties": map[string]any{
			"score": map[string]any{"current": 12.5, "max": 20, "percentage": 0.625}}})},
		httpfake.Route{Match: "GET " + base + "/secureScoreControls", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"name": "c1", "properties": map[string]any{"score": map[string]any{"current": 1, "max": 2}, "unhealthyResourceCount": 3}},
			map[string]any{"name": "c2", "properties": map[string]any{"displayName": "MFA", "score": map[string]any{"current": 0, "max": 10}}}))},
		httpfake.Route{Match: "GET " + base + "/assessments", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"id": armfake.Group("rg") + "/providers/Microsoft.Security/assessments/a1", "properties": map[string]any{
				"displayName": "Low one", "status": map[string]any{"code": "Unhealthy"}, "metadata": map[string]any{"severity": "Low"}}},
			map[string]any{"id": "odd", "properties": map[string]any{"displayName": "High one", "status": map[string]any{"code": "Unhealthy",
				"description": "why"}, "metadata": map[string]any{"severity": "High"}}},
			map[string]any{"id": "x", "properties": map[string]any{"status": map[string]any{"code": "Healthy"}}}))},
		httpfake.Route{Match: "GET " + base + "/pricings", Reply: httpfake.JSON(map[string]any{"value": []any{
			map[string]any{"name": "VirtualMachines", "properties": map[string]any{"pricingTier": "Standard", "enablementTime": "2026-01-01T00:00:00Z"}},
			map[string]any{"name": "Api", "properties": map[string]any{"pricingTier": "Free", "deprecated": true}}}})},
	))
	score, err := c.SecureScore(context.Background(), sub)
	if err != nil || *score.Current != 12.5 || *score.Max != 20 {
		t.Errorf("%+v %v", score, err)
	}
	controls, _ := c.SecureScoreControls(context.Background(), sub)
	if controls[0].Name != "MFA" || controls[1].Unhealthy != 3 {
		t.Errorf("%+v", controls)
	}
	assessments, _ := c.Assessments(context.Background(), sub, true)
	if len(assessments) != 2 || assessments[0].Name != "High one" || assessments[0].Cause != "why" || assessments[1].ResourceID != armfake.Group("rg") {
		t.Errorf("%+v", assessments)
	}
	plans, _ := c.DefenderPlans(context.Background(), sub)
	if plans[0].Name != "Api" || !plans[0].Deprecated || !plans[1].Enabled() {
		t.Errorf("%+v", plans)
	}
	missing, _, _ := client(t, httpfake.Routes())
	if score, err := missing.SecureScore(context.Background(), sub); score != nil || err != nil {
		t.Errorf("%v %v", score, err)
	}
}
