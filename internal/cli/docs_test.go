package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	fence  = regexp.MustCompile("(?s)```bash\n(.*?)```")
	inline = regexp.MustCompile("`(ldo-go [^`]+)`")
)

// examples are the ldo-go command lines the README, AI.md and docs/ show, each as its
// arguments: those in bash blocks (after a pipe too), and in inline code.
func examples(t *testing.T) map[string][]string {
	t.Helper()
	base := filepath.Join("..", "..")
	pages, _ := filepath.Glob(filepath.Join(base, "docs", "*.md"))
	found := map[string][]string{}
	var top []string
	for _, name := range []string{"README.md", "AI.md", "CHANGELOG.md", "CONTRIBUTING.md", "SECURITY.md"} {
		top = append(top, filepath.Join(base, name))
	}
	for _, path := range append(top, pages...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, block := range fence.FindAllStringSubmatch(string(data), -1) {
			lines = append(lines, strings.Split(block[1], "\n")...)
		}
		for _, code := range inline.FindAllStringSubmatch(string(data), -1) {
			lines = append(lines, code[1])
		}
		for _, line := range lines {
			for _, part := range strings.Split(withoutComment(line), "|") {
				if words := shellWords(strings.TrimSpace(part)); len(words) > 1 && words[0] == "ldo-go" {
					found[filepath.Base(path)+": "+strings.TrimSpace(part)] = words[1:]
				}
			}
		}
	}
	return found
}

// withoutComment is a shell line without its # comment (a # inside quotes is kept).
func withoutComment(line string) string {
	quote := rune(0)
	for index, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote == 0 && (char == '"' || char == '\''):
			quote = char
		case quote == 0 && char == '#' && (index == 0 || line[index-1] == ' '):
			return line[:index]
		}
	}
	return line
}

// shellWords splits a shell line into its words, as far as the docs' examples need:
// spaces, and single or double quotes.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	quote, started := rune(0), false
	for _, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(char)
		case char == '"' || char == '\'':
			quote, started = char, true
		case char == ' ':
			if started || word.Len() > 0 {
				words = append(words, word.String())
			}
			word.Reset()
			started = false
		default:
			word.WriteRune(char)
		}
	}
	if started || word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// Every ldo-go example in the docs is a command this build has, with options it takes.
func TestEveryExampleInTheDocsIsARealCommand(t *testing.T) {
	found := examples(t)
	if len(found) < 20 {
		t.Fatalf("only %d examples found", len(found))
	}
	h := newHarness(t, nil)
	for name, args := range found {
		if code := h.run(append(args, "--help")...); code != ExitOK {
			t.Errorf("%s: exit %d\n%s", name, code, h.err)
		}
	}
}

func TestShellWordsAndComments(t *testing.T) {
	if got := strings.Join(shellWords(`xdr hunt "DeviceInfo | take 1" -o 'json'`), ","); got != "xdr,hunt,DeviceInfo | take 1,-o,json" {
		t.Error(got)
	}
	if got := withoutComment(`ldo-go json "a # b"   # a comment`); got != `ldo-go json "a # b"   ` {
		t.Errorf("%q", got)
	}
}
