package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

const testWorkspace = "aaaaaaaa-1111-2222-3333-444444444444"

// vaultsAndWorkspaces answers two vaults (one refused) and a workspace by name.
func vaultsAndWorkspaces() httpfake.Handler {
	epoch := func(at time.Time) float64 { return float64(at.Unix()) }
	workspace := map[string]any{"id": armfake.Group("rg-soc") + "/providers/Microsoft.OperationalInsights/workspaces/law-soc",
		"name": "law-soc", "properties": map[string]any{"customerId": testWorkspace}}
	usage := map[string]any{"tables": []any{map[string]any{
		"columns": []any{map[string]any{"name": "DataType"}, map[string]any{"name": "LastData"}, map[string]any{"name": "Megabytes"},
			map[string]any{"name": "BillableMegabytes"}, map[string]any{"name": "Solutions"}},
		"rows": []any{[]any{"Syslog", "2026-09-20T00:00:00Z", 1500.0, 1500.0, "LogManagement"},
			[]any{"Perf", "2026-09-24T11:00:00Z", 2500000.0, 0.0, ""}}}}}
	return func(r *http.Request) httpfake.Reply {
		switch {
		case r.URL.Host == "kv-app.vault.azure.net" && r.URL.Path == "/secrets":
			return httpfake.JSON(map[string]any{"value": []any{
				map[string]any{"id": "https://kv-app.vault.azure.net/secrets/db-password", "attributes": map[string]any{"exp": epoch(testNow.Add(5 * 24 * time.Hour)), "enabled": true}},
				map[string]any{"id": "https://kv-app.vault.azure.net/secrets/old", "attributes": map[string]any{"exp": epoch(testNow.Add(-72 * time.Hour))}},
				map[string]any{"id": "https://kv-app.vault.azure.net/secrets/later", "attributes": map[string]any{"exp": epoch(testNow.Add(90 * 24 * time.Hour))}},
			}})
		case r.URL.Host == "kv-app.vault.azure.net":
			return httpfake.JSON(map[string]any{"value": []any{}})
		case r.URL.Host == "kv-locked.vault.azure.net":
			return httpfake.Status(403, map[string]any{"error": map[string]any{"code": "Forbidden", "message": "Caller is not authorized"}})
		case strings.HasPrefix(r.URL.Path, "/providers/Microsoft.ResourceGraph"):
			return httpfake.JSON(map[string]any{"data": []any{workspace}})
		case strings.HasPrefix(r.URL.Path, "/v1/workspaces/"+testWorkspace+"/query"):
			if strings.Contains(readBody(r), "Usage") {
				return httpfake.JSON(usage)
			}
			return httpfake.JSON(map[string]any{"tables": []any{map[string]any{"columns": []any{map[string]any{"name": "Computer"}},
				"rows": []any{[]any{"web01"}}}}})
		}
		return httpfake.Status(404, nil)
	}
}

func readBody(r *http.Request) string {
	data := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(data)
	return string(data)
}

func TestKeyVaultExpiryExitsThreeAndReadsTheRest(t *testing.T) {
	h := newHarness(t, vaultsAndWorkspaces())
	if code := h.run("keyvault", "expiry", "kv-app", "kv-locked"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "kv-app  secret  old", "expired 3d ago", "db-password", "5 ")
	if strings.Contains(h.out.String(), "later") {
		t.Error("an item beyond the window was shown")
	}
	contains(t, h.err.String(), "cannot read vault kv-locked", "2 item(s) expire within 30d (3 checked in 1 of 2 vault(s))")
	if code := h.run("keyvault", "expiry", "kv-locked"); code != ExitError {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.err.String(), "none of the 1 vault(s) could be read")
	contains(t, usageError(h.fails(2, "keyvault", "expiry", "kv-app", "--kind", "password")), "--kind must be one of")
	contains(t, h.fails(1, "keyvault", "expiry"), "no vaults given")
}

func TestLogsQueryByWorkspaceName(t *testing.T) {
	h := newHarness(t, vaultsAndWorkspaces())
	contains(t, h.ok("logs", "query", "Heartbeat", "-w", "law-soc"), "Computer\n--------\nweb01")
	contains(t, h.err.String(), "workspace law-soc in rg-soc, from its name: Workspace ID "+testWorkspace, "1 row(s)")
	h.ok("logs", "query", "Heartbeat", "-w", testWorkspace)
	if strings.Contains(h.err.String(), "from its") {
		t.Error("a Workspace ID was looked up")
	}
	contains(t, h.fails(1, "logs", "query", "Heartbeat"), "no workspace given")
}

func TestLogsIngestionQuietTablesFirst(t *testing.T) {
	h := newHarness(t, vaultsAndWorkspaces())
	if code := h.run("logs", "ingestion", "-w", testWorkspace); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	lines := strings.Split(h.out.String(), "\n")
	contains(t, lines[2], "Syslog", "4d 12h", "1.500")
	contains(t, lines[3], "Perf", "2,500.000", "0.000")
	contains(t, h.err.String(), "2 table(s) received data in the last 30d: 2,501.50 GB, 1.50 GB billable", "1 table(s) quiet for over 1d")
	contains(t, usageError(h.fails(2, "logs", "ingestion", "-w", testWorkspace, "--window", "forever")), "--window")
}
