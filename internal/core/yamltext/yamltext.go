// Package yamltext writes JSON-shaped data as YAML, with the standard library alone.
//
// Only what JSON can hold is written (objects, arrays, strings, numbers, booleans, null),
// in block style. A string is left plain only when no YAML reader could take it for
// anything else: yes, no, null, 1.0, 2026-09-24, @odata.context and a: b are all quoted,
// as JSON strings, which YAML reads the same way. A multi-line string becomes a | block.
// There is no parser: reading YAML is not needed here.
//
// Go maps have no order, so an object whose keys must stay in order is an Ordered; a map
// is written with its keys sorted.
package yamltext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Pair is one key of an Ordered object, and its value.
type Pair struct {
	Key   string
	Value any
}

// Ordered is an object whose keys keep their order.
type Ordered []Pair

// Get is the value of key, and whether it is there.
func (o Ordered) Get(key string) (any, bool) {
	for _, pair := range o {
		if pair.Key == key {
			return pair.Value, true
		}
	}
	return nil, false
}

// MarshalJSON writes the object with its keys in order.
func (o Ordered) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, pair := range o {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, _ := json.Marshal(pair.Key)
		value, err := json.Marshal(pair.Value)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// Decode reads JSON keeping each object's keys in order: objects become Ordered, numbers
// float64 (json.Number when useNumber, to keep them exactly as written).
func Decode(data []byte, useNumber bool) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if useNumber {
		decoder.UseNumber()
	}
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	rest := bytes.TrimLeft(data[decoder.InputOffset():], " \t\r\n")
	if len(rest) > 0 {
		return nil, &ExtraData{Offset: len(data) - len(rest)}
	}
	return value, nil
}

// ExtraData is JSON with more after its one value: Offset is the byte where the rest
// starts.
type ExtraData struct{ Offset int }

func (e *ExtraData) Error() string { return "extra data after the JSON value" }

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := Ordered{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, err
			}
			object = append(object, Pair{Key: key.(string), Value: value})
		}
		_, err = decoder.Token()
		return object, err
	case '[':
		list := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		_, err = decoder.Token()
		return list, err
	}
	return nil, fmt.Errorf("unexpected %v", delim)
}

// Paint styles a piece of text by its kind: key, string, number, bool, null or punct.
type Paint func(kind, text string) string

var (
	// Plain only when it starts with a letter or underscore and holds nothing YAML reads
	// specially; single spaces between words are fine.
	plain = regexp.MustCompile(`^[A-Za-z_][\p{L}\p{N}_.\-/+@]*(?: [\p{L}\p{N}_.\-/+@]+)*$`)
	// Characters YAML will not take as they are inside a quoted string: C1 controls
	// (U+0085 is a line break to YAML), the Unicode line and paragraph separators, and
	// non-characters.
	unprintable = regexp.MustCompile(`[\x{7f}-\x{9f}\x{2028}\x{2029}\x{feff}\x{fffe}\x{ffff}]`)
	// Characters that make a string unsafe for a literal block: controls other than tab.
	control = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f\x{85}\x{2028}\x{2029}\x{feff}]`)
)

// words are what YAML 1.1 or 1.2 readers take for booleans or null, in any case.
var words = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true, "y": true,
	"n": true, "null": true, "none": true, "nan": true, "inf": true}

// Dumps is data as a YAML document, ending in a newline. The newline matters: a | block
// last in the document keeps its own final line break only when the document ends with
// one. indent is 2 when less than 1; paint may be nil.
func Dumps(data any, indent int, paint Paint) string {
	if paint == nil {
		paint = func(_, text string) string { return text }
	}
	writer := writer{indent: max(indent, 1), paint: paint}
	if indent < 1 {
		writer.indent = 2
	}
	return strings.Join(writer.node(normal(data), 0), "\n") + "\n"
}

type writer struct {
	indent int
	paint  Paint
}

// normal is value with maps and typed slices as Ordered and []any.
func normal(value any) any {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(Ordered, len(keys))
		for index, key := range keys {
			ordered[index] = Pair{key, v[key]}
		}
		return ordered
	case []string:
		list := make([]any, len(v))
		for index, item := range v {
			list[index] = item
		}
		return list
	case []map[string]any:
		list := make([]any, len(v))
		for index, item := range v {
			list[index] = item
		}
		return list
	case []Ordered:
		list := make([]any, len(v))
		for index, item := range v {
			list[index] = item
		}
		return list
	}
	return value
}

func nests(value any) bool {
	switch v := normal(value).(type) {
	case Ordered:
		return len(v) > 0
	case []any:
		return len(v) > 0
	}
	return false
}

// node is the lines for value standing alone, at level spaces.
func (w writer) node(value any, level int) []string {
	pad := strings.Repeat(" ", level)
	switch v := normal(value).(type) {
	case Ordered:
		if len(v) > 0 {
			var lines []string
			for _, pair := range v {
				head := pad + w.paint("key", quoted(pair.Key)) + w.paint("punct", ":")
				lines = append(lines, w.entry(head, pair.Value, level)...)
			}
			return lines
		}
	case []any:
		if len(v) > 0 {
			var lines []string
			for _, item := range v {
				lines = append(lines, w.item(item, level)...)
			}
			return lines
		}
	}
	block := w.scalar(value)
	return append([]string{pad + block[0]}, indented(block[1:], level+w.indent)...)
}

// entry is key: followed by its value, on the same line or nested below it.
func (w writer) entry(head string, value any, level int) []string {
	if nests(value) {
		return append([]string{head}, w.node(value, level+w.indent)...)
	}
	block := w.scalar(value)
	return append([]string{head + " " + block[0]}, indented(block[1:], level+w.indent)...)
}

// item is "- " followed by an array item; a nested object or array starts on the same line.
func (w writer) item(value any, level int) []string {
	dash := strings.Repeat(" ", level) + w.paint("punct", "-")
	if nests(value) {
		inner := w.node(value, level+2)
		return append([]string{dash + " " + strings.TrimLeft(inner[0], " ")}, inner[1:]...)
	}
	block := w.scalar(value)
	return append([]string{dash + " " + block[0]}, indented(block[1:], level+2)...)
}

// indented is a block's lines at level; blank ones stay empty, with no trailing spaces.
func indented(lines []string, level int) []string {
	found := make([]string, len(lines))
	for index, line := range lines {
		if line != "" {
			found[index] = strings.Repeat(" ", level) + line
		}
	}
	return found
}

// scalar is a scalar's lines: one, or a | header and a block, each without the indent.
func (w writer) scalar(value any) []string {
	switch v := normal(value).(type) {
	case nil:
		return []string{w.paint("null", "null")}
	case bool:
		return []string{w.paint("bool", strconv.FormatBool(v))}
	case float64:
		return []string{w.paint("number", float(v))}
	case int:
		return []string{w.paint("number", strconv.Itoa(v))}
	case int64:
		return []string{w.paint("number", strconv.FormatInt(v, 10))}
	case json.Number:
		return []string{w.paint("number", number(v))}
	case Ordered:
		return []string{w.paint("punct", "{}")}
	case []any:
		return []string{w.paint("punct", "[]")}
	case string:
		if block := literalBlock(v); block != nil {
			lines := []string{w.paint("punct", block[0])}
			for _, line := range block[1:] {
				lines = append(lines, w.paint("string", line))
			}
			return lines
		}
		return []string{w.paint("string", quoted(v))}
	}
	return []string{w.paint("string", quoted(fmt.Sprint(value)))}
}

// float is a float as Python's json writes one (its repr: 1.5, 2.0, 1e-07, 1e+16), made
// one every YAML reader takes as a float: YAML 1.1 wants a point, so 1e-07 is 1.0e-07.
func float(value float64) string {
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
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	sign := "+"
	if exponent < 0 {
		sign, exponent = "-", -exponent
	}
	return fmt.Sprintf("%se%s%02d", mantissa, sign, exponent)
}

// number is a JSON number as it was written, when it is a whole number, else as the float
// it is, as Python's json reads and writes numbers.
func number(value json.Number) string {
	text := value.String()
	if !strings.ContainsAny(text, ".eE") {
		if whole, err := strconv.ParseInt(text, 10, 64); err == nil {
			return strconv.FormatInt(whole, 10)
		}
		return text
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return text
	}
	// Too big for a float is infinity, as Python's json reads it.
	return float(parsed)
}

// quoted is text plain when that reads back as the same string, else JSON-quoted.
func quoted(text string) string {
	if plain.MatchString(text) && !words[strings.ToLower(text)] {
		return text
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	encoded := strings.TrimSuffix(buffer.String(), "\n")
	// Go escapes U+2028 and U+2029 itself; the rest YAML will not take as they are.
	return unprintable.ReplaceAllStringFunc(encoded, func(found string) string {
		return fmt.Sprintf("\\u%04x", []rune(found)[0])
	})
}

// literalBlock is a multi-line string as a literal block (|), or nil when it cannot be
// one. The lines come back unindented. A block must not start with a space (the reader
// would take that as the indent), and holds no control characters.
func literalBlock(text string) []string {
	if !strings.Contains(text, "\n") || control.MatchString(text) || strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\n") {
		return nil
	}
	body := strings.TrimRight(text, "\n")
	trailing := len(text) - len(body)
	chomp := map[int]string{0: "|-", 1: "|"}[trailing]
	if chomp == "" {
		chomp = "|+"
	}
	lines := strings.Split(body, "\n")
	for range max(trailing-1, 0) {
		lines = append(lines, "")
	}
	for _, line := range lines {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			return nil // trailing spaces would be invisible, so quote it instead
		}
	}
	return append([]string{chomp}, lines...)
}
