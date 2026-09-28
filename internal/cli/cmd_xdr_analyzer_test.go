package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

var analyzerZips = filepath.Join("..", "microsoft", "analyzer", "testdata")

func TestAnalyzerShowsEachDevicesFindingsWorstFirst(t *testing.T) {
	h := newHarness(t, nil)
	code := h.run("xdr", "analyzer", filepath.Join(analyzerZips, "MDEClientAnalyzerResult.zip"), filepath.Join(analyzerZips, "mde_support.zip"))
	if code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	lines := strings.Split(h.out.String(), "\n")
	contains(t, lines[2], "web01", "error", "SenseService", "Sense is stopped")
	contains(t, h.out.String(), "db01.corp.example  error", "EDR Cloud Cyber")
	contains(t, h.err.String(), "web01 (windows, MDEClientAnalyzerResult.zip): 1 error, 1 warning, 1 informational",
		"db01.corp.example (linux, mde_support.zip): 1 error, 1 warning, 2 informational")
}

func TestAnalyzerGuidanceSeverityAndFacts(t *testing.T) {
	h := newHarness(t, nil)
	windows := filepath.Join(analyzerZips, "MDEClientAnalyzerResult.zip")
	h.run("xdr", "analyzer", windows, "--severity", "warning", "--guidance")
	contains(t, h.out.String(), "GUIDANCE", "Update it: Updates (https://learn.microsoft.com/x) & more")
	if strings.Contains(h.out.String(), "informational") {
		t.Error("an informational finding was kept")
	}
	h.run("xdr", "analyzer", windows, "--facts")
	contains(t, h.out.String(), "EDR        Sense service Status", "High")
	contains(t, h.fails(1, "xdr", "analyzer", windows, "--severity", "bad"), "'bad' is not a severity")
	contains(t, usageError(h.fails(2, "xdr", "analyzer", "/nowhere.zip")), "does not exist")
}
