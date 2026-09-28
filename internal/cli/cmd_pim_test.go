package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// pimTenant answers every PIM area: Azure (with P2), Entra roles, and one group; the
// groups area refuses when refuseGroups.
func pimTenant(refuseGroups bool) func() httpfake.Handler {
	return func() httpfake.Handler {
		return func(r *http.Request) httpfake.Reply {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/Microsoft.Authorization/roleEligibilityScheduleInstances"):
				return httpfake.JSON(armfake.Page("", map[string]any{"id": "e1", "properties": map[string]any{"memberType": "Direct",
					"endDateTime": "2027-01-01T00:00:00Z", "expandedProperties": map[string]any{"roleDefinition": map[string]any{"displayName": "Owner"},
						"scope": map[string]any{"displayName": "Dev"}}}}))
			case strings.HasSuffix(path, "/Microsoft.Authorization/roleAssignmentScheduleInstances"):
				return httpfake.JSON(armfake.Page("", map[string]any{"id": "a1", "properties": map[string]any{"memberType": "Direct", "assignmentType": "Assigned",
					"expandedProperties": map[string]any{"roleDefinition": map[string]any{"displayName": "Reader"}, "scope": map[string]any{"displayName": "Dev"}}}}))
			case strings.HasSuffix(path, "/Microsoft.Authorization/roleAssignmentScheduleRequests"):
				return httpfake.JSON(armfake.Page("", map[string]any{"id": "q1", "properties": map[string]any{"requestType": "SelfActivate",
					"status": "PendingApproval", "justification": "patching", "scheduleInfo": map[string]any{"expiration": map[string]any{"duration": "PT8H"}},
					"expandedProperties": map[string]any{"roleDefinition": map[string]any{"displayName": "Owner"}, "scope": map[string]any{"displayName": "Dev"}}}}))
			case strings.HasSuffix(path, "/Microsoft.Authorization/roleDefinitions"):
				return httpfake.JSON(armfake.Page("", map[string]any{"id": "def-owner"}))
			case strings.HasSuffix(path, "/Microsoft.Authorization/roleManagementPolicyAssignments"):
				return httpfake.JSON(armfake.Page("", map[string]any{"properties": map[string]any{"effectiveRules": graphfake.PIMRules()}}))
			case strings.HasPrefix(path, "/v1.0/roleManagement/directory/roleDefinitions"):
				return httpfake.JSON(graphfake.Page(map[string]any{"id": "r-ga", "displayName": "Global Administrator"}))
			case strings.HasPrefix(path, "/v1.0/roleManagement/directory/roleEligibilityScheduleInstances"):
				return httpfake.JSON(graphfake.Page(map[string]any{"roleDefinitionId": "r-ga", "memberType": "Direct"}))
			case strings.HasPrefix(path, "/v1.0/roleManagement/directory/roleAssignmentScheduleInstances"):
				return httpfake.JSON(graphfake.Page(map[string]any{"roleDefinitionId": "r-ga", "assignmentType": "Activated", "endDateTime": "2026-09-24T20:00:00Z"}))
			case strings.HasPrefix(path, "/v1.0/roleManagement/directory/roleAssignmentScheduleRequests"):
				return httpfake.JSON(graphfake.Page())
			case strings.HasPrefix(path, "/v1.0/identityGovernance"):
				if refuseGroups {
					return httpfake.GraphError(403, "UnknownError", "PermissionScopeNotGranted")
				}
				return httpfake.JSON(graphfake.Page(map[string]any{"groupId": graphfake.ID(50), "accessId": "member"}))
			case strings.HasPrefix(path, "/v1.0/groups/"+graphfake.ID(50)):
				return httpfake.JSON(map[string]any{"displayName": "Break glass"})
			case strings.HasPrefix(path, "/v1.0/groups"):
				return httpfake.JSON(graphfake.Page(graphfake.Group(50, "Break glass", false)))
			case strings.HasPrefix(path, "/v1.0/policies/roleManagementPolicyAssignments"):
				return httpfake.JSON(graphfake.Page(map[string]any{"policy": map[string]any{"rules": graphfake.PIMRules()}}))
			case strings.HasPrefix(path, "/v1.0/users/ana@corp.example"):
				return httpfake.JSON(graphfake.User(10, "ana@corp.example", "Ana"))
			}
			return httpfake.Status(404, nil)
		}
	}
}

func TestPIMEligibleAcrossTheAreas(t *testing.T) {
	h := newHarness(t, pimTenant(false)())
	out := h.ok("pim", "eligible")
	contains(t, out, "azure   Owner                 Dev", "entra   Global Administrator  /", "groups  member                Break glass")
	contains(t, h.err.String(), "3 eligible role(s)")
	h.ok("pim", "eligible", "--entra")
	if strings.Contains(h.out.String(), "Owner") {
		t.Error("--entra showed Azure roles")
	}
}

func TestPIMActiveMarksStandingAccess(t *testing.T) {
	h := newHarness(t, pimTenant(false)())
	out := h.ok("pim", "active", "--azure", "--entra")
	contains(t, out, "Reader", "Assigned", "permanent", "Global Administrator", "activated")
	contains(t, h.err.String(), "2 active role(s), 1 of them permanent")
	if strings.Contains(h.ok("pim", "active", "--permanent-only"), "Global Administrator") {
		t.Error("an ending role was kept")
	}
}

func TestPIMAnAreaThatFailsIsAWarning(t *testing.T) {
	h := newHarness(t, pimTenant(true)())
	h.ok("pim", "eligible")
	contains(t, h.err.String(), "warning: groups:", "these views need a token with the PIM scopes")
	h.fails(1, "pim", "eligible", "--groups")
}

func TestPIMRequestsApprovalsAndSomeoneElses(t *testing.T) {
	h := newHarness(t, pimTenant(false)())
	contains(t, h.ok("pim", "requests", "--azure", "--pending"), "SelfActivate  PendingApproval  Owner  Dev    8h   patching")
	contains(t, h.ok("pim", "approvals", "--azure"), "PendingApproval")
	contains(t, h.err.String(), "1 request(s) waiting for you")
	h.ok("pim", "eligible", "--user", "ana@corp.example", "--azure")
	if !strings.Contains(h.transport.Seen()[len(h.transport.Seen())-1].URL.RawQuery, "assignedTo") {
		t.Error(h.transport.Paths())
	}
}

func TestPIMSettings(t *testing.T) {
	h := newHarness(t, pimTenant(false)())
	out := h.ok("pim", "settings", "Global Administrator")
	contains(t, out, "Longest activation      8h", "Needs MFA               yes", "Approvers               Security Approvers, u1",
		"Eligible assignments    permanent allowed", "Active assignments      180d")
	contains(t, h.ok("pim", "settings", "--group", "Break glass", "--owner"), "Role                    owner", "Break glass")
	contains(t, h.ok("pim", "settings", "Owner", "--scope", "/subscriptions/"+testSubscription), "azure")
	contains(t, usageError(h.fails(2, "pim", "settings")), "name a role, or pass --group")
	if pimDuration("P1DT2H30M") != "1d 2h 30m" || pimDuration("soon") != "soon" || pimDuration("") != "-" || pimDuration("P") != "P" {
		t.Error(pimDuration("P1DT2H30M"))
	}
}
