package logicapps

import (
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/jsontext"
)

func TestTerraformTokensAreBlankedWhereverTheySit(t *testing.T) {
	got := TokenSafeJSON(`{"a": ${x}, "b": "p-${y}-q", "c": "\"${z}\"", "d": "$${kept}", "e": ${ {nested} }}`)
	want := `{"a": "TFTPL_TOKEN", "b": "p-TFTPL_TOKEN-q", "c": "\"TFTPL_TOKEN\"", "d": "${kept}", "e": "TFTPL_TOKEN"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestShapesAndNames(t *testing.T) {
	arm, err := Parse(`{"name": "flow", "properties": {"definition": {"triggers": {}}, "parameters": {"a": {"value": 1}}}}`, "x", "fallback")
	if err != nil || arm.Shape != "arm" || arm.Name != "flow" || len(arm.ParameterValues) != 1 {
		t.Errorf("%+v %v", arm, err)
	}
	bare, _ := Parse(`{"parameters": {"a": {"type": "String"}}}`, "x", "fallback")
	if bare.Shape != "bare" || bare.Name != "fallback" || bare.ParameterValues != nil || len(bare.Declarations()) != 1 {
		t.Errorf("%+v", bare)
	}
	for _, text := range []string{"", "  ", "{", "[1]"} {
		if _, err := Parse(text, "x", ""); !errs.Is(err, errs.Input) {
			t.Errorf("%q: %v", text, err)
		}
	}
	if WorkflowNameFrom("/a/router.json.tftpl") != "router" {
		t.Error(WorkflowNameFrom("/a/router.json.tftpl"))
	}
}

func TestNestedActionsAreAllFound(t *testing.T) {
	document, _ := Parse(`{"actions": {"If": {"type": "If", "actions": {"Yes": {}}, "else": {"actions": {"No": {}}}},
		"Switch": {"type": "Switch", "cases": {"one": {"actions": {"One": {}}}}, "default": {"actions": {"Other": {}}}}, "skip": 1}}`, "x", "")
	var paths []string
	for _, node := range ActionNodes(document.Actions(), "") {
		paths = append(paths, node.Path)
	}
	if strings.Join(paths, ",") != "If,If/Yes,If/No,Switch,Switch/Other,Switch/One" {
		t.Error(paths)
	}
}

func TestACycleHasNoDeployOrder(t *testing.T) {
	call := func(target string) string {
		return `{"definition": {"actions": {"Call": {"type": "Workflow", "inputs": {"host": {"workflow": {"id": "/x/workflows/` + target + `"}}}}}}}`
	}
	a, _ := Parse(call("b"), "a.json", "a")
	b, _ := Parse(call("a"), "b.json", "b")
	if _, err := DeployOrder([]Document{a, b}); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "cycle: a, b") {
		t.Errorf("%v", err)
	}
	if _, err := DeployOrder([]Document{a, a}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := RewriteReferences("x", [][2]string{{"", "y"}}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestSecureParametersAreNeverCopied(t *testing.T) {
	document, _ := Parse(`{"definition": {"parameters": {"secret": {"type": "SecureString"}, "kept": {"type": "String", "defaultValue": "old"}}},
		"parameters": {"secret": {"value": "hunter2"}, "kept": {"value": "new"}}}`, "x", "")
	definition, added, _ := WithParameterDefaults(document, false)
	if added != 0 || strings.Contains(fmtJSON(definition), "hunter2") {
		t.Errorf("%d %s", added, fmtJSON(definition))
	}
	definition, added, _ = WithParameterDefaults(document, true)
	if added != 1 || !strings.Contains(fmtJSON(definition), `"defaultValue": "new"`) {
		t.Errorf("%d %s", added, fmtJSON(definition))
	}
}

func fmtJSON(value any) string { return jsontext.Dumps(value, 0) }
