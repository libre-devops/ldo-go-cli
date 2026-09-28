package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

func detectionRule(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "microsoft", "detections", "testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rule map[string]any
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

func detectionRules(t *testing.T) func() httpfake.Handler {
	modern, legacy, auto := detectionRule(t, "modern"), detectionRule(t, "legacy"), detectionRule(t, "auto")
	return func() httpfake.Handler {
		return httpfake.Routes(
			httpfake.Route{Match: "GET /beta/security/rules/detectionRules/42", Reply: httpfake.JSON(legacy)},
			httpfake.Route{Match: "GET /beta/security/rules/detectionRules", Reply: httpfake.JSON(map[string]any{"value": []any{modern, legacy, auto}})},
		)
	}
}

func TestDetectionsListExitsThreeWhenDefenderTurnedOneOff(t *testing.T) {
	h := newHarness(t, detectionRules(t)())
	if code := h.run("xdr", "detections", "list"); code != ExitAttention {
		t.Fatalf("exit %d", code)
	}
	contains(t, h.out.String(), "Auto off", "autoDisabled", "every 3h", "CommandAndControl", "Old style rule")
	contains(t, h.err.String(), "3 rule(s): 1 turned off by Defender, 1 enabled, 1 disabled (profile dev)",
		"Defender turned 1 rule(s) off itself")
	h.ok("xdr", "detections", "list", "--status", "enabled", "--severity", "medium")
	if strings.Contains(h.out.String(), "Old style") {
		t.Error("a disabled rule was listed")
	}
	contains(t, usageError(h.fails(2, "xdr", "detections", "list", "--status", "paused")), "paused: use enabled, disabled, autodisabled")
}

func TestDetectionsShowAsPairsOrYAML(t *testing.T) {
	h := newHarness(t, detectionRules(t)())
	out := h.ok("xdr", "detections", "show", "certutil used to download remote content")
	contains(t, out, "Schedule     every 3h", "Techniques   T1105, T1059, T1059.001", "Query\nDeviceProcessEvents")
	yaml := h.ok("xdr", "detections", "show", "42", "--yaml", "--no-id")
	contains(t, yaml, "display_name: Old style rule", "by ldo-go xdr detections export on 2026-09-24 12:00Z.")
	if strings.Contains(yaml, "id: \"42\"") {
		t.Error("--no-id kept the id")
	}
	contains(t, h.err.String(), "warning: review: legacy impactedAssets")
	contains(t, h.ok("xdr", "detections", "show", "42", "-o", "csv"), "Old style rule,disabled,every 12h,low,42")
}

func TestDetectionsExportWritesAFilePerRule(t *testing.T) {
	h := newHarness(t, detectionRules(t)())
	folder := filepath.Join(t.TempDir(), "rules")
	out := h.ok("xdr", "detections", "export", folder)
	contains(t, out, "command-and-control/certutil-used-to-download-remote-content.yaml", "execution/old-style-rule.yaml", "written")
	contains(t, h.err.String(), "wrote 3 of 3 rule(s)", "2 file(s) have TODO(export) comments")
	text, err := os.ReadFile(filepath.Join(folder, "execution", "old-style-rule.yaml"))
	if err != nil || !strings.Contains(string(text), "TODO(export)") {
		t.Errorf("%s %v", text, err)
	}
	h.ok("xdr", "detections", "export", folder, "--name", "42")
	contains(t, h.out.String(), "kept")
	contains(t, h.err.String(), "kept 1 file(s)")
}
