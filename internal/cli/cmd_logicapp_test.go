package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// The expected outputs are the Python ldo's, from the same files; it was run from the
// Python repository, so its paths are rewritten to these.
const flows = "testdata/logicapps"

func pythonOutput(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(flows, "expected", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "../ldo-go-cli/internal/cli/testdata", "testdata")
}

func sameJSON(t *testing.T, name, got string) {
	t.Helper()
	var want, found any
	if err := json.Unmarshal([]byte(pythonOutput(t, name)), &want); err != nil {
		t.Fatal(err)
	}
	// The Python's output was recorded on Linux: a path the command shows (its files'
	// sources) has Windows's separators on Windows, in both tools alike.
	if os.PathSeparator == '\\' {
		got = strings.ReplaceAll(got, `\\`, "/")
	}
	if err := json.Unmarshal([]byte(got), &found); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if !reflect.DeepEqual(want, found) {
		wantText, _ := json.MarshalIndent(want, "", " ")
		gotText, _ := json.MarshalIndent(found, "", " ")
		t.Errorf("%s differs from the Python's:\ngot  %s\nwant %s", name, gotText, wantText)
	}
}

func flow(name string) string { return filepath.Join(flows, name) }

func TestLogicAppCommandsGiveThePythonsJSON(t *testing.T) {
	cases := map[string]struct {
		args []string
		code int
	}{
		"check.json": {[]string{"check", flow("code-view.json"), flow("bare.json"), flow("secure.json"), flow("template.json.tftpl"),
			"--supplied", "extra", "--callback-trigger", "missing"}, ExitAttention},
		"params.json":      {[]string{"params", flow("code-view.json"), flow("bare.json"), flow("secure.json"), "--supplied", "retry_count"}, ExitOK},
		"references.json":  {[]string{"references", flow("references.json"), flow("code-view.json"), flow("bare.json")}, ExitOK},
		"connections.json": {[]string{"connections", flow("code-view.json")}, ExitOK},
		"order.json":       {[]string{"order", flow("flows")}, ExitOK},
		"diff.json":        {[]string{"diff", flow("arm.json"), flow("bare.json"), "--parameters"}, ExitAttention},
	}
	for name, test := range cases {
		h := newHarness(t, nil)
		if code := h.run(append(append([]string{"logicapp"}, test.args...), "-o", "json")...); code != test.code {
			t.Errorf("%s exited %d: %s", name, code, h.err)
			continue
		}
		sameJSON(t, name, h.out.String())
	}
}

func TestLogicAppDefaultsAndRewriteWriteThePythonsText(t *testing.T) {
	h := newHarness(t, nil)
	if out := h.ok("logicapp", "defaults", flow("code-view.json")); out != pythonOutput(t, "defaults.txt") {
		t.Errorf("defaults:\n%s\nwant:\n%s", out, pythonOutput(t, "defaults.txt"))
	}
	contains(t, h.err.String(), "added 3 default(s)")
	out := h.ok("logicapp", "rewrite", flow("code-view.json"), "--replace", "00000000-0000-0000-0000-000000000000=11111111-1111-1111-1111-111111111111",
		"--replace", "rg-int=rg-new")
	if out != pythonOutput(t, "rewrite.txt") {
		t.Errorf("rewrite:\n%s", out)
	}
	target := filepath.Join(t.TempDir(), "out.json")
	h.ok("logicapp", "defaults", flow("arm.json"), "--out", target)
	contains(t, h.err.String(), "wrote "+target, "added 1 default(s)")
	contains(t, h.fails(1, "logicapp", "defaults", flow("bare.json")), "is a bare definition")
	contains(t, usageError(h.fails(2, "logicapp", "rewrite", flow("arm.json"), "--replace", "nothing")), "--replace takes OLD=NEW")
}

func TestLogicAppTablesAndMistakes(t *testing.T) {
	h := newHarness(t, nil)
	if code := h.run("logicapp", "check", flow("arm.json"), "--strict"); code != ExitOK {
		t.Fatalf("exit %d: %s%s", code, h.out, h.err)
	}
	contains(t, h.err.String(), "1 definition(s): 0 error(s), 0 warning(s)")
	contains(t, h.ok("logicapp", "params", flow("secure.json"), flow("code-view.json"), "--unsatisfied"), "api_secret", "SecureString  no")
	contains(t, h.ok("logicapp", "order", flow("flows")), "0     callee", "1     caller    callee", "2     other     caller")
	contains(t, h.fails(1, "logicapp", "check", t.TempDir()), "no definition files found")
	contains(t, h.fails(1, "logicapp", "check", flow("missing.json")), "not found")
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("[1]"), 0o600)
	contains(t, h.fails(1, "logicapp", "check", bad), "is not a JSON object")
}

func logicAppsInAzure() httpfake.Handler {
	group := armfake.Group("rg-int")
	resource := `{"id": "` + group + `/providers/Microsoft.Logic/workflows/router", "name": "router", "location": "uksouth", ` +
		`"properties": {"state": "Enabled", "definition": {"triggers": {}, "actions": {"B": {"type": "Compose"}, "A": {"type": "Compose"}}}, ` +
		`"parameters": {"p": {"value": 1}}}}`
	return httpfake.Routes(
		httpfake.Route{Match: "GET " + group + "/providers/Microsoft.Logic/workflows/router", Reply: httpfake.Reply{Status: 200,
			Body: httpfake.Text{Content: resource, ContentType: "application/json"}}},
		httpfake.Route{Match: "GET " + group + "/providers/Microsoft.Logic/workflows", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"name": "router"}, map[string]any{"name": "other"}))},
		httpfake.Route{Match: "POST " + group + "/providers/Microsoft.Logic/locations/uksouth/workflows/code-view/validate",
			Reply: httpfake.Status(400, map[string]any{"error": map[string]any{"code": "InvalidTemplate", "message": "The value for the workflow parameter 'x' is not provided."}})},
		httpfake.Route{Match: "POST " + group + "/providers/Microsoft.Logic/locations/uksouth/workflows/", Reply: httpfake.Reply{Status: 200}},
		httpfake.Route{Match: "GET " + group, Reply: httpfake.JSON(map[string]any{"location": "uksouth"})},
	)
}

func TestLogicAppExportKeepsTheOrderAzureGave(t *testing.T) {
	h := newHarness(t, logicAppsInAzure())
	folder := filepath.Join(t.TempDir(), "export")
	contains(t, h.ok("logicapp", "export", "-g", "rg-int", "--out", folder, "--name", "ROUTER"), "router")
	written, _ := os.ReadFile(filepath.Join(folder, "router.json"))
	want := "{\n  \"definition\": {\n    \"triggers\": {},\n    \"actions\": {\n      \"B\": {\n        \"type\": \"Compose\"\n      },\n" +
		"      \"A\": {\n        \"type\": \"Compose\"\n      }\n    }\n  },\n  \"parameters\": {\n    \"p\": {\n      \"value\": 1\n    }\n  }\n}\n"
	if string(written) != want {
		t.Errorf("%s", written)
	}
	h.ok("logicapp", "export", "-g", "rg-int", "--out", folder, "--name", "router", "--shape", "arm")
	written, _ = os.ReadFile(filepath.Join(folder, "router.json"))
	contains(t, string(written), `"state": "Enabled"`)
	h.ok("logicapp", "export", "-g", "rg-int", "--out", folder, "--name", "nothing")
	contains(t, h.err.String(), "no Logic App workflows matched")
	contains(t, usageError(h.fails(2, "logicapp", "export", "-g", "rg-int", "--out", folder, "--shape", "zip")), "--shape must be")
}

func TestLogicAppValidateIsTheProvidersVerdict(t *testing.T) {
	h := newHarness(t, logicAppsInAzure())
	if code := h.run("logicapp", "validate", flow("code-view.json"), "-g", "rg-int"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "code-view  uksouth   no", "is not provided")
	contains(t, h.ok("logicapp", "validate", flow("arm.json"), "-g", "rg-int", "--location", "uksouth"), "logic-arm  uksouth   yes    accepted")
	contains(t, h.fails(1, "logicapp", "validate", flow("arm.json"), "-g", "rg-int", "--location", "UK South"), "is not an Azure region name")
}
