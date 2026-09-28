package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

func incidentQueue() httpfake.Handler {
	return httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/security/incidents/42", Reply: httpfake.JSON(graphfake.Incident("42", "high", "inProgress",
			testNow.Add(-time.Hour), graphfake.IncidentAlert("a1", "microsoftSentinel")))},
		httpfake.Route{Match: "GET /v1.0/security/incidents", Reply: httpfake.JSON(graphfake.Page(
			graphfake.Incident("1", "medium", "active", testNow.Add(-time.Hour), graphfake.IncidentAlert("a1", "microsoftSentinel")),
			graphfake.Incident("2", "high", "inProgress", testNow.Add(-3*time.Hour), graphfake.IncidentAlert("a2", "microsoftDefenderForEndpoint")),
		))},
	)
}

func TestIncidentsTopIsMostSevereFirst(t *testing.T) {
	h := newHarness(t, incidentQueue())
	out := h.ok("xdr", "incidents", "top")
	lines := strings.Split(out, "\n")
	contains(t, lines[2], "high", "in progress", "Incident 2", "Endpoint")
	contains(t, lines[3], "medium", "Incident 1", "Sentinel")
	contains(t, h.err.String(), "2 of 2 incident(s) created today")
	filter := h.transport.Seen()[0].URL.Query().Get("$filter")
	contains(t, filter, "createdDateTime ge ", "status eq 'active' or status eq 'inProgress'")
}

func TestIncidentsListLatestAndTheirFilters(t *testing.T) {
	h := newHarness(t, incidentQueue())
	h.ok("xdr", "incidents", "latest", "-n", "1")
	contains(t, h.err.String(), "1 of 2 incident(s) created the last 30d")
	h.ok("xdr", "incidents", "list", "--source", "sentinel", "--severity", "medium", "--updated", "--from", "2026-09-20", "--to", "2026-09-24")
	contains(t, h.err.String(), "1 of 1 incident(s) updated 2026-09-20 to 2026-09-24")
	contains(t, h.transport.Seen()[len(h.transport.Seen())-1].URL.Query().Get("$filter"), "lastUpdateDateTime ge", "(severity eq 'high' or severity eq 'medium')")
	contains(t, usageError(h.fails(2, "xdr", "incidents", "list", "--status", "done")), "--status must be one of")
	contains(t, usageError(h.fails(2, "xdr", "incidents", "list", "--source", "splunk")), "--source must be one of")
	contains(t, h.fails(1, "xdr", "incidents", "list", "--severity", "severe"), "unknown severity")
}

func TestIncidentsSummary(t *testing.T) {
	h := newHarness(t, incidentQueue())
	out := h.ok("xdr", "incidents", "summary")
	contains(t, out, "2 incident(s) today", "SEVERITY  INCIDENTS", "high      1", "in progress", "Sentinel")
	var record map[string]any
	_ = json.Unmarshal([]byte(h.ok("xdr", "incidents", "summary", "-o", "json")), &record)
	if record["total"] != float64(2) || record["by_status"].(map[string]any)["active"] != float64(1) {
		t.Errorf("%v", record)
	}
	contains(t, h.ok("xdr", "incidents", "summary", "-o", "csv"), "BY,VALUE,INCIDENTS", "source,Endpoint,1")
}

func TestIncidentsShow(t *testing.T) {
	h := newHarness(t, incidentQueue())
	out := h.ok("xdr", "incidents", "show", "42")
	contains(t, out, "Incident 42: Incident 42", "Assigned to     ana@corp.example", "Devices         web01.corp.example",
		"Users           ana", "Tags            patching", "Sentinel  Alert a1")
	contains(t, h.ok("xdr", "incidents", "show", "42", "-o", "csv"), "CREATED,SEVERITY,SOURCE,TITLE,ALERT ID")
	contains(t, h.fails(1, "xdr", "incidents", "show", "forty-two"), "is not an incident id")
}
