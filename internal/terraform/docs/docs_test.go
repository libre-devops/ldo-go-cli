package docs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/terraformfake"
)

var (
	ctx     = context.Background()
	markers = Begin + "\n" + End + "\n"
)

func text(value string) *string { return &value }

func TestTheHeaderGoesAboveTheSectionAndTheRestIsKept(t *testing.T) {
	readme := "# Old\n" + Begin + "\ntables\n" + End + "\nfooter"
	for _, test := range []struct {
		readme  string
		header  *string
		newline string
		want    string
	}{
		{readme, text("# New\n\n"), "\n", "# New\n\n" + Begin + "\ntables\n" + End + "\nfooter\n"},
		{readme, nil, "\n", "# Old\n\n" + Begin + "\ntables\n" + End + "\nfooter\n"},
		{"# Mine\n", nil, "\n", "# Mine\n\n" + markers},
		{"", nil, "\n", markers},
		{"", text("  \n"), "\n", markers},
		{"# A\r\n", text("# B\r\n"), "\r\n", "# B\r\n\r\n" + Begin + "\r\n" + End + "\r\n"},
	} {
		if got, err := WithHeader(test.readme, test.header, test.newline); err != nil || got != test.want {
			t.Errorf("%q: %q %v", test.readme, got, err)
		}
	}
	if _, err := WithHeader(Begin+"\n", text("# New"), "\n"); err == nil || !strings.Contains(err.Error(), "but no") {
		t.Error(err)
	}
}

func tool(tools *terraformfake.Tools) *process.Command {
	return &process.Command{Name: "terraform-docs", Executable: "/usr/bin/terraform-docs", Runner: tools}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAReadmeIsWrittenThenFoundUpToDate(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	tools := terraformfake.New("terraform-docs")
	first, err := Document(ctx, module, tool(tools), Options{})
	readme := filepath.Join(module, "README.md")
	if err != nil || first != (Result{Folder: module, Path: readme, Header: filepath.Join(module, "HEADER.md"), State: "updated"}) {
		t.Fatal(first, err)
	}
	if got := read(t, readme); got != terraformfake.Header+"\n"+terraformfake.Generated {
		t.Errorf("%q", got)
	}
	calls := tools.Calls()
	if got := strings.Join(calls[len(calls)-1][1:], " "); got != "markdown table --output-file README.md --output-mode inject "+module {
		t.Error(got)
	}
	for _, check := range []bool{true, false} {
		if again, _ := Document(ctx, module, tool(tools), Options{Check: check}); again.State != "up to date" {
			t.Error(check, again)
		}
	}
}

func TestACheckChangesNothingAndAConfigFileIsTheModulesOwn(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	_ = os.WriteFile(filepath.Join(module, ".terraform-docs.yml"), []byte("formatter: markdown table\n"), 0o644)
	before := read(t, filepath.Join(module, "README.md"))
	tools := terraformfake.New("terraform-docs")
	if found, _ := Document(ctx, module, tool(tools), Options{Check: true}); found.State != "out of date" || len(tools.Calls()) != 0 {
		// The header differs, so terraform-docs need not say.
		t.Error(found, tools.Calls())
	}
	_ = os.WriteFile(filepath.Join(module, "HEADER.md"), []byte("# Old title\n"), 0o644)
	if found, _ := Document(ctx, module, tool(tools), Options{Check: true}); found.State != "out of date" {
		t.Error(found)
	}
	if got := strings.Join(tools.Calls()[0][1:], " "); got != "--output-check "+module {
		t.Error(got)
	}
	if read(t, filepath.Join(module, "README.md")) != before {
		t.Error("a check changed the README")
	}
}

func TestAFolderWithoutAHeaderKeepsItsReadmesTop(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	_ = os.Remove(filepath.Join(module, "HEADER.md"))
	result, err := Document(ctx, module, tool(terraformfake.New()), Options{Readme: "README.md"})
	if err != nil || result.Header != "" || result.State != "updated" {
		t.Fatal(result, err)
	}
	if got := read(t, filepath.Join(module, "README.md")); got != "# Old title\n\n"+terraformfake.Generated {
		t.Errorf("%q", got)
	}
	_ = os.WriteFile(filepath.Join(module, "README.md"), []byte(Begin+"\n"), 0o644)
	if _, err := Document(ctx, module, tool(terraformfake.New()), Options{}); err == nil || !strings.Contains(err.Error(), "README.md: it has") {
		t.Error(err)
	}
}

func TestAFolderWithNoReadmeGetsOne(t *testing.T) {
	folder := t.TempDir()
	result, err := Document(ctx, folder, tool(terraformfake.New()), Options{})
	if err != nil || result.State != "updated" {
		t.Fatal(result, err)
	}
	if got := read(t, filepath.Join(folder, "README.md")); got != terraformfake.Generated {
		t.Errorf("%q", got)
	}
}

func TestTerraformDocsFailingForAnotherReasonSaysSo(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	_ = os.WriteFile(filepath.Join(module, "HEADER.md"), []byte("# Old title\n"), 0o644)
	tools := terraformfake.New()
	tools.Broken = "Error: failed to load module\n"
	for _, check := range []bool{true, false} {
		if _, err := Document(ctx, module, tool(tools), Options{Check: check}); !errs.Is(err, errs.Command) || !strings.Contains(err.Error(), "failed to load module") {
			t.Error(check, err)
		}
	}
}

func TestFoldersBeneathAreThoseWithAHeaderOfTheirOwn(t *testing.T) {
	module := terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))
	example := filepath.Join(module, "examples", "minimal")
	if found, _ := Folders([]string{module}, false, ""); strings.Join(found, ",") != module {
		t.Error(found)
	}
	if found, _ := Folders([]string{module}, true, ""); strings.Join(found, ",") != module+","+example {
		t.Error(found)
	}
	if found, _ := Folders([]string{module}, true, "OTHER.md"); strings.Join(found, ",") != module {
		t.Error(found)
	}
	if _, err := Folders([]string{filepath.Join(module, "README.md")}, false, ""); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Error(err)
	}
}
