package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/xdrfake"
)

// fleet answers Entra, Defender, Intune and hunting for web01 (everywhere) and web02 (in
// Entra, onboarded to Defender only once onboarded is set).
func fleet(onboarded *atomic.Bool) func() httpfake.Handler {
	return func() httpfake.Handler {
		entraDevice := func(number int, name string) map[string]any {
			device := graphfake.Device(number, name, testNow)
			device["deviceId"] = "aad-" + name
			return device
		}
		return httpfake.Routes(
			httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
				filter := r.URL.Query().Get("$filter")
				switch {
				case strings.Contains(filter, "'web01'"):
					return httpfake.JSON(graphfake.Page(entraDevice(1, "web01")))
				case strings.Contains(filter, "'web02'"):
					return httpfake.JSON(graphfake.Page(entraDevice(2, "web02")))
				}
				return httpfake.JSON(graphfake.Page())
			}},
			httpfake.Route{Match: "GET /v1.0/devices/", Reply: httpfake.JSON(graphfake.Page(graphfake.Group(50, "Servers", false)))},
			httpfake.Route{Match: "GET /api/machines", Func: func(r *http.Request) httpfake.Reply {
				filter := r.URL.Query().Get("$filter")
				if strings.Contains(filter, "'web01'") || (onboarded != nil && onboarded.Load() && strings.Contains(filter, "'web02'")) {
					machine := xdrfake.Machine('1', "web01", testNow.Add(-time.Hour))
					machine["aadDeviceId"] = "aad-web01"
					return httpfake.JSON(xdrfake.Page(machine))
				}
				return httpfake.JSON(xdrfake.Page())
			}},
			httpfake.Route{Match: "GET /v1.0/deviceManagement/managedDevices", Reply: httpfake.JSON(graphfake.Page(map[string]any{
				"deviceName": "web01", "complianceState": "compliant", "azureADDeviceId": "aad-web01"}))},
			httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery", Reply: httpfake.JSON(map[string]any{"results": []any{
				map[string]any{"DeviceName": "web01", "AvSignatureVersion": "1.419.2.0", "AvMode": "0", "SignatureUpToDate": true,
					"Reported": "2026-09-24T10:00:00Z"}}})},
		)
	}
}

func TestDevicesCheckSaysWhichExpectationEachMisses(t *testing.T) {
	h := newHarness(t, fleet(nil)())
	if code := h.run("devices", "check", "web01", "web02", "--tag", "prod"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "DEVICE  MET  ENTRA  DEFENDER            TAG PROD", "web01   3/3  ok     ok                  ok",
		"web02   1/3  ok     no Defender record  no Defender record")
	contains(t, h.err.String(), "1/2 complete (entra 2/2, defender 1/2, tag prod 1/2) (profile dev)")
	h.ok("device", "check", "web01", "--no-defender", "--intune", "--compliant")
	contains(t, h.out.String(), "INTUNE", "COMPLIANT")
	contains(t, h.fails(1, "devices", "check", "web01", "--no-entra", "--no-defender"), "nothing to check")
}

func TestDevicesWatchUntilEveryDeviceIsComplete(t *testing.T) {
	var onboarded atomic.Bool
	h := newHarness(t, fleet(&onboarded)())
	passes := 0
	h.rt.Sleep = func(ctx context.Context, wait time.Duration) error {
		passes++
		onboarded.Store(true)
		return h.clock.Sleep(ctx, wait)
	}
	out := h.ok("devices", "watch", "web01", "web02", "--interval", "90s", "--timeout", "1h")
	contains(t, out, "web02   2/2")
	contains(t, h.err.String(), "watching 2 device(s) every 1m 30s, up to 1h 00m; Ctrl-C to stop", "pass 1: 1/2 complete",
		"next pass in 1m 30s", "pass 2: 2/2 complete", "every device meets every expectation after 2 pass(es), 1m 30s")
	if passes != 1 {
		t.Errorf("%d sleeps", passes)
	}
}

func TestDevicesWatchStopsAtItsLimitOrOnCtrlC(t *testing.T) {
	h := newHarness(t, fleet(nil)())
	if code := h.run("devices", "watch", "web02", "--max-passes", "2", "--interval", "1m"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.err.String(), "reached --max-passes before every device was complete after 2 pass(es)")
	h.rt.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if code := h.run("devices", "watch", "web02"); code != ExitInterrupted {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "web02")
	contains(t, h.err.String(), "stopped")
	contains(t, usageError(h.fails(2, "devices", "watch", "web02", "--interval", "never")), "--interval")
}

func TestDevicesShowEachServiceThenTheFindings(t *testing.T) {
	h := newHarness(t, fleet(nil)())
	out := h.ok("devices", "show", "web01", "--intune")
	contains(t, out, "Entra (1 object(s))", "Groups        Servers", "Defender (1 record(s))", "Tags             prod",
		"Intune (1 record(s))", "Findings", "ok     nothing looks wrong")
	if code := h.run("devices", "show", "web02", "-o", "json"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	var record map[string]any
	_ = json.Unmarshal(h.out.Bytes(), &record)
	if record["intune"] != nil || record["findings"].([]any)[0].(map[string]any)["message"] != "no Defender record" {
		t.Errorf("%v", record)
	}
	h.ok("devices", "show", "web01", "--no-defender")
	if strings.Contains(h.out.String(), "Defender (") {
		t.Error("Defender was read")
	}
}

func TestDevicesAVSignature(t *testing.T) {
	h := newHarness(t, fleet(nil)())
	if code := h.run("devices", "av-signature", "web01", "web02", "--at-least", "1.420.0.0"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "web01   web01", "1.419.2.0", "active", "yes", "web02   not found")
	contains(t, h.err.String(), "1 found, 1 not found, 1 older than 1.420.0.0 (profile dev)")
	contains(t, h.ok("devices", "av-signature", "web01", "--show-query"), `dynamic(["web01"])`)
	contains(t, h.fails(1, "devices", "av-signature", "web01", "--at-least", "latest"), "is not a version number")
}
