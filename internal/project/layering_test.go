// Package project holds tests of the repository itself: its layering, its layout and
// its docs.
package project

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/libre-devops/ldo-go-cli/"

// root is the repository root, from this package's folder.
func root(t *testing.T) string {
	t.Helper()
	folder, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return folder
}

// packages is every package folder under internal, relative to the root, with the
// module packages its non-test files import.
func packages(t *testing.T) map[string][]string {
	t.Helper()
	base := root(t)
	found := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(base, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(base, filepath.Dir(path))
		folder := filepath.ToSlash(relative)
		if _, seen := found[folder]; !seen {
			found[folder] = nil
		}
		for _, spec := range file.Imports {
			imported := strings.Trim(spec.Path.Value, `"`)
			if strings.HasPrefix(imported, module) && !slices.Contains(found[folder], strings.TrimPrefix(imported, module)) {
				found[folder] = append(found[folder], strings.TrimPrefix(imported, module))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// vendors are the vendor layers: each a shared package, its own helpers, and features.
var vendors = map[string][]string{
	"internal/microsoft":  {"internal/microsoft/identity", "internal/microsoft/azcli"},
	"internal/servicenow": {},
	"internal/atlassian":  {},
	"internal/terraform":  {},
}

// composites are the feature packages allowed to use other features of their vendor.
var composites = map[string][]string{
	"internal/microsoft/devices": {"internal/microsoft/entra", "internal/microsoft/xdr", "internal/microsoft/intune"},
}

func under(folder, parent string) bool {
	return folder == parent || strings.HasPrefix(folder, parent+"/")
}

// allowed reports whether the package in folder may import imported.
func allowed(folder, imported string) bool {
	switch {
	case under(imported, "internal/fakes"):
		return under(folder, "internal/fakes")
	case under(folder, "internal/cli"):
		return true
	case under(imported, "internal/core"):
		return true
	case under(folder, "internal/core"), under(folder, "internal/fakes"):
		return false
	}
	for shared, helpers := range vendors {
		if !under(folder, shared) {
			continue
		}
		if imported == shared {
			return folder != shared
		}
		// The shared layer's own helpers use it; nothing else uses them but the CLI.
		if slices.Contains(helpers, imported) {
			return false
		}
		return slices.Contains(composites[folder], imported)
	}
	return false
}

func TestEachLayerImportsOnlyWhatItMay(t *testing.T) {
	for folder, imports := range packages(t) {
		for _, imported := range imports {
			if !allowed(folder, imported) {
				t.Errorf("%s imports %s", folder, imported)
			}
		}
	}
}

func TestEveryPackageHasTests(t *testing.T) {
	base := root(t)
	var missing []string
	for folder := range packages(t) {
		if under(folder, "internal/fakes") {
			continue
		}
		tests, _ := filepath.Glob(filepath.Join(base, folder, "*_test.go"))
		if len(tests) == 0 {
			missing = append(missing, folder)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("packages with no tests: %s", strings.Join(missing, ", "))
	}
}

func TestTheLayeringRulesHold(t *testing.T) {
	cases := map[[2]string]bool{
		{"internal/core/httpx", "internal/core/errs"}:                true,
		{"internal/core/httpx", "internal/microsoft"}:                false,
		{"internal/microsoft", "internal/core/httpx"}:                true,
		{"internal/microsoft", "internal/microsoft/entra"}:           false,
		{"internal/microsoft/entra", "internal/microsoft"}:           true,
		{"internal/microsoft/entra", "internal/microsoft/graph"}:     false,
		{"internal/microsoft/entra", "internal/microsoft/identity"}:  false,
		{"internal/microsoft/identity", "internal/microsoft"}:        true,
		{"internal/microsoft/devices", "internal/microsoft/entra"}:   true,
		{"internal/cli", "internal/microsoft/identity"}:              true,
		{"internal/cli", "internal/fakes/httpfake"}:                  false,
		{"internal/fakes/tokenfake", "internal/core/auth"}:           true,
		{"internal/fakes/tokenfake", "internal/microsoft"}:           false,
		{"internal/servicenow", "internal/microsoft"}:                false,
		{"internal/servicenow/instance", "internal/servicenow"}:      true,
		{"internal/servicenow/instance", "internal/microsoft"}:       false,
		{"internal/microsoft/entra", "internal/servicenow"}:          false,
		{"internal/atlassian/jira", "internal/atlassian"}:            true,
		{"internal/atlassian/jira", "internal/atlassian/confluence"}: false,
		{"internal/atlassian", "internal/servicenow"}:                false,
	}
	for pair, want := range cases {
		if allowed(pair[0], pair[1]) != want {
			t.Errorf("%s importing %s: want %v", pair[0], pair[1], want)
		}
	}
}
