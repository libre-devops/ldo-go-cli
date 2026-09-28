// Package sorting sorts and de-duplicates the values these APIs return.
//
// Compare orders what a person would expect rather than what text order gives:
//
//   - numbers numerically: 9.8 before 10.0;
//   - versions part by part: 1.419.99.0 before 1.419.100.0;
//   - severities by rank: Informational, Low, Medium, High, Critical;
//   - names naturally and without case: web2 before web10, Web3 beside web3;
//   - dates in time order, as ISO text (which that last rule gives).
//
// SortRows sorts on several keys, each ascending or descending, keeping blanks ("" and
// "-") last either way; Unique keeps the first of each value, so sorting newest first and
// then keeping one per device keeps the newest record of each.
package sorting

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// SeverityRank is each severity's place, lowest first.
var SeverityRank = map[string]int{
	"informational": 0, "info": 0, "none": 0, "low": 1, "medium": 2, "moderate": 2,
	"high": 3, "important": 3, "critical": 4,
}

var (
	numberPattern  = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)
	versionPattern = regexp.MustCompile(`^\d+(\.\d+){2,}$`)
	digitRuns      = regexp.MustCompile(`\d+`)
	squashPattern  = regexp.MustCompile(`[\s_-]+`)
)

// Blank reports whether a table shows value as nothing: empty, or "-".
func Blank(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "-"
}

type key struct {
	kind   int
	number float64
	parts  []part
}

type part struct {
	numeric bool
	number  int
	text    string
}

func keyOf(value string) key {
	text := strings.TrimSpace(value)
	folded := strings.ToLower(text)
	if rank, ok := SeverityRank[folded]; ok {
		return key{kind: 2, number: float64(rank)}
	}
	if numberPattern.MatchString(text) {
		number, _ := strconv.ParseFloat(text, 64)
		return key{kind: 1, number: number}
	}
	if versionPattern.MatchString(text) {
		var parts []part
		for _, piece := range strings.Split(text, ".") {
			number, _ := strconv.Atoi(piece)
			parts = append(parts, part{numeric: true, number: number})
		}
		return key{kind: 3, parts: parts}
	}
	return key{kind: 4, parts: naturalParts(folded)}
}

// naturalParts splits text into runs of digits and runs of anything else, as Python's
// re.split with a capturing group does, so "web10" is ["web", 10, ""].
func naturalParts(text string) []part {
	var parts []part
	last := 0
	for _, span := range digitRuns.FindAllStringIndex(text, -1) {
		parts = append(parts, part{text: text[last:span[0]]})
		number, _ := strconv.Atoi(text[span[0]:span[1]])
		parts = append(parts, part{numeric: true, number: number})
		last = span[1]
	}
	return append(parts, part{text: text[last:]})
}

func comparePart(a, b part) int {
	if a.numeric != b.numeric {
		if a.numeric {
			return -1
		}
		return 1
	}
	if a.numeric {
		return cmp.Compare(a.number, b.number)
	}
	return strings.Compare(a.text, b.text)
}

// Compare is the natural order of a and b: negative when a goes first.
func Compare(a, b string) int {
	ka, kb := keyOf(a), keyOf(b)
	if ka.kind != kb.kind {
		return cmp.Compare(ka.kind, kb.kind)
	}
	if ka.parts == nil && kb.parts == nil {
		return cmp.Compare(ka.number, kb.number)
	}
	return slices.CompareFunc(ka.parts, kb.parts, comparePart)
}

// Key is one column to sort by, and which way.
type Key struct {
	Column     int
	Descending bool
}

// SortRows sorts rows by keys, most significant first. It is stable, so rows equal on
// every key keep their order, and blanks go last whichever way a key runs. text reads a
// cell of a row.
func SortRows[R any](rows []R, keys []Key, text func(R, int) string) []R {
	ordered := slices.Clone(rows)
	// Sorting by the least significant key first, then by each more significant one, gives
	// the order of all of them together, since each sort keeps ties in the order it found.
	for index := len(keys) - 1; index >= 0; index-- {
		sortKey := keys[index]
		slices.SortStableFunc(ordered, func(a, b R) int {
			ta, tb := text(a, sortKey.Column), text(b, sortKey.Column)
			blankA, blankB := Blank(ta), Blank(tb)
			switch {
			case blankA && blankB:
				return 0
			case blankA:
				return 1
			case blankB:
				return -1
			}
			order := Compare(ta, tb)
			if sortKey.Descending {
				return -order
			}
			return order
		})
	}
	return ordered
}

// Unique is the first row for each value of the columns, in order. Values compare
// without case or surrounding space, so WEB01 and "web01 " are one.
func Unique[R any](rows []R, columns []int, text func(R, int) string) []R {
	seen := map[string]bool{}
	var kept []R
	for _, row := range rows {
		var marker strings.Builder
		for _, column := range columns {
			marker.WriteString(strings.ToLower(strings.TrimSpace(text(row, column))))
			marker.WriteByte(0)
		}
		if !seen[marker.String()] {
			seen[marker.String()] = true
			kept = append(kept, row)
		}
	}
	return kept
}

// ParseSort reads a --sort value: "last seen:desc" is ("last seen", true); ":asc" or
// nothing is ascending.
func ParseSort(spec string) (name string, descending bool, err error) {
	name, direction := spec, "asc"
	if index := strings.LastIndex(spec, ":"); index >= 0 {
		name, direction = spec[:index], spec[index+1:]
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	name = strings.TrimSpace(name)
	if (direction != "asc" && direction != "desc") || name == "" {
		return "", false, errs.Inputf("cannot sort by %q", spec).
			WithHint("use a column name, with :desc to reverse it")
	}
	return name, direction == "desc", nil
}

// ColumnIndex is the column name names, ignoring case, spaces, hyphens and underscores.
func ColumnIndex(headers []string, name string) (int, error) {
	wanted := squash(name)
	for index, header := range headers {
		if squash(header) == wanted {
			return index, nil
		}
	}
	var named []string
	for _, header := range headers {
		if strings.TrimSpace(header) != "" {
			named = append(named, header)
		}
	}
	return 0, errs.Inputf("there is no %q column", name).WithHint("columns: %s", strings.Join(named, ", "))
}

func squash(text string) string {
	return strings.ToLower(squashPattern.ReplaceAllString(text, ""))
}
