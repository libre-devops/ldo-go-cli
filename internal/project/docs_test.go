package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// documents are the Markdown files a person reads: those at the top, and docs/.
func documents(t *testing.T) []string {
	t.Helper()
	base := root(t)
	var found []string
	for _, name := range []string{"README.md", "AI.md", "CHANGELOG.md", "CONTRIBUTING.md", "SECURITY.md"} {
		found = append(found, filepath.Join(base, name))
	}
	pages, _ := filepath.Glob(filepath.Join(base, "docs", "*.md"))
	return append(found, pages...)
}

var (
	link    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	heading = regexp.MustCompile(`(?m)^#{1,6} +(.+)$`)
	justUse = regexp.MustCompile("(?m)^just ([a-z][a-z0-9-]*)")
	recipe  = regexp.MustCompile(`(?m)^(?:\[.*\]\n)*([a-z][a-z0-9-]*)(?: [^:\n]*)?:`)
)

// anchor is the id GitHub gives a heading: lower case, punctuation dropped, spaces as
// hyphens.
func anchor(text string) string {
	var kept strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case char == ' ':
			kept.WriteRune('-')
		case char == '-' || char == '_' || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9'):
			kept.WriteRune(char)
		}
	}
	return kept.String()
}

func anchors(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, match := range heading.FindAllStringSubmatch(string(data), -1) {
		found = append(found, anchor(strings.ReplaceAll(match[1], "`", "")))
	}
	return found
}

// Every relative link goes to a file that is there, and to a heading it has.
func TestEveryLinkInTheDocsGoesSomewhere(t *testing.T) {
	for _, document := range documents(t) {
		data, _ := os.ReadFile(document)
		for _, match := range link.FindAllStringSubmatch(string(data), -1) {
			target := match[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			file, section, _ := strings.Cut(target, "#")
			path := document
			if file != "" {
				path = filepath.Join(filepath.Dir(document), filepath.FromSlash(file))
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: %s is not there", filepath.Base(document), target)
				continue
			}
			if section != "" && !slices.Contains(anchors(t, path), section) {
				t.Errorf("%s: %s has no heading #%s", filepath.Base(document), file, section)
			}
		}
	}
}

// Every just example in the docs is a recipe.
func TestEveryJustExampleIsARecipe(t *testing.T) {
	justfile, err := os.ReadFile(filepath.Join(root(t), "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	var recipes []string
	for _, match := range recipe.FindAllStringSubmatch(string(justfile), -1) {
		recipes = append(recipes, match[1])
	}
	for _, document := range documents(t) {
		data, _ := os.ReadFile(document)
		for _, match := range justUse.FindAllStringSubmatch(string(data), -1) {
			if !slices.Contains(recipes, match[1]) {
				t.Errorf("%s: just %s is not a recipe (%v)", filepath.Base(document), match[1], recipes)
			}
		}
	}
}

// No file holds an em or en dash: commas, colons, brackets or a plain hyphen instead.
func TestNoFileHoldsAnEmOrEnDash(t *testing.T) {
	base := root(t)
	dashes := string([]rune{0x2013, 0x2014})
	skipped := []string{".git", "bin", "dist"}
	_ = filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if slices.Contains(skipped, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".out") || strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".xlsx") {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.ContainsAny(string(data), dashes) {
			relative, _ := filepath.Rel(base, path)
			t.Errorf("%s holds an em or en dash", relative)
		}
		return nil
	})
}

func TestAnchorsAreGitHubs(t *testing.T) {
	for text, want := range map[string]string{"Keeping a sign-in": "keeping-a-sign-in", "JSON and YAML": "json-and-yaml",
		"How it differs from the Python ldo": "how-it-differs-from-the-python-ldo", "Options every command takes": "options-every-command-takes"} {
		if got := anchor(text); got != want {
			t.Errorf("%s: %s", text, got)
		}
	}
}
