// Package terraform is Terraform (and OpenTofu) modules, as files: their configuration
// read as far as sorting it needs, and the command line tools run on them (terraform or
// tofu to format a module, and terraform-docs for its README). Nothing here signs in
// anywhere, plans or applies. It depends only on core.
package terraform

import (
	"regexp"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Terraform's configuration language (HCL), as far as sorting its blocks needs: a file
// split into its top-level pieces.
//
// Not a parser. It follows strings, their ${ } templates, comments and heredocs only to
// know which braces open and close a block, so a brace in a description, a map default or
// an object({ }) type never ends one early, and whatever lies between blocks is kept.

var (
	// A block's first line: its type, its labels (quoted, or bare names), and its opening
	// brace.
	blockHead = regexp.MustCompile(`^[ \t]*([A-Za-z_][A-Za-z0-9_-]*)((?:[ \t]+(?:"(?:[^"\\\n]|\\.)*"|[A-Za-z_][A-Za-z0-9_-]*))*)[ \t]*\{`)
	label     = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"|([A-Za-z_][A-Za-z0-9_-]*)`)
	heredoc   = regexp.MustCompile(`^<<-?([A-Za-z_][A-Za-z0-9_-]*)[ \t]*\r?\n?$`)
)

// commentStarts open a comment line.
var commentStarts = []string{"#", "//", "/*"}

// inString marks an open string on the nesting stack, rather than a template's brace
// depth.
const inString = -1

// Piece is one top-level piece of a file, as written: a block with the comments just
// above it (Kind its type, Name its first label), or what lies between blocks (no Kind).
type Piece struct {
	Text string
	Kind string
	Name string
}

type line struct {
	text string
	// depth is the blocks open at its start.
	depth int
	// inside is comment, heredoc or string when it starts in one.
	inside string
}

// Split is text as its top-level pieces, which joined give it back as it was. An Input
// error when its braces, strings or comments do not close, which Terraform itself would
// refuse too.
func Split(text string) ([]Piece, error) {
	lines, err := (&scanner{}).scan(text)
	if err != nil {
		return nil, err
	}
	var pieces []Piece
	var loose []line // lines between blocks, not yet a piece
	for at := 0; at < len(lines); {
		kind, name, isBlock := head(lines[at])
		if !isBlock {
			loose = append(loose, lines[at])
			at++
			continue
		}
		end := blockEnd(lines, at)
		above := len(loose) - commentsAtEnd(loose)
		if above > 0 {
			pieces = append(pieces, Piece{Text: joined(loose[:above])})
		}
		pieces = append(pieces, Piece{Text: joined(loose[above:]) + joined(lines[at:end+1]), Kind: kind, Name: name})
		loose = nil
		at = end + 1
	}
	if len(loose) > 0 {
		pieces = append(pieces, Piece{Text: joined(loose)})
	}
	return pieces, nil
}

// head is the type and first label of the block the line opens.
func head(at line) (string, string, bool) {
	if at.depth != 0 || at.inside != "" {
		return "", "", false
	}
	match := blockHead.FindStringSubmatch(at.text)
	if match == nil {
		return "", "", false
	}
	name := ""
	if found := label.FindStringSubmatch(match[2]); found != nil {
		name = found[1] + found[2]
	}
	return match[1], name, true
}

// blockEnd is the last line of the block opening on line start: the one before depth is
// 0 again.
func blockEnd(lines []line, start int) int {
	for at := start + 1; at < len(lines); at++ {
		if lines[at].depth == 0 && lines[at].inside == "" {
			return at - 1
		}
	}
	return len(lines) - 1
}

// commentsAtEnd is how many of the last lines are comments, with no blank line among
// them: those that document the block below them, and move with it.
func commentsAtEnd(lines []line) int {
	count := 0
	for at := len(lines) - 1; at >= 0; at-- {
		trimmed := strings.TrimLeft(lines[at].text, " \t\r\n\f\v")
		if lines[at].inside != "comment" && !startsWithAny(trimmed, commentStarts...) {
			break
		}
		count++
	}
	return count
}

func startsWithAny(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func joined(lines []line) string {
	var text strings.Builder
	for _, at := range lines {
		text.WriteString(at.text)
	}
	return text.String()
}

// scanner walks the text a line at a time, keeping the depth of braces outside strings,
// templates, comments and heredocs.
type scanner struct {
	depth   int
	comment bool
	heredoc string
	// nesting is the open strings (inString) and the ${ } templates in them (their own
	// brace depth).
	nesting    []int
	unbalanced bool
}

func (s *scanner) scan(text string) ([]line, error) {
	var lines []line
	for _, part := range strings.SplitAfter(text, "\n") {
		if part == "" {
			continue
		}
		lines = append(lines, line{text: part, depth: s.depth, inside: s.inside()})
		s.walk(part)
	}
	if s.unbalanced || s.depth != 0 || s.inside() != "" {
		return nil, errs.Inputf("its braces, strings or comments do not all close").
			WithHint("check it is valid Terraform: terraform validate")
	}
	return lines, nil
}

func (s *scanner) inside() string {
	switch {
	case s.comment:
		return "comment"
	case s.heredoc != "":
		return "heredoc"
	case len(s.nesting) > 0:
		return "string"
	}
	return ""
}

func (s *scanner) walk(text string) {
	if s.heredoc != "" {
		if strings.TrimSpace(text) == s.heredoc {
			s.heredoc = ""
		}
		return
	}
	for at := 0; at < len(text); {
		at = s.step(text, at)
	}
}

// step reads what starts at at, and is where the next thing starts.
func (s *scanner) step(text string, at int) int {
	if s.comment {
		end := strings.Index(text[at:], "*/")
		s.comment = end < 0
		if end < 0 {
			return len(text)
		}
		return at + end + 2
	}
	if len(s.nesting) > 0 && s.nesting[len(s.nesting)-1] == inString {
		return s.inStringAt(text, at)
	}
	return s.inCode(text, at)
}

func (s *scanner) inStringAt(text string, at int) int {
	rest := text[at:]
	switch {
	case rest[0] == '\\':
		return at + 2
	case rest[0] == '"':
		s.nesting = s.nesting[:len(s.nesting)-1]
		return at + 1
	case startsWithAny(rest, "$${", "%%{"):
		// An escaped ${, which opens nothing.
		return at + 3
	case startsWithAny(rest, "${", "%{"):
		s.nesting = append(s.nesting, 0)
		return at + 2
	}
	return at + 1
}

func (s *scanner) inCode(text string, at int) int {
	rest := text[at:]
	switch {
	case startsWithAny(rest, "#", "//"):
		return len(text)
	case strings.HasPrefix(rest, "/*"):
		s.comment = true
		return at + 2
	}
	if found := heredoc.FindStringSubmatch(rest); found != nil {
		s.heredoc = found[1]
		return len(text)
	}
	switch rest[0] {
	case '"':
		s.nesting = append(s.nesting, inString)
	case '{':
		s.open()
	case '}':
		s.close()
	}
	return at + 1
}

func (s *scanner) open() {
	if len(s.nesting) > 0 {
		s.nesting[len(s.nesting)-1]++
		return
	}
	s.depth++
}

func (s *scanner) close() {
	if len(s.nesting) > 0 {
		last := len(s.nesting) - 1
		if s.nesting[last] == 0 {
			// The end of a ${ } template, back in its string.
			s.nesting = s.nesting[:last]
		} else {
			s.nesting[last]--
		}
		return
	}
	s.depth--
	s.unbalanced = s.unbalanced || s.depth < 0
}
