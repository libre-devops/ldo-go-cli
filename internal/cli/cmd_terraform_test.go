package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/terraformfake"
)

// terraformHarness is a harness with tools on its PATH, and a module in a folder of its
// own.
func terraformHarness(t *testing.T, tools *terraformfake.Tools) (*harness, string) {
	t.Helper()
	h := newHarness(t, nil)
	h.rt.LookPath, h.rt.Runner = tools.LookPath, tools
	return h, terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTerraformSortPutsAFoldersVariablesAndOutputsInOrderThenFormatsIt(t *testing.T) {
	tools := terraformfake.New("tofu")
	tools.Formatted = []string{"variables.tf"}
	h, module := terraformHarness(t, tools)
	t.Chdir(module)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(h.ok("terraform", "sort", "-o", "json")), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["file"] != "variables.tf" || rows[0]["blocks"] != "variables" || rows[0]["count"] != 3.0 ||
		rows[0]["state"] != "sorted" || rows[1]["file"] != "outputs.tf" || rows[1]["count"] != 2.0 {
		t.Error(rows)
	}
	if !strings.HasPrefix(readFile(t, filepath.Join(module, "variables.tf")), "variable \"Environment\"") {
		t.Error("the variables were not sorted")
	}
	contains(t, h.err.String(), "sorted 2 of 2; the rest were in order", "tofu fmt formatted 1 file(s)")
	if calls := tools.Calls(); len(calls) != 1 || strings.Join(calls[0], " ") != "/usr/bin/tofu fmt ." {
		t.Error(calls)
	}
}

func TestTerraformSortCanTakeOnlyTheInputsOrOutputsAndTheFoldersBeneath(t *testing.T) {
	tools := terraformfake.New()
	h, module := terraformHarness(t, tools)
	lines := strings.Split(strings.TrimSpace(h.ok("terraform", "sort", module, "--inputs", "-r", "--no-fmt", "-o", "csv")), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[1], ",variables,3,sorted") || !strings.HasSuffix(lines[2], ",variables,2,sorted") {
		t.Error(lines)
	}
	if len(tools.Calls()) != 0 {
		t.Error(tools.Calls())
	}
	lines = strings.Split(strings.TrimSpace(h.ok("terraform", "sort", module, "--outputs", "-o", "csv")), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[1], ",outputs,2,sorted") {
		t.Error(lines)
	}
	contains(t, h.err.String(), "terraform fmt not run: neither terraform nor tofu is on PATH")
	if hidden := readFile(t, filepath.Join(module, ".terraform", "modules", "other", "variables.tf")); !strings.HasPrefix(hidden, "variable \"z\"") {
		t.Error("a downloaded module was sorted")
	}
}

func TestTerraformSortCheckChangesNothingAndExits3WhenOutOfOrder(t *testing.T) {
	tools := terraformfake.New("terraform")
	h, module := terraformHarness(t, tools)
	outputs := filepath.Join(module, "outputs.tf")
	h.fails(3, "terraform", "sort", outputs, "--check", "-o", "csv")
	if lines := strings.Split(strings.TrimSpace(h.out.String()), "\n"); lines[1] != outputs+",variables,0,in order" || lines[2] != outputs+",outputs,2,out of order" {
		t.Error(lines)
	}
	contains(t, h.err.String(), "1 of 2 not in order: run without --check")
	if readFile(t, outputs) != terraformfake.Outputs || len(tools.Calls()) != 0 {
		t.Error("a check changed something")
	}
	h.ok("terraform", "sort", module, "--no-fmt")
	h.ok("terraform", "sort", module, "--check")
	contains(t, h.err.String(), "0 of 2 not in order")
}

func TestTerraformSortSaysWhatItCannotSort(t *testing.T) {
	h, module := terraformHarness(t, terraformfake.New())
	contains(t, h.fails(1, "terraform", "sort", filepath.Join(module, "nowhere")), "is not a file or a folder")
	broken := filepath.Join(module, "broken.tf")
	_ = os.WriteFile(broken, []byte("variable \"a\" {\n"), 0o644)
	contains(t, h.fails(1, "terraform", "sort", broken), "broken.tf: its braces", "terraform validate")
	tools := terraformfake.New("terraform")
	failing, module := terraformHarness(t, tools)
	failing.rt.Runner = failingTool{}
	contains(t, failing.fails(1, "terraform", "sort", module), "terraform fmt")
}

func TestTerraformDocsWritesEachReadmeFromItsHeaderAndTerraformDocs(t *testing.T) {
	h, module := terraformHarness(t, terraformfake.New("terraform-docs"))
	var rows []map[string]any
	if err := json.Unmarshal([]byte(h.ok("terraform", "docs", module, "-r", "-o", "json")), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["header"] == nil || rows[0]["state"] != "updated" || rows[1]["state"] != "updated" {
		t.Error(rows)
	}
	if got := readFile(t, filepath.Join(module, "README.md")); got != terraformfake.Header+"\n"+terraformfake.Generated {
		t.Errorf("%q", got)
	}
	if got := readFile(t, filepath.Join(module, "examples", "minimal", "README.md")); got != "# The minimal example\n\n"+terraformfake.Generated {
		t.Errorf("%q", got)
	}
	if lines := strings.Split(h.ok("terraform", "docs", module, "-r", "--check", "-o", "csv"), "\n"); !strings.HasSuffix(lines[1], ",HEADER.md,up to date") {
		t.Error(lines)
	}
	_ = os.WriteFile(filepath.Join(module, "HEADER.md"), []byte("# Changed\n"), 0o644)
	h.fails(3, "terraform", "docs", module, "--check")
	contains(t, h.err.String(), "1 of 1 out of date: run without --check")
	contains(t, h.fails(1, "terraform", "docs", filepath.Join(module, "HEADER.md")), "is not a folder")
}

func TestTerraformDocsNeedsTerraformDocsAndPlainFileNames(t *testing.T) {
	h, module := terraformHarness(t, terraformfake.New())
	contains(t, h.fails(1, "terraform", "docs", module), "terraform-docs is not on PATH", "https://terraform-docs.io/user-guide/installation/")
	for _, option := range []string{"--header", "--readme"} {
		contains(t, usageError(h.fails(2, "terraform", "docs", module, option, "../x.md")), "give a file name")
	}
	_ = os.WriteFile(filepath.Join(module, "README.md"), []byte("<!-- BEGIN_TF_DOCS -->\n"), 0o644)
	withTool, _ := terraformHarness(t, terraformfake.New("terraform-docs"))
	contains(t, withTool.fails(1, "terraform", "docs", module), "README.md: it has")
}

// failingTool fails every run, as terraform fmt does on a file it cannot read.
type failingTool struct{}

func (failingTool) Run(context.Context, process.Request) (process.Result, error) {
	return process.Result{ExitCode: 2, Stderr: "Error: Invalid character\n"}, nil
}
