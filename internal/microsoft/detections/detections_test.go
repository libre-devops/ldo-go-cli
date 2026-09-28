package detections

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var now = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) fields.Object {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rule fields.Object
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestTheExportIsThePythonOnes(t *testing.T) {
	for _, name := range []string{"modern", "legacy", "auto"} {
		want, _ := os.ReadFile(filepath.Join("testdata", name+".yaml"))
		wantPath, _ := os.ReadFile(filepath.Join("testdata", name+".path"))
		found := ExportRule(fixture(t, name), true, now, "ldo-go xdr detections export")
		if found.Text != string(want) {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, found.Text, want)
		}
		if found.Path != string(wantPath) {
			t.Errorf("%s: %s, want %s", name, found.Path, wantPath)
		}
	}
}

func TestWithoutTheIDAndWithTheSameName(t *testing.T) {
	first, second := fixture(t, "modern"), fixture(t, "modern")
	second["id"] = "../8"
	exported := ExportRules([]fields.Object{first, second}, false, now, "x")
	if exported[0].Path == exported[1].Path || !strings.HasSuffix(exported[1].Path, "-8.yaml") {
		t.Errorf("%s %s", exported[0].Path, exported[1].Path)
	}
	if strings.Contains(exported[0].Text, "\nid:") || strings.Contains(exported[0].Text, "server assigned") {
		t.Error("the id was kept")
	}
}

func TestARulesTextCannotAddKeysOrLeaveTheFolder(t *testing.T) {
	rule := fixture(t, "modern")
	rule["displayName"] = "../../etc/passwd\nstatus: enabled"
	rule["detectionAction"].(map[string]any)["alertTemplate"].(map[string]any)["tactics"] = []any{}
	found := ExportRule(rule, true, now, "x")
	if found.Path != "uncategorised/etc-passwd-status-enabled.yaml" {
		t.Error(found.Path)
	}
	if strings.Count(found.Text, "\nstatus:") != 1 {
		t.Errorf("%s", found.Text)
	}
	nameless := fixture(t, "modern")
	nameless["displayName"] = "!!!"
	if path := ExportRule(nameless, true, now, "x").Path; path != "command-and-control/rule-7506.yaml" {
		t.Error(path)
	}
}

func TestAnUnknownCategoryAndPeriodAreNoted(t *testing.T) {
	rule := fixture(t, "legacy")
	rule["schedule"] = map[string]any{"period": "5H"}
	rule["detectionAction"].(map[string]any)["alertTemplate"].(map[string]any)["category"] = "Malware"
	found := ExportRule(rule, true, now, "x")
	joined := strings.Join(found.Notes, "\n")
	for _, want := range []string{"legacy schedule period '5H'", "legacy category 'Malware' is not an ATT&CK tactic; techniques not carried: T1059."} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	noEntities := fixture(t, "modern")
	delete(noEntities["detectionAction"].(map[string]any)["alertTemplate"].(map[string]any), "entityMappings")
	if notes := ExportRule(noEntities, true, now, "x").Notes; len(notes) != 1 || !strings.Contains(notes[0], "maps no entities") {
		t.Error(notes)
	}
}

func TestFilesAreKeptUnlessForcedAndNeverWrittenThroughALink(t *testing.T) {
	folder := t.TempDir()
	exported := ExportRules([]fields.Object{fixture(t, "modern")}, true, now, "x")
	results, err := WriteRules(exported, folder, false)
	if err != nil || results[0].Result != "written" {
		t.Fatalf("%+v %v", results, err)
	}
	if results, _ = WriteRules(exported, folder, false); results[0].Result != "kept" {
		t.Error(results[0].Result)
	}
	if results, _ = WriteRules(exported, folder, true); results[0].Result != "written" {
		t.Error(results[0].Result)
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	linked := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, "command-and-control")); err != nil {
		t.Fatal(err)
	}
	if results, _ = WriteRules(exported, linked, true); results[0].Result != "refused" {
		t.Error(results[0].Result)
	}
	target := filepath.Join(t.TempDir(), "command-and-control")
	_ = os.MkdirAll(target, 0o755)
	_ = os.Symlink(filepath.Join(outside, "x"), filepath.Join(target, "certutil-used-to-download-remote-content.yaml"))
	if results, _ = WriteRules(exported, filepath.Dir(target), true); results[0].Result != "refused" {
		t.Error(results[0].Result)
	}
}

func TestModels(t *testing.T) {
	modern := RuleFrom(fixture(t, "modern"))
	if modern.Schedule() != "every 3h" || strings.Join(modern.Techniques, ",") != "T1105,T1059,T1059.001" || modern.AutoDisabled() {
		t.Errorf("%+v", modern)
	}
	legacy := RuleFrom(fixture(t, "legacy"))
	if legacy.Status != "disabled" || legacy.Frequency != "PT12H" || legacy.Tactics[0] != "Execution" {
		t.Errorf("%+v", legacy)
	}
	if (Rule{}).Schedule() != "-" || (Rule{Frequency: "PT5M"}).Schedule() != "PT5M" || StatusLabel("autoDisabled") != "turned off by Defender" {
		t.Error("labels")
	}
}

func client(t *testing.T, handler httpfake.Handler) *Client {
	t.Helper()
	httpClient, _ := httpfake.Client(handler)
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func TestRulesByNameOrID(t *testing.T) {
	modern, legacy := fixture(t, "modern"), fixture(t, "legacy")
	web10, web2 := map[string]any{"id": "3", "displayName": "web10"}, map[string]any{"id": "4", "displayName": "Web2"}
	c := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /beta/security/rules/detectionRules/42", Reply: httpfake.JSON(legacy)},
		httpfake.Route{Match: "GET /beta/security/rules/detectionRules/5", Reply: httpfake.GraphError(404, "NotFound", "no")},
		httpfake.Route{Match: "GET /beta/security/rules/detectionRules", Reply: httpfake.JSON(map[string]any{"value": []any{modern, web10, web2, web2}})},
	))
	rules, err := c.Rules(context.Background())
	if err != nil || rules[0].DisplayName != "Certutil used to download remote content" || rules[1].DisplayName != "Web2" {
		t.Errorf("%v %v", rules, err)
	}
	if found, err := c.Rule(context.Background(), "42"); err != nil || found.ID != "42" {
		t.Errorf("%+v %v", found, err)
	}
	if found, err := c.Rule(context.Background(), "CERTUTIL used to download remote content"); err != nil || found.ID != "7506" {
		t.Errorf("%+v %v", found, err)
	}
	for ref, kind := range map[string]errs.Kind{"5": errs.NotFound, "nope": errs.NotFound, "web2": errs.Ambiguous, " ": errs.Input} {
		if _, err := c.Rule(context.Background(), ref); !errs.Is(err, kind) {
			t.Errorf("%q: %v", ref, err)
		}
	}
}

func TestARefusalSaysWhichPermission(t *testing.T) {
	c := client(t, func(*http.Request) httpfake.Reply { return httpfake.GraphError(403, "Forbidden", "denied") })
	if _, err := c.Rules(context.Background()); errs.HintOf(err) != ScopeHint {
		t.Errorf("%v", err)
	}
}
