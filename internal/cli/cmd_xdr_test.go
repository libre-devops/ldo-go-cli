package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/xdrfake"
)

// defender answers machine lookups, alerts, vulnerabilities, indicators and hunting.
func defender() httpfake.Handler {
	return httpfake.Routes(
		httpfake.Route{Match: "GET /api/machines", Func: func(r *http.Request) httpfake.Reply {
			filter := r.URL.Query().Get("$filter")
			switch {
			case filter == "computerDnsName eq 'web01.corp.example'":
				return httpfake.JSON(xdrfake.Page(xdrfake.Machine('1', "web01.corp.example", testNow.Add(-time.Hour)),
					xdrfake.Machine('2', "web01.corp.example", testNow.Add(-30*24*time.Hour))))
			case filter == "computerDnsName eq 'web02'":
				return httpfake.JSON(xdrfake.Page(xdrfake.Machine('3', "web02", testNow.Add(-time.Hour))))
			case strings.HasPrefix(filter, "lastSeen lt"):
				return httpfake.JSON(xdrfake.Page(xdrfake.Machine('4', "old01", testNow.Add(-60*24*time.Hour))))
			}
			return httpfake.JSON(xdrfake.Page())
		}},
		httpfake.Route{Match: "GET /api/machines/" + xdrfake.MachineID('1') + "/alerts", Reply: httpfake.JSON(xdrfake.Page(
			xdrfake.Alert("da1", "High", "New", testNow.Add(-time.Hour))))},
		httpfake.Route{Match: "GET /api/machines/" + xdrfake.MachineID('1') + "/vulnerabilities", Reply: httpfake.JSON(xdrfake.Page(
			map[string]any{"id": "CVE-2026-1", "severity": "Critical", "cvssV3": 9.8, "publicExploit": true, "name": "Remote code"},
			map[string]any{"id": "CVE-2026-2", "severity": "Low", "name": "Minor"}))},
		httpfake.Route{Match: "GET /api/alerts", Reply: httpfake.JSON(xdrfake.Page(xdrfake.Alert("da2", "Medium", "New", testNow.Add(-2*time.Hour)),
			xdrfake.Alert("da3", "Low", "Resolved", testNow.Add(-3*time.Hour))))},
		httpfake.Route{Match: "GET /api/indicators", Reply: httpfake.JSON(xdrfake.Page(map[string]any{"indicatorType": "IpAddress",
			"indicatorValue": "192.0.2.1", "action": "Block", "severity": "High", "title": "Bad host"}))},
		httpfake.Route{Match: "POST /api/advancedqueries/run", Reply: httpfake.JSON(map[string]any{"Schema": []any{map[string]any{"Name": "DeviceName"}},
			"Results": []any{map[string]any{"DeviceName": "web01"}}})},
		httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery", Reply: httpfake.JSON(map[string]any{"results": []any{
			map[string]any{"Timestamp": "2026-09-24T11:00:00Z", "Type": "process", "ActionType": "ProcessCreated", "Detail": "cmd.exe /c whoami",
				"Account": "ana", "Process": "explorer.exe", "DeviceName": "web01", "DeviceId": "a", "Id": "1"},
			map[string]any{"Timestamp": "2026-09-24T11:30:00Z", "Type": "alert", "ActionType": "High", "Detail": "Suspicious",
				"DeviceName": "web01", "DeviceId": "b", "Id": "da1"},
		}})},
	)
}

func TestXdrMachinesFindEachAndExitThreeForAMissingOne(t *testing.T) {
	h := newHarness(t, defender())
	if code := h.run("xdr", "machines", "web01.corp.example", "web02.corp.example", "nope", "--all-records"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "web01.corp.example  fqdn", "Onboarded", "  older record", "web02.corp.example  short",
		"nope                not found", "prod", "Servers")
	contains(t, h.err.String(), "2 of 3 found in Defender (profile dev)")
	h.ok("xdr", "machines", "web01.corp.example")
}

func TestXdrStaleExitsThreeWhenAnyAreSilent(t *testing.T) {
	h := newHarness(t, defender())
	if code := h.run("xdr", "stale", "--older-than", "30d"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "old01")
	contains(t, h.err.String(), "1 machine(s) not seen for 30d")
}

func TestXdrAlertsForTheTenantAndADevice(t *testing.T) {
	h := newHarness(t, defender())
	out := h.ok("xdr", "alerts")
	contains(t, out, "Medium", "Suspicious da2")
	if strings.Contains(out, "da3") {
		t.Error("a resolved alert was shown")
	}
	contains(t, h.ok("xdr", "alerts", "--include-resolved"), "da3")
	contains(t, h.ok("xdr", "alerts", "-d", "web01.corp.example"), "Suspicious da1")
	contains(t, h.fails(1, "xdr", "alerts", "-d", "nope"), "no Defender record for 'nope'")
	contains(t, h.fails(1, "xdr", "alerts", "--severity", "severe"), "unknown severity 'severe'")
}

func TestXdrVulnsMostSevereFirst(t *testing.T) {
	h := newHarness(t, defender())
	out := h.ok("xdr", "vulns", "web01.corp.example")
	contains(t, out, "CVE-2026-1  Critical  9.8   public", "CVE-2026-2")
	contains(t, h.err.String(), "2 vulnerabilities on web01.corp.example")
	if strings.Contains(h.ok("xdr", "vulns", "web01.corp.example", "--severity", "high"), "CVE-2026-2") {
		t.Error("a low one was kept")
	}
	contains(t, h.err.String(), "1 vulnerability on")
}

func TestXdrIndicators(t *testing.T) {
	h := newHarness(t, defender())
	contains(t, h.ok("xdr", "indicators"), "IpAddress  192.0.2.1  Block")
}

func TestXdrHuntThroughGraphOrTheEndpoint(t *testing.T) {
	h := newHarness(t, defender())
	h.ok("xdr", "hunt", "DeviceInfo", "--endpoint")
	contains(t, h.out.String(), "DeviceName", "web01")
	if h.transport.Paths()[0] != "POST /api/advancedqueries/run" {
		t.Error(h.transport.Paths())
	}
	contains(t, usageError(h.fails(2, "xdr", "hunt", "DeviceInfo", "--endpoint", "--timespan", "7d")), "--timespan is not available")
	h.ok("xdr", "hunt", "DeviceInfo")
	if h.transport.Paths()[len(h.transport.Paths())-1] != "POST /v1.0/security/runHuntingQuery" {
		t.Error(h.transport.Paths())
	}
}

func TestXdrTimelineNewestFirstWithItsWarnings(t *testing.T) {
	h := newHarness(t, defender())
	out := h.ok("xdr", "timeline", "web01", "--limit", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[2], "alert") || !strings.Contains(lines[3], "cmd.exe /c whoami") {
		t.Errorf("%s", out)
	}
	contains(t, h.err.String(), "2 event(s) on web01, the last 1d (profile dev)", "stopped at the newest 2", "2 devices have that name")
	var records []map[string]any
	h.ok("xdr", "timeline", "web01", "-o", "json", "--today")
	_ = json.Unmarshal(h.out.Bytes(), &records)
	if records[0]["type"] != "alert" || records[0]["time"] != "2026-09-24T11:30:00Z" {
		t.Errorf("%v", records)
	}
	contains(t, h.ok("xdr", "timeline", "web01", "--type", "file", "--show-query"), "DeviceFileEvents")
	contains(t, h.fails(1, "xdr", "timeline", "web01", "--endpoint", "--type", "alert"), "alerts are not in the Defender for Endpoint API")
	contains(t, h.fails(1, "xdr", "timeline", "web01", "--today", "--since", "1h"), "choose one time window")
	contains(t, usageError(h.fails(2, "xdr", "timeline", "web01", "--limit", "0")), "--limit")
	h.ok("xdr", "timeline", "web01", "--endpoint", "--from", "2026-08-01")
	contains(t, h.err.String(), "alerts are left out", "Advanced Hunting keeps 30 days")
}
