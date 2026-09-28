package terraform

import (
	"context"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/terraformfake"
)

const module = `# Loose note, a blank line above the block, so it stays where it is.

// Documents the block below it, so moves with it.
/* And so
   does this. */
variable "a" {
  description = <<-EOT
  A brace on a line of its own:
}
  EOT
  default = "${ {x = "}"}["x"] }$${not a template}"
}
variable b {}
locals {
  map = { "}" = "{" } # a { in a comment
}
`

func TestAFileSplitsIntoItsBlocksAndWhatLiesBetweenThem(t *testing.T) {
	pieces, err := Split(module)
	if err != nil {
		t.Fatal(err)
	}
	var whole strings.Builder
	var kinds []string
	for _, piece := range pieces {
		whole.WriteString(piece.Text)
		kinds = append(kinds, piece.Kind+":"+piece.Name)
	}
	if whole.String() != module || strings.Join(kinds, ",") != ":,variable:a,variable:b,locals:" {
		t.Errorf("%v", kinds)
	}
	if pieces[0].Text != "# Loose note, a blank line above the block, so it stays where it is.\n\n" {
		t.Errorf("%q", pieces[0].Text)
	}
	if !strings.HasPrefix(pieces[1].Text, "// Documents the block below it") || !strings.HasSuffix(pieces[1].Text, "$${not a template}\"\n}\n") {
		t.Errorf("%q", pieces[1].Text)
	}
}

func TestALabelCanBeEscapedAndAFileCanEndWithoutALineBreak(t *testing.T) {
	pieces, err := Split("output \"a\\\"b\" {\n  value = 1\n}")
	if err != nil || len(pieces) != 1 || pieces[0] != (Piece{Text: "output \"a\\\"b\" {\n  value = 1\n}", Kind: "output", Name: "a\\\"b"}) {
		t.Errorf("%+v %v", pieces, err)
	}
	if pieces, _ := Split(""); len(pieces) != 0 {
		t.Error(pieces)
	}
}

func TestBracesStringsCommentsAndHeredocsMustClose(t *testing.T) {
	for name, text := range map[string]string{
		"open":    "variable \"a\" {\n",
		"extra":   "variable \"a\" {\n}\n}\n",
		"string":  "variable \"a\" {\n  default = \"open\n}\n",
		"comment": "/* never closed\n",
		"heredoc": "variable \"a\" {\n  description = <<EOT\nno end\n}\n",
	} {
		if _, err := Split(text); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "do not all close") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTerraformFormatsAndTofuStandsInWhenItIsMissing(t *testing.T) {
	both := terraformfake.New("terraform", "tofu")
	if tool := Formatter(both.LookPath, both); tool == nil || tool.Name != "terraform" {
		t.Fatal(tool)
	}
	tofu := terraformfake.New("tofu")
	tool := Formatter(tofu.LookPath, tofu)
	if tool.Name != "tofu" {
		t.Fatal(tool.Name)
	}
	tofu.Formatted = []string{"variables.tf"}
	ctx := context.Background()
	if changed, _ := FormatCode(ctx, tool, []string{"a", "b.tf"}, false); strings.Join(changed, ",") != "variables.tf,variables.tf" {
		t.Error(changed)
	}
	if changed, _ := FormatCode(ctx, tool, []string{"a/"}, true); strings.Join(changed, ",") != "variables.tf" {
		t.Error(changed)
	}
	var calls []string
	for _, call := range tofu.Calls() {
		calls = append(calls, strings.Join(call[1:], " "))
	}
	if strings.Join(calls, "|") != "fmt a|fmt b.tf|fmt -recursive a" {
		t.Error(calls)
	}
	if Formatter(terraformfake.New().LookPath, nil) != nil {
		t.Error("a formatter from nothing")
	}
	failing := &process.Command{Name: "tofu", Executable: "/usr/bin/tofu", Runner: failingRunner{}}
	if _, err := FormatCode(ctx, failing, []string{"a"}, false); err == nil || !strings.Contains(err.Error(), "tofu fmt a failed: Error: bad syntax") {
		t.Error(err)
	}
}

// failingRunner fails every run, as a tool refusing a broken file does.
type failingRunner struct{}

func (failingRunner) Run(context.Context, process.Request) (process.Result, error) {
	return process.Result{ExitCode: 3, Stderr: "Error: bad syntax\n"}, nil
}

func TestTerraformDocsMustBeOnPath(t *testing.T) {
	tools := terraformfake.New("terraform-docs")
	if tool, err := TerraformDocs(tools.LookPath, tools); err != nil || tool.Name != "terraform-docs" {
		t.Fatal(tool, err)
	}
	_, err := TerraformDocs(terraformfake.New().LookPath, nil)
	if !errs.Is(err, errs.Command) || err.Error() != "terraform-docs is not on PATH" || errs.As(err).Hint != TerraformDocsHint {
		t.Error(err)
	}
}
