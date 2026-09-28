package pim

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

func api(t *testing.T, handler httpfake.Handler) (microsoft.API, *httpfake.Transport) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	return microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: armfake.Tenant, Cloud: microsoft.Public, HTTPClient: httpClient}, transport
}

func TestSettingsAreReadFromTheRules(t *testing.T) {
	var rules []fields.Object
	for _, rule := range graphfake.PIMRules() {
		rules = append(rules, rule.(map[string]any))
	}
	found := SettingsFromRules("entra", "Global Administrator", "/", rules)
	if found.MaxActivation != "PT8H" || !found.RequiresMFA || !found.RequiresJustification || found.RequiresTicket || !found.RequiresApproval ||
		strings.Join(found.Approvers, ",") != "Security Approvers,u1" || found.AuthenticationContext != "c1" ||
		found.EligibleExpiry != "permanent allowed" || found.ActiveExpiry != "P180D" || len(found.Rules) != 6 {
		t.Errorf("%+v", found)
	}
	empty := SettingsFromRules("azure", "r", "s", nil)
	if empty.EligibleExpiry != "" || empty.RequiresApproval {
		t.Errorf("%+v", empty)
	}
	if expiry(fields.Object{"isExpirationRequired": true}) != "required" {
		t.Error("an expiry with no duration")
	}
}

func TestAzureMineAtTheRootAndTheirsPerScope(t *testing.T) {
	instance := func(id, role string) map[string]any {
		return map[string]any{"id": id, "properties": map[string]any{"principalId": "p", "memberType": "Group", "assignmentType": "Activated",
			"expandedProperties": map[string]any{"roleDefinition": map[string]any{"displayName": role}, "scope": map[string]any{"displayName": "Dev"},
				"principal": map[string]any{"email": "ana@corp.example"}}}}
	}
	api, transport := api(t, func(r *http.Request) httpfake.Reply {
		switch {
		case strings.HasSuffix(r.URL.Path, "/roleAssignmentScheduleInstances"):
			return httpfake.JSON(armfake.Page(""))
		case strings.HasSuffix(r.URL.Path, "/roleEligibilityScheduleInstances"):
			return httpfake.JSON(armfake.Page("", instance("b", "Reader"), instance("a", "Owner"), instance("a", "Owner")))
		case strings.HasSuffix(r.URL.Path, "/roleAssignmentScheduleRequests"):
			return httpfake.JSON(armfake.Page("", map[string]any{"id": "r1", "properties": map[string]any{"status": "PendingApproval", "createdOn": "2026-09-24T10:00:00Z",
				"scheduleInfo": map[string]any{"expiration": map[string]any{"duration": "PT8H"}}}}))
		}
		return httpfake.Status(404, nil)
	})
	client, _ := NewAzure(api)
	mine, err := client.Eligible(context.Background(), "", nil)
	if err != nil || len(mine) != 2 || mine[0].Role != "Owner" || mine[0].PrincipalName != "ana@corp.example" || !mine[0].Activated() {
		t.Fatalf("%+v %v", mine, err)
	}
	if transport.Seen()[0].URL.Path != "/providers/Microsoft.Authorization/roleEligibilityScheduleInstances" ||
		transport.Seen()[0].URL.Query().Get("$filter") != "asTarget()" {
		t.Error(transport.Seen()[0].URL)
	}
	principal := "33333333-3333-3333-3333-333333333333"
	if _, err := client.Active(context.Background(), principal, []string{"/subscriptions/" + armfake.Subscription}); err != nil {
		t.Fatal(err)
	}
	last := transport.Seen()[len(transport.Seen())-1]
	if !strings.HasPrefix(last.URL.Path, "/subscriptions/"+armfake.Subscription+"/providers") || last.URL.Query().Get("$filter") != "assignedTo('"+principal+"')" {
		t.Error(last.URL)
	}
	requests, err := client.Requests(context.Background(), true, "", nil)
	if err != nil || !requests[0].Pending() || requests[0].Duration != "PT8H" || transport.Seen()[len(transport.Seen())-1].URL.Query().Get("$filter") != "asApprover()" {
		t.Errorf("%+v %v", requests, err)
	}
	for _, scopes := range [][]string{nil, {"rg-app"}, {"/providers/Microsoft.Web/sites/x"}} {
		if _, err := client.Eligible(context.Background(), principal, scopes); !errs.Is(err, errs.Input) {
			t.Errorf("%v: %v", scopes, err)
		}
	}
}

func TestAzureSettingsByRoleNameAtAScope(t *testing.T) {
	scope := "/subscriptions/" + armfake.Subscription
	api, _ := api(t, httpfake.Routes(
		httpfake.Route{Match: "GET " + scope + "/providers/Microsoft.Authorization/roleDefinitions", Func: func(r *http.Request) httpfake.Reply {
			if r.URL.Query().Get("$filter") == "roleName eq 'Owner'" {
				return httpfake.JSON(armfake.Page("", map[string]any{"id": "def-owner"}))
			}
			return httpfake.JSON(armfake.Page(""))
		}},
		httpfake.Route{Match: "GET " + scope + "/providers/Microsoft.Authorization/roleManagementPolicyAssignments", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"properties": map[string]any{"effectiveRules": graphfake.PIMRules()}}))},
	))
	client, _ := NewAzure(api)
	found, err := client.Settings(context.Background(), "Owner", scope)
	if err != nil || found.MaxActivation != "PT8H" || found.Area != "azure" {
		t.Errorf("%+v %v", found, err)
	}
	if _, err := client.Settings(context.Background(), "Nobody", scope); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
}

func TestGraphRolesAndGroupsByName(t *testing.T) {
	group := graphfake.ID(50)
	api, transport := api(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/roleManagement/directory/roleDefinitions", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"id": "r-ga", "displayName": "Global Administrator"}, map[string]any{"id": "r-sr", "displayName": "Security Reader"}))},
		httpfake.Route{Match: "GET /v1.0/roleManagement/directory/roleEligibilityScheduleInstances", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"roleDefinitionId": "r-sr", "memberType": "Direct"}, map[string]any{"roleDefinitionId": "r-ga", "directoryScopeId": "/au1"},
			map[string]any{"roleDefinitionId": "unknown"}))},
		httpfake.Route{Match: "GET /v1.0/roleManagement/directory/roleAssignmentScheduleRequests", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"id": "q1", "roleDefinitionId": "r-ga", "status": "Provisioned", "createdDateTime": "2026-09-23T00:00:00Z"},
			map[string]any{"id": "q2", "roleDefinitionId": "r-sr", "status": "PendingApproval", "createdDateTime": "2026-09-24T00:00:00Z"}))},
		httpfake.Route{Match: "GET /v1.0/identityGovernance/privilegedAccess/group/eligibilityScheduleInstances", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"groupId": group, "accessId": "owner"}, map[string]any{"groupId": "not-a-guid", "accessId": "member"}))},
		httpfake.Route{Match: "GET /v1.0/identityGovernance/privilegedAccess/group/assignmentScheduleRequests", Reply: httpfake.JSON(graphfake.Page(
			map[string]any{"id": "g1", "groupId": group, "accessId": "member"}))},
		httpfake.Route{Match: "GET /v1.0/groups/" + group, Reply: httpfake.JSON(map[string]any{"displayName": "Break glass"})},
		httpfake.Route{Match: "GET /v1.0/policies/roleManagementPolicyAssignments", Reply: httpfake.JSON(graphfake.Page(map[string]any{
			"policy": map[string]any{"rules": graphfake.PIMRules()}}))},
	))
	client, _ := NewGraph(api)
	eligible, err := client.RoleEligible(context.Background(), "")
	if err != nil || eligible[0].Role != "Global Administrator" || eligible[0].Scope != "/au1" || eligible[2].Role != "unknown" {
		t.Fatalf("%+v %v", eligible, err)
	}
	if !strings.HasSuffix(transport.Seen()[0].URL.Path, "filterByCurrentUser(on='principal')") {
		t.Error(transport.Seen()[0].URL.Path)
	}
	requests, _ := client.RoleRequests(context.Background(), true, "")
	if requests[0].ID != "q2" || requests[0].Role != "Security Reader" {
		t.Errorf("%+v", requests)
	}
	groups, _ := client.GroupEligible(context.Background(), graphfake.ID(1))
	if groups[0].Scope != "Break glass" || groups[1].Scope != "not-a-guid" {
		t.Errorf("%+v", groups)
	}
	groupRequests, _ := client.GroupRequests(context.Background(), false, "")
	if groupRequests[0].Scope != "Break glass" || groupRequests[0].Role != "member" {
		t.Errorf("%+v", groupRequests)
	}
	settings, err := client.RoleSettings(context.Background(), "global administrator")
	if err != nil || settings.Role != "Global Administrator" || settings.Scope != "/" {
		t.Errorf("%+v %v", settings, err)
	}
	if !strings.Contains(transport.Seen()[len(transport.Seen())-1].URL.Query().Get("$filter"), "roleDefinitionId eq 'r-ga'") {
		t.Error(transport.Seen()[len(transport.Seen())-1].URL)
	}
	if group, err := client.GroupSettings(context.Background(), group, "owner"); err != nil || group.Scope != "Break glass" || group.Role != "owner" {
		t.Errorf("%+v %v", group, err)
	}
	if _, err := client.RoleSettings(context.Background(), "Nobody"); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if _, err := client.RoleActive(context.Background(), "ana"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}
