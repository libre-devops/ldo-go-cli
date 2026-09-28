package sort

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/terraformfake"
)

func TestVariablesSortByNameIgnoringCaseWithTheirComments(t *testing.T) {
	text, count, inOrder, err := Text(terraformfake.Variables, "variable")
	if err != nil || count != 3 || inOrder || !strings.HasPrefix(text, "variable \"Environment\" {") {
		t.Fatalf("%q %d %v %v", text, count, inOrder, err)
	}
	if !(strings.Index(text, "variable \"Environment\"") < strings.Index(text, "# The region") &&
		strings.Index(text, "# The region") < strings.Index(text, "variable \"name\"")) {
		t.Error(text)
	}
	if again, count, inOrder, _ := Text(text, "variable"); again != text || count != 3 || !inOrder {
		t.Error("sorting a sorted file changed it")
	}
	if same, count, inOrder, _ := Text(terraformfake.Variables, "output"); same != terraformfake.Variables || count != 0 || !inOrder {
		t.Error("no outputs, yet something moved")
	}
	names := []string{"b", "A", "a", "a_b", "ab"}
	slices.SortFunc(names, Compare)
	if strings.Join(names, ",") != "A,a,a_b,ab,b" {
		t.Error(names)
	}
	if _, _, _, err := Text("variable \"a\" {\n", "variable"); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestOnlyTheBlocksMoveAndALastBlockGainsItsLineBreak(t *testing.T) {
	if text, _, _, _ := Text("locals {}\n\n# loose\n\noutput \"b\" {}\n\noutput \"a\" {}", "output"); text != "locals {}\n\n# loose\n\noutput \"a\" {}\n\noutput \"b\" {}\n" {
		t.Errorf("%q", text)
	}
	if text, _, _, _ := Text("output \"b\" {}\r\noutput \"a\" {}", "output"); text != "output \"a\" {}\r\noutput \"b\" {}\r\n" {
		t.Errorf("%q", text)
	}
}

func TestAFileIsWrittenOnlyWhenAskedAndSomethingMoved(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	path := filepath.Join(module, "outputs.tf")
	if found, _ := File(path, []string{"output"}, false); !slices.Equal(found, []Sorting{{Path: path, Kind: "output", Count: 2}}) {
		t.Error(found)
	}
	if data, _ := os.ReadFile(path); string(data) != terraformfake.Outputs {
		t.Error("a check wrote the file")
	}
	found, _ := File(path, []string{"output", "variable"}, true)
	if !slices.Equal(found, []Sorting{{Path: path, Kind: "output", Count: 2, Written: true}, {Path: path, Kind: "variable", InOrder: true}}) {
		t.Error(found)
	}
	if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), "output \"fqdn\"") {
		t.Error(string(data))
	}
	if found, _ := File(path, []string{"output"}, true); !slices.Equal(found, []Sorting{{Path: path, Kind: "output", Count: 2, InOrder: true}}) {
		t.Error(found)
	}
	broken := filepath.Join(module, "broken.tf")
	_ = os.WriteFile(broken, []byte("variable \"a\" {\n"), 0o644)
	if _, err := File(broken, []string{"variable"}, true); err == nil || !strings.Contains(err.Error(), "broken.tf: its braces") {
		t.Error(err)
	}
	if _, err := File(filepath.Join(module, "missing.tf"), []string{"variable"}, true); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestTargetsAreFilesNamedOrEachFoldersVariablesAndOutputs(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	show := func(found []Target) string {
		var shown []string
		for _, target := range found {
			relative, _ := filepath.Rel(module, target.Path)
			shown = append(shown, filepath.ToSlash(relative)+":"+strings.Join(target.Kinds, "+"))
		}
		return strings.Join(shown, ",")
	}
	if found, _ := Targets([]string{module}, Kinds, false); show(found) != "variables.tf:variable,outputs.tf:output" {
		t.Error(show(found))
	}
	if found, _ := Targets([]string{module}, []string{"output"}, true); show(found) != "outputs.tf:output" {
		t.Error(show(found))
	}
	if found, _ := Targets([]string{module}, []string{"variable"}, true); show(found) != "variables.tf:variable,examples/minimal/variables.tf:variable" {
		t.Error(show(found))
	}
	main := filepath.Join(module, "main.tf")
	_ = os.WriteFile(main, nil, 0o644)
	if found, _ := Targets([]string{main}, Kinds, false); show(found) != "main.tf:variable+output" {
		t.Error(show(found))
	}
	if _, err := Targets([]string{filepath.Join(module, "missing")}, Kinds, false); err == nil || !strings.Contains(err.Error(), "is not a file or a folder") {
		t.Error(err)
	}
	_, err := Targets([]string{filepath.Join(module, "examples")}, []string{"output"}, true)
	if err == nil || err.Error() != "no outputs.tf to sort there" || errs.As(err).Hint != "name the .tf files to sort" {
		t.Error(err)
	}
}
