// Package jsontext writes JSON as Python's json.dumps does, for files a person keeps and
// diffs: keys in the order they came (an ordered object from yamltext.Decode), ASCII only
// (other characters as \uXXXX escapes), a float as Python writes it (1.0, 1e-07), and
// ", " and ": " between items on one line, or indent spaces a level when indented.
package jsontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
)

// Dumps is value as JSON text, as Python's json.dumps(value, indent=indent) writes it: on
// one line when indent is 0, else indented by indent spaces a level. Maps are written with
// their keys sorted; an Ordered keeps its order.
func Dumps(value any, indent int) string {
	return Write(value, Options{Indent: indent, Lines: indent > 0, ASCII: true})
}

// Options are how Write lays JSON out.
type Options struct {
	// Indent is spaces a level, when Lines puts each item on a line of its own (Python's
	// indent=0 is Lines with no Indent).
	Indent int
	Lines  bool
	// Compact is one line with no spaces after , and :.
	Compact bool
	// ASCII escapes every character past ASCII, as Python's ensure_ascii does.
	ASCII bool
	// SortKeys sorts an Ordered's keys too.
	SortKeys bool
	// Paint styles a key, string, number, bool or null; Bracket a bracket at a depth.
	Paint   func(kind, text string) string
	Bracket func(depth int, text string) string
}

func (o Options) paint(kind, text string) string {
	if o.Paint == nil {
		return text
	}
	return o.Paint(kind, text)
}

func (o Options) bracket(depth int, text string) string {
	if o.Bracket == nil {
		return text
	}
	return o.Bracket(depth, text)
}

// Write is value as JSON text, laid out as opts say.
func Write(value any, opts Options) string {
	var out strings.Builder
	opts.write(&out, value, 0)
	return out.String()
}

func (o Options) quote(text string) string {
	if o.ASCII {
		return Quote(text)
	}
	return QuoteUnicode(text)
}

func (o Options) write(out *strings.Builder, value any, level int) {
	switch v := value.(type) {
	case nil:
		out.WriteString(o.paint("null", "null"))
	case bool:
		out.WriteString(o.paint("bool", strconv.FormatBool(v)))
	case string:
		out.WriteString(o.paint("string", o.quote(v)))
	case float64:
		out.WriteString(o.paint("number", Float(v)))
	case int:
		out.WriteString(o.paint("number", strconv.Itoa(v)))
	case int64:
		out.WriteString(o.paint("number", strconv.FormatInt(v, 10)))
	case json.Number:
		out.WriteString(o.paint("number", Number(v)))
	case yamltext.Ordered:
		if o.SortKeys {
			v = sortedPairs(v)
		}
		o.object(out, v, level)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(yamltext.Ordered, len(keys))
		for index, key := range keys {
			ordered[index] = yamltext.Pair{Key: key, Value: v[key]}
		}
		o.object(out, ordered, level)
	case []any:
		o.list(out, v, level)
	case []string:
		items := make([]any, len(v))
		for index, item := range v {
			items[index] = item
		}
		o.list(out, items, level)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			out.WriteString(o.quote(fmt.Sprint(v)))
			return
		}
		out.Write(encoded)
	}
}

func sortedPairs(pairs yamltext.Ordered) yamltext.Ordered {
	sorted := append(yamltext.Ordered(nil), pairs...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Key < sorted[b].Key })
	return sorted
}

func (o Options) separator(out *strings.Builder, first bool, level int) {
	if !first {
		out.WriteString(",")
		if !o.Lines && !o.Compact {
			out.WriteString(" ")
		}
	}
	if o.Lines {
		out.WriteString("\n" + strings.Repeat(" ", o.Indent*level))
	}
}

func (o Options) object(out *strings.Builder, pairs yamltext.Ordered, level int) {
	if len(pairs) == 0 {
		out.WriteString(o.bracket(level, "{") + o.bracket(level, "}"))
		return
	}
	colon := ": "
	if o.Compact {
		colon = ":"
	}
	out.WriteString(o.bracket(level, "{"))
	for index, pair := range pairs {
		o.separator(out, index == 0, level+1)
		out.WriteString(o.paint("key", o.quote(pair.Key)) + colon)
		o.write(out, pair.Value, level+1)
	}
	if o.Lines {
		out.WriteString("\n" + strings.Repeat(" ", o.Indent*level))
	}
	out.WriteString(o.bracket(level, "}"))
}

func (o Options) list(out *strings.Builder, items []any, level int) {
	if len(items) == 0 {
		out.WriteString(o.bracket(level, "[") + o.bracket(level, "]"))
		return
	}
	out.WriteString(o.bracket(level, "["))
	for index, item := range items {
		o.separator(out, index == 0, level+1)
		o.write(out, item, level+1)
	}
	if o.Lines {
		out.WriteString("\n" + strings.Repeat(" ", o.Indent*level))
	}
	out.WriteString(o.bracket(level, "]"))
}

// QuoteUnicode is text as a JSON string with only what JSON needs escaped, as Python's
// json.dumps(ensure_ascii=False) writes one.
func QuoteUnicode(text string) string {
	short := map[rune]string{'"': `\"`, '\\': `\\`, '\n': `\n`, '\r': `\r`, '\t': `\t`, '\b': `\b`, '\f': `\f`}
	var out strings.Builder
	out.WriteString(`"`)
	for _, char := range text {
		switch escaped, ok := short[char]; {
		case ok:
			out.WriteString(escaped)
		case char < 0x20:
			fmt.Fprintf(&out, `\u%04x`, char)
		default:
			out.WriteRune(char)
		}
	}
	out.WriteString(`"`)
	return out.String()
}

// Quote is text as a JSON string with everything outside printable ASCII escaped, as
// Python's json.dumps writes one by default.
func Quote(text string) string {
	short := map[rune]string{'"': `\"`, '\\': `\\`, '\n': `\n`, '\r': `\r`, '\t': `\t`, '\b': `\b`, '\f': `\f`}
	var out strings.Builder
	out.WriteString(`"`)
	for _, char := range text {
		switch escaped, ok := short[char]; {
		case ok:
			out.WriteString(escaped)
		case char >= ' ' && char <= '~':
			out.WriteRune(char)
		case char >= 0x10000:
			high, low := utf16.EncodeRune(char)
			fmt.Fprintf(&out, `\u%04x\u%04x`, high, low)
		default:
			fmt.Fprintf(&out, `\u%04x`, char)
		}
	}
	out.WriteString(`"`)
	return out.String()
}

// Float is a float as Python writes one (its repr): 1.0, 0.1, 1e-07, 1e+16.
func Float(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(scientific, "e")
	exponent, _ := strconv.Atoi(exponentText)
	if exponent >= -4 && exponent < 16 {
		text := strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(text, ".") {
			text += ".0"
		}
		return text
	}
	sign := "+"
	if exponent < 0 {
		sign, exponent = "-", -exponent
	}
	return fmt.Sprintf("%se%s%02d", mantissa, sign, exponent)
}

// Number is a JSON number as Python reads and writes it: a whole number as written, any
// other as its float.
func Number(value json.Number) string {
	text := value.String()
	if !strings.ContainsAny(text, ".eE") {
		return text
	}
	parsed, ok := ParseFloat(text)
	if !ok {
		return text
	}
	return Float(parsed)
}

// ParseFloat reads a JSON number as Python's json does: one too big for a float is
// infinity, not an error.
func ParseFloat(text string) (float64, bool) {
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return parsed, true
}
