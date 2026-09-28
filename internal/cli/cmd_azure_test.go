package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// arm answers Resource Manager: subscriptions, Resource Graph, RBAC, Defender for Cloud
// and an Automation account with two jobs.
func arm() httpfake.Handler {
	security := "/subscriptions/" + testSubscription + "/providers/Microsoft.Security"
	account := armfake.AutomationAccount("rg-ops", "aa-ops")
	id := account["id"].(string)
	return httpfake.Routes(
		httpfake.Route{Match: "GET /subscriptions/" + testSubscription + "/providers/Microsoft.Automation", Reply: httpfake.JSON(armfake.Page("", account))},
		httpfake.Route{Match: "GET " + id + "/jobs/j2/streams", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"properties": map[string]any{"jobStreamId": "s1", "time": "2026-09-24T10:01:00Z", "streamType": "Output", "summary": "hello"}},
			map[string]any{"properties": map[string]any{"jobStreamId": "s2", "time": "2026-09-24T10:02:00Z", "streamType": "Error", "summary": "boom"}}))},
		httpfake.Route{Match: "GET " + id + "/jobs/j2/output", Reply: httpfake.Reply{Status: 200, Body: httpfake.Text{Content: "hello\n"}}},
		httpfake.Route{Match: "GET " + id + "/jobs/j2", Reply: httpfake.JSON(armfake.Job("j2", "Patch", "Failed", "2026-09-24T10:00:00Z", ""))},
		httpfake.Route{Match: "GET " + id + "/jobs", Reply: httpfake.JSON(armfake.Page("",
			armfake.Job("j1", "Tidy", "Completed", "2026-09-23T10:00:00Z", "2026-09-23T10:02:30Z"),
			armfake.Job("j2", "Patch", "Failed", "2026-09-24T10:00:00Z", "")))},
		httpfake.Route{Match: "GET /subscriptions/" + testSubscription + "/providers/Microsoft.Authorization/roleAssignments",
			Reply: httpfake.JSON(armfake.Page("", map[string]any{"id": "ra1", "properties": map[string]any{"scope": "/subscriptions/" + testSubscription,
				"roleDefinitionId": "/providers/Microsoft.Authorization/roleDefinitions/r1", "principalId": graphfake.ID(10), "principalType": "User"}}))},
		httpfake.Route{Match: "GET /providers/Microsoft.Authorization/roleDefinitions/r1", Reply: httpfake.JSON(map[string]any{"properties": map[string]any{"roleName": "Reader"}})},
		httpfake.Route{Match: "GET " + security + "/secureScores/ascScore", Reply: httpfake.JSON(map[string]any{"properties": map[string]any{
			"score": map[string]any{"current": 12.5, "max": 20, "percentage": 0.625}}})},
		httpfake.Route{Match: "GET " + security + "/secureScoreControls", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"properties": map[string]any{"displayName": "Enable MFA", "score": map[string]any{"current": 0, "max": 10}}}))},
		httpfake.Route{Match: "GET " + security + "/assessments", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"id": armfake.Group("rg") + "/providers/Microsoft.Security/assessments/a1", "properties": map[string]any{
				"displayName": "Encrypt disks", "status": map[string]any{"code": "Unhealthy"}, "metadata": map[string]any{"severity": "High"}}}))},
		httpfake.Route{Match: "GET " + security + "/pricings", Reply: httpfake.JSON(map[string]any{"value": []any{
			map[string]any{"name": "VirtualMachines", "properties": map[string]any{"pricingTier": "Standard"}}}})},
		httpfake.Route{Match: "GET /subscriptions", Reply: httpfake.JSON(armfake.Page("", map[string]any{"subscriptionId": testSubscription,
			"displayName": "Dev", "state": "Enabled", "tenantId": testTenant}))},
		httpfake.Route{Match: "POST /providers/Microsoft.ResourceGraph/resources", Reply: httpfake.JSON(map[string]any{"data": []any{
			map[string]any{"name": "kv1", "type": "microsoft.keyvault/vaults"}}})},
		httpfake.Route{Match: "GET /v1.0/users/ana@corp.example", Reply: httpfake.JSON(graphfake.User(10, "ana@corp.example", "Ana"))},
	)
}

func TestAzureSubscriptionsAndResourceGraph(t *testing.T) {
	h := newHarness(t, arm())
	contains(t, h.ok("azure", "subscriptions"), "Dev           "+testSubscription+"  Enabled")
	contains(t, h.ok("azure", "resource-graph", "resources | take 1"), "kv1   microsoft.keyvault/vaults")
	contains(t, h.err.String(), "1 row(s)")
	if !strings.Contains(h.transport.Seen()[len(h.transport.Seen())-1].Body, testSubscription) {
		t.Error("the profile's subscription was not the scope")
	}
}

func TestAzureRBACByUPN(t *testing.T) {
	h := newHarness(t, arm())
	out := h.ok("azure", "rbac", "ana@corp.example")
	contains(t, out, "Ana (user)  object "+graphfake.ID(10), "Reader  /subscriptions/"+testSubscription+"  this principal")
	contains(t, h.err.String(), "1 assignment(s) across 1 subscription(s)")
}

func TestAzureDefenderForCloud(t *testing.T) {
	h := newHarness(t, arm())
	contains(t, h.ok("azure", "secure-score", "--controls"), "12.5   20   62%", "controls for", "Enable MFA  10.00")
	contains(t, h.ok("azure", "recommendations"), "High      Unhealthy  Encrypt disks")
	contains(t, usageError(h.fails(2, "azure", "recommendations", "--severity", "urgent")), "--severity must be high, medium or low")
	contains(t, h.ok("azure", "defender-plans"), "VirtualMachines  on")
}

func TestAzureParseID(t *testing.T) {
	h := newHarness(t, nil)
	id := armfake.Group("rg") + "/providers/Microsoft.KeyVault/vaults/kv1/secrets/s1"
	contains(t, h.ok("azure", "parse-id", id), "s1    Microsoft.KeyVault/vaults/secrets  rg")
	h.stdin = id + "\nnot-an-id\n"
	contains(t, h.fails(1, "azure", "parse-id", "-"), "1 of 2 id(s) are not Azure resource ids")
	contains(t, h.out.String(), "s1")
	contains(t, h.fails(1, "azure", "parse-id", "nope"), "not an Azure resource id")
}

func TestAutomationJobsLogsAndOutput(t *testing.T) {
	h := newHarness(t, arm())
	contains(t, h.ok("azure", "automation", "accounts"), "aa-ops   rg-ops          uksouth")
	if code := h.run("azure", "automation", "jobs", "aa-ops"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "j2   Patch    Failed", "j1   Tidy     Completed", "2m 30s", "Azure")
	contains(t, h.err.String(), "2 job(s) in aa-ops, 1 failed")
	h.ok("azure", "automation", "jobs", "aa-ops", "--runbook", "tidy", "--since", "2d")
	contains(t, h.err.String(), "1 job(s) in aa-ops")
	if code := h.run("azure", "automation", "logs", "aa-ops", "--stream", "error"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "Job      j2", "Error   boom")
	if strings.Contains(h.out.String(), "hello") {
		t.Error("an output record was shown")
	}
	contains(t, h.fails(1, "azure", "automation", "logs", "aa-ops", "--stream", "trace"), "unknown stream 'trace'")
	if out := h.ok("azure", "automation", "output", "aa-ops", "j2"); out != "hello\n" {
		t.Errorf("%q", out)
	}
	contains(t, h.fails(1, "azure", "automation", "logs", "aa-ops", "--runbook", "Nothing"), "no jobs for runbook 'Nothing'")
}

func TestAzureWithoutAPinnedSubscriptionCoversTheTenant(t *testing.T) {
	h := newHarness(t, func(r *http.Request) httpfake.Reply { return arm()(r) })
	h.ok("azure", "defender-plans", "-p", "other")
	if h.transport.Paths()[0] != "GET /subscriptions" {
		t.Error(h.transport.Paths())
	}
}
