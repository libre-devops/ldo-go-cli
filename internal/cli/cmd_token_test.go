package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
)

func TestTokenChecksWhatTheFeaturesNeed(t *testing.T) {
	h := newHarness(t, nil)
	out := h.ok("entra", "token", "graph")
	contains(t, out, "graph token for profile dev (azure-cli)", "PASS    expiry", "PASS    audience",
		"WARN    permissions: hunting")
	contains(t, h.err.String(), "Signature not verified")
	if strings.Contains(out, tokenfake.JWT(h.tokens.Claims)) {
		t.Error("the token was printed without --raw")
	}
}

func TestTokenRawPrintsOnlyTheToken(t *testing.T) {
	h := newHarness(t, nil)
	if out := strings.TrimSpace(h.ok("graph", "token", "--raw")); out != tokenfake.JWT(h.tokens.Claims) {
		t.Errorf("raw %q", out)
	}
}

func TestTokenRawRefusesAFailingToken(t *testing.T) {
	h := newHarness(t, nil)
	h.tokens.Claims["exp"] = float64(testNow.Add(-time.Hour).Unix())
	stderr := h.fails(1, "graph", "token", "--raw")
	contains(t, stderr, "FAIL    expiry")
	if h.out.Len() != 0 {
		t.Errorf("stdout %q", h.out.String())
	}
}

func TestTokenStrictFailsOnAWarning(t *testing.T) {
	h := newHarness(t, nil)
	h.fails(1, "graph", "token", "--strict")
}

func TestTokenRequireNamesTheMissingOne(t *testing.T) {
	h := newHarness(t, nil)
	h.fails(1, "graph", "token", "--require", "Device.Read.All")
	contains(t, h.out.String(), "FAIL    requires Device.Read.All")
}

func TestTokenAsJSON(t *testing.T) {
	h := newHarness(t, nil)
	var record map[string]any
	if err := json.Unmarshal([]byte(h.ok("graph", "token", "-o", "json")), &record); err != nil {
		t.Fatal(err)
	}
	if record["valid"] != true || record["claims"].(map[string]any)["tid"] != testTenant {
		t.Errorf("record %v", record)
	}
	first := record["checks"].([]any)[0].(map[string]any)
	if first["name"] != "expiry" || first["status"] != "pass" {
		t.Errorf("check %v", first)
	}
}

func TestTokenForAnUnknownResource(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "entra", "token", "nope"), `unknown resource "nope"`, "use one of arm, graph")
}

func TestInspectTokenReadsStdin(t *testing.T) {
	h := newHarness(t, nil)
	h.stdin = "Bearer " + tokenfake.JWT(userClaims(testTenant))
	out := h.ok("entra", "inspect-token", "--resource", "graph", "--tenant", testTenant, "--all-claims")
	contains(t, out, "Principal     ana@corp.example (user)", "PASS    tenant", "app_displayname")
}

func TestInspectTokenWithNothingGiven(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "entra", "inspect-token"), "no token given")
}

func TestInspectTokenForAnotherTenantFails(t *testing.T) {
	h := newHarness(t, nil)
	h.fails(1, "entra", "inspect-token", tokenfake.JWT(userClaims(otherTenant)), "--tenant", testTenant)
	contains(t, h.out.String(), "FAIL    tenant")
}

func TestSignOutOfAnAzureCLIProfileSaysWhoKeepsIt(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "entra", "sign-out"), "uses the Azure CLI's sign-in", "az logout")
}

func TestSignOutOfAMemoryProfileHasNothingToForget(t *testing.T) {
	h := newHarness(t, nil)
	h.ok("entra", "sign-out", "-p", "other")
	contains(t, h.err.String(), `keeps no sign-in (token_cache = "memory")`)
}
