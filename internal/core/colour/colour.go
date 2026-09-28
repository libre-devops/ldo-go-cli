// Package colour decides whether to use colour, and holds the styles and palettes that
// apply it.
//
// One decision for everything written: what was asked (--colour or --no-colour), else
// NO_COLOR (off), else FORCE_COLOR (on), else colour only on a terminal. Colour is ANSI
// escape codes, so nothing here needs a library, and Strip takes them out again.
package colour

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Rainbow is 256-colour codes, red through to pink: the banner's bands, and brackets.
var Rainbow = []int{196, 208, 226, 46, 51, 33, 129, 201}

// palette is each kind of JSON value's 256-colour code (-1 for none), and bold.
var palette = map[string]struct {
	code int
	bold bool
}{
	"key": {75, true}, "string": {114, false}, "number": {215, false},
	"bool": {176, false}, "null": {244, false}, "punct": {-1, false},
}

var names = []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

const reset = "\x1b[0m"

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

var (
	mu     sync.Mutex
	choice *bool
)

// Use is what was asked: colour (true), none (false), or nil to leave it to the terminal.
func Use(asked *bool) {
	mu.Lock()
	defer mu.Unlock()
	choice = asked
}

// Setting is colour on or off, or nil for "on a terminal": what was asked, else NO_COLOR
// (off), else FORCE_COLOR (on, unless it is 0).
func Setting() *bool {
	mu.Lock()
	defer mu.Unlock()
	if choice != nil {
		return choice
	}
	off, on := false, true
	if os.Getenv("NO_COLOR") != "" {
		return &off
	}
	if force := os.Getenv("FORCE_COLOR"); force != "" && force != "0" {
		return &on
	}
	return nil
}

// Wanted reports whether to colour what goes to file: as decided, else when it is a
// terminal.
func Wanted(file *os.File) bool {
	if decided := Setting(); decided != nil {
		return *decided
	}
	return IsTerminal(file)
}

// IsTerminal reports whether file is a terminal (a character device).
func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Style is text in fg, and bold or dim, as ANSI codes; plain text when there is nothing
// to apply. fg is a 256-colour code (an int), a name such as "green" or "bright_black",
// or "#RRGGBB" (exactly that where the terminal can show it, else the nearest of 256);
// nil or "" for none.
func Style(text string, fg any, bold, dim bool) string {
	var codes []string
	if code := code(fg); code != "" {
		codes = append(codes, code)
	}
	if bold {
		codes = append(codes, "\x1b[1m")
	}
	if dim {
		codes = append(codes, "\x1b[2m")
	}
	if len(codes) == 0 {
		return text
	}
	return strings.Join(codes, "") + text + reset
}

func code(fg any) string {
	switch value := fg.(type) {
	case int:
		if value < 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[38;5;%dm", value)
	case string:
		if value == "" {
			return ""
		}
		if strings.HasPrefix(value, "#") && len(value) == 7 {
			if Truecolour() {
				r, g, b := rgb(value)
				return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
			}
			return fmt.Sprintf("\x1b[38;5;%dm", Nearest(value))
		}
		bright := strings.HasPrefix(value, "bright_")
		for index, name := range names {
			if strings.TrimPrefix(value, "bright_") == name {
				base := 30
				if bright {
					base = 90
				}
				return fmt.Sprintf("\x1b[%dm", base+index)
			}
		}
	}
	return ""
}

// Truecolour reports whether the terminal says it shows 24-bit colour: COLORTERM
// (truecolor or 24bit), as most set it, or Windows Terminal, which always does.
func Truecolour() bool {
	said := strings.ToLower(os.Getenv("COLORTERM"))
	_, windowsTerminal := os.LookupEnv("WT_SESSION")
	return said == "truecolor" || said == "24bit" || windowsTerminal
}

func rgb(hex string) (int, int, int) {
	parse := func(part string) int {
		value, _ := strconv.ParseInt(part, 16, 0)
		return int(value)
	}
	return parse(hex[1:3]), parse(hex[3:5]), parse(hex[5:7])
}

var levels = []int{0, 95, 135, 175, 215, 255}

// Nearest is the 256-colour code nearest #RRGGBB: from the 6x6x6 cube, or the grey ramp.
func Nearest(hex string) int {
	r, g, b := rgb(hex)
	step := func(value int) int {
		best := 0
		for index, level := range levels {
			if abs(level-value) < abs(levels[best]-value) {
				best = index
			}
		}
		return best
	}
	sr, sg, sb := step(r), step(g), step(b)
	cube := 16 + 36*sr + 6*sg + sb
	greyStep := min(23, max(0, int(float64((r+g+b)/3-8)/10+0.5)))
	grey := 8 + 10*greyStep
	distance := func(or, og, ob int) int {
		return (r-or)*(r-or) + (g-og)*(g-og) + (b-ob)*(b-ob)
	}
	if distance(levels[sr], levels[sg], levels[sb]) <= distance(grey, grey, grey) {
		return cube
	}
	return 232 + greyStep
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// Strip is text without its colour.
func Strip(text string) string { return ansi.ReplaceAllString(text, "") }

// Paint is text in the palette's style for kind: key, string, number, bool, null, punct.
func Paint(kind, text string) string {
	style := palette[kind]
	return Style(text, style.code, style.bold, false)
}

// Diagonal is line (row row of some art) in rainbow bands six wide that run along the
// diagonal, so any art gets the same sweep; one style per run of a colour.
func Diagonal(line string, row int) string {
	var out strings.Builder
	var run strings.Builder
	current := -1
	for column, char := range []rune(line) {
		shade := Rainbow[((column+2*row)/6)%len(Rainbow)]
		if shade != current && run.Len() > 0 {
			out.WriteString(Style(run.String(), current, true, false))
			run.Reset()
		}
		current = shade
		run.WriteRune(char)
	}
	if run.Len() > 0 {
		out.WriteString(Style(run.String(), current, true, false))
	}
	return out.String()
}

// JSON is indented JSON text (as json.MarshalIndent writes it) in colour: keys, strings,
// numbers, booleans and null each in the palette's colour, and brackets by how deeply
// they nest, so matching pairs share one. Strip the colour and the text is unchanged.
func JSON(text string) string {
	var out strings.Builder
	depth := 0
	runes := []rune(text)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		switch {
		case char == '"':
			end := stringEnd(runes, index)
			literal := string(runes[index : end+1])
			if isKey(runes, end+1) {
				out.WriteString(Paint("key", literal))
			} else {
				out.WriteString(Paint("string", literal))
			}
			index = end
		case char == '{' || char == '[':
			out.WriteString(Style(string(char), Rainbow[depth%len(Rainbow)], false, false))
			depth++
		case char == '}' || char == ']':
			depth--
			out.WriteString(Style(string(char), Rainbow[max(depth, 0)%len(Rainbow)], false, false))
		case char == '-' || (char >= '0' && char <= '9'):
			end := index
			for end+1 < len(runes) && strings.ContainsRune("0123456789.eE+-", runes[end+1]) {
				end++
			}
			out.WriteString(Paint("number", string(runes[index:end+1])))
			index = end
		case char == 't' || char == 'f' || char == 'n':
			word := "null"
			kind := "null"
			if char == 't' {
				word, kind = "true", "bool"
			} else if char == 'f' {
				word, kind = "false", "bool"
			}
			out.WriteString(Paint(kind, word))
			index += len(word) - 1
		default:
			out.WriteRune(char)
		}
	}
	return out.String()
}

func stringEnd(runes []rune, start int) int {
	for index := start + 1; index < len(runes); index++ {
		if runes[index] == '\\' {
			index++
			continue
		}
		if runes[index] == '"' {
			return index
		}
	}
	return len(runes) - 1
}

func isKey(runes []rune, after int) bool {
	for index := after; index < len(runes); index++ {
		switch runes[index] {
		case ' ', '\t':
			continue
		case ':':
			return true
		}
		return false
	}
	return false
}
