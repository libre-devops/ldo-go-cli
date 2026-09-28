package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Every case is a command line this build takes: each one's --help runs.
func TestEverySelfTestCaseIsACommandThisBuildHas(t *testing.T) {
	flags := selfTestFlags{device: "web01.corp.example", user: "ana@corp.example", group: "Servers", workspace: "law-ops",
		vault: "kv-ops", snow: true, atlassian: true, everything: true}
	runs, skipped, _ := flags.plan()
	if skipped != 0 || len(runs) != len(selfTestCases()) {
		t.Fatalf("%d runs, %d skipped", len(runs), skipped)
	}
	h := newHarness(t, nil)
	for _, run := range runs {
		if code := h.run(append(run.args, "--help")...); code != ExitOK {
			t.Errorf("%s: exit %d\n%s", strings.Join(run.args, " "), code, h.err)
		}
	}
}

func TestSelfTestRunsTheCasesChosenAndSaysWhatEachDid(t *testing.T) {
	h := newHarness(t, nil)
	report := filepath.Join(t.TempDir(), "report.json")
	h.ok("self-test", "--only", "welcome", "--only", "json", "--only", "config", "--only", "entra token graph", "--report", report)
	contains(t, h.out.String(), "welcome", "json --yaml", "config path", "entra token graph")
	contains(t, h.err.String(), "running 5 commands; each one's output is discarded", "1/5  ok ", "5 ok")
	var outcomes []map[string]any
	data, _ := os.ReadFile(report)
	if err := json.Unmarshal(data, &outcomes); err != nil || len(outcomes) != 5 || outcomes[0]["command"] != "welcome" ||
		outcomes[0]["exit_code"] != 0.0 || outcomes[0]["hint"] != nil {
		t.Error(string(data), err)
	}
}

func TestSelfTestSaysWhichCasesNeedNames(t *testing.T) {
	h := newHarness(t, nil)
	h.ok("self-test", "--only", "devices check", "--only", "snow", "--only", "welcome")
	contains(t, h.err.String(), "running 1 commands", "4 more need --device, --group, --snow: give them to run those too")
}

func TestARefusalIsExplainedAndACrashIsFoundWithItsLines(t *testing.T) {
	h := newHarness(t, nil)
	h.config(testConfig + "\n[servicenow]\ncolour = \"blue\"\n")
	// A refusal is the command working as it should, so the self-test still passes.
	h.ok("self-test", "--only", "snow whoami", "--snow", "--only", "graph whoami", "--only", "profiles")
	contains(t, h.out.String(), "refused: ldo-go snow whoami", "Said  ", "unknown key")
	contains(t, h.err.String(), "1 ok, 2 refused")
	h.rt.Credential = func(microsoft.Profile) (auth.TokenProvider, error) { panic("a broken credential") }
	h.config(testConfig)
	h.fails(1, "self-test", "--only", "graph whoami")
	contains(t, h.out.String(), "graph whoami  CRASH", "(at ", "CRASH: ldo-go graph whoami", "Said  string: a broken credential", "At    ")
	contains(t, h.err.String(), "1 CRASH")
	var outcomes []map[string]any
	h.fails(1, "self-test", "--only", "graph whoami", "-o", "json")
	if err := json.Unmarshal(h.out.Bytes(), &outcomes); err != nil || outcomes[0]["result"] != "CRASH" || outcomes[0]["exit_code"] != nil ||
		len(outcomes[0]["where"].([]any)) == 0 {
		t.Error(outcomes, err)
	}
}

func TestSelfTestUsesTheProfileGiven(t *testing.T) {
	h := newHarness(t, nil)
	h.tokens.Claims = userClaims(otherTenant)
	h.ok("self-test", "--only", "entra token graph", "-p", "other", "-o", "json")
	var outcomes []map[string]any
	_ = json.Unmarshal(h.out.Bytes(), &outcomes)
	if len(outcomes) != 1 || outcomes[0]["result"] != "ok" {
		t.Error(outcomes)
	}
	if got := h.env["LDO_PROFILE"]; got != "" {
		t.Error("the profile leaked into the environment:", got)
	}
}

func TestTheExplanationIsEveryErrorAndWarningWithTheirHints(t *testing.T) {
	said, hint := explanation("note: read 3\nwarning: one was odd\nerror: it stopped\nhint: try again\nerror: it stopped\n3 read\n")
	if said != "warning: one was odd\nerror: it stopped" || hint == nil || *hint != "try again" {
		t.Error(said, hint)
	}
	if said, hint := explanation("\n12 device(s)\n\n"); said != "12 device(s)" || hint != nil {
		t.Error(said, hint)
	}
	if said, hint := explanation("warning: only this\n"); said != "warning: only this" || hint != nil {
		t.Error(said, hint)
	}
	if got := lastLine(strings.Repeat("x", 300)); len(got) != 200 {
		t.Error(len(got))
	}
}
