package terraform

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
)

// The command line tools run on a module: terraform (or OpenTofu's tofu) to format it,
// and terraform-docs to write its README's generated section. Neither ships with this
// tool: each is found on PATH, and one that is missing says how to install it.

// Formatters are the tools that format a module: terraform first, as the module's own
// tool; OpenTofu's formats the same.
var Formatters = []string{"terraform", "tofu"}

// TerraformDocsHint is how to install terraform-docs.
const TerraformDocsHint = "install terraform-docs: https://terraform-docs.io/user-guide/installation/"

// Formatter is terraform, else tofu, as found on PATH; nil when neither is there.
func Formatter(lookPath process.LookPath, runner process.Runner) *process.Command {
	for _, name := range Formatters {
		if found, err := lookPath(name); err == nil {
			return &process.Command{Name: name, Executable: found, Runner: runner}
		}
	}
	return nil
}

// FormatCode formats each of targets (a folder, or a file) with the tool's fmt, and the
// folders beneath one too when recursive: the files it changed.
func FormatCode(ctx context.Context, tool *process.Command, targets []string, recursive bool) ([]string, error) {
	var changed []string
	for _, target := range targets {
		args := []string{"fmt", filepath.Clean(target)}
		if recursive {
			args = []string{"fmt", "-recursive", filepath.Clean(target)}
		}
		out, err := tool.Run(ctx, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				changed = append(changed, line)
			}
		}
	}
	return changed, nil
}

// TerraformDocs is terraform-docs, as found on PATH: a Command error saying how to
// install it when it is not.
func TerraformDocs(lookPath process.LookPath, runner process.Runner) (*process.Command, error) {
	found, err := lookPath("terraform-docs")
	if err != nil {
		return nil, errs.Commandf("terraform-docs is not on PATH").WithHint("%s", TerraformDocsHint)
	}
	return &process.Command{Name: "terraform-docs", Executable: found, Runner: runner}, nil
}
