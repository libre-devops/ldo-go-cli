// Package terraformfake is a made-up Terraform module on disk, and stand-ins for the
// tools run on it: terraform fmt and terraform-docs, found on a fake PATH only when a test
// says they are there.
package terraformfake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/process"
)

// The module's files, as written: its variables and outputs out of order.
const (
	Variables = `# The region, as Azure names it.
variable "location" {
  type        = string
  description = <<-EOT
  Where it goes.
}
  EOT
}

variable "name" {
  type = string
}

variable "Environment" {
  type    = string
  default = "${upper("dev")}"
}
`
	Outputs = `output "id" {
  value = azurerm_resource_group.this.id
}

output "fqdn" {
  value = { for name, host in var.hosts : name => "${host}.example.test" }
}
`
	Header    = "# A module\n\nWhat it makes.\n"
	Readme    = "# Old title\n\n<!-- BEGIN_TF_DOCS -->\nold tables\n<!-- END_TF_DOCS -->\n"
	Generated = "<!-- BEGIN_TF_DOCS -->\n## Inputs\n<!-- END_TF_DOCS -->\n"
)

// WriteModule writes a module in folder, its variables and outputs out of order, with an
// example beneath it and a downloaded module in .terraform.
func WriteModule(t testing.TB, folder string) string {
	t.Helper()
	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(folder, "variables.tf"), Variables)
	write(filepath.Join(folder, "outputs.tf"), Outputs)
	write(filepath.Join(folder, "HEADER.md"), Header)
	write(filepath.Join(folder, "README.md"), Readme)
	example := filepath.Join(folder, "examples", "minimal")
	write(filepath.Join(example, "variables.tf"), "variable \"b\" {}\nvariable \"a\" {}\n")
	write(filepath.Join(example, "HEADER.md"), "# The minimal example\n")
	write(filepath.Join(folder, ".terraform", "modules", "other", "variables.tf"), "variable \"z\" {}\nvariable \"y\" {}\n")
	return folder
}

// Tools stand in for PATH and for running a program: terraform fmt formats nothing (and
// says Formatted), and terraform-docs writes Generated between a README's markers, or with
// --output-check fails as the real one does when the README differs.
type Tools struct {
	mu        sync.Mutex
	present   []string
	calls     [][]string
	Formatted []string
	// Broken makes terraform-docs fail with this on stderr.
	Broken string
}

// New is the tools, with present on PATH.
func New(present ...string) *Tools { return &Tools{present: present} }

// LookPath finds a present tool in /usr/bin.
func (t *Tools) LookPath(name string) (string, error) {
	if slices.Contains(t.present, name) {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New(name + " not found")
}

// Calls are each program run, with its arguments.
func (t *Tools) Calls() [][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.calls)
}

// Run runs a program, as its fake.
func (t *Tools) Run(_ context.Context, request process.Request) (process.Result, error) {
	t.mu.Lock()
	t.calls = append(t.calls, append([]string{request.Path}, request.Args...))
	t.mu.Unlock()
	if strings.HasSuffix(request.Path, "terraform-docs") {
		return t.docs(request.Args)
	}
	var out strings.Builder
	for _, file := range t.Formatted {
		out.WriteString(file + "\n")
	}
	return process.Result{Stdout: out.String()}, nil
}

func (t *Tools) docs(args []string) (process.Result, error) {
	if t.Broken != "" {
		return process.Result{ExitCode: 1, Stderr: t.Broken}, nil
	}
	folder := args[len(args)-1]
	name := "README.md"
	if at := slices.Index(args, "--output-file"); at >= 0 {
		name = args[at+1]
	}
	readme := filepath.Join(folder, name)
	data, err := os.ReadFile(readme)
	if err != nil {
		return process.Result{ExitCode: 1, Stderr: err.Error()}, nil
	}
	text := string(data)
	wanted := text[:strings.Index(text, "<!-- BEGIN_TF_DOCS -->")] + Generated
	if slices.Contains(args, "--output-check") {
		if text != wanted {
			return process.Result{ExitCode: 1, Stderr: "Error: " + readme + " is out of date\n"}, nil
		}
		return process.Result{}, nil
	}
	if err := os.WriteFile(readme, []byte(wanted), 0o644); err != nil {
		return process.Result{}, err
	}
	return process.Result{Stdout: readme + " updated successfully\n"}, nil
}
