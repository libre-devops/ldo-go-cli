// Package render writes output. Data goes to stdout; notes and warnings go to stderr.
//
// Every data command offers its shapes through -o: an aligned table for people, JSON (the
// services' records) for jq and scripts, CSV for spreadsheets, TSV for shell pipelines
// and HTML for a page to open or share. --sort and --unique arrange the rows of the
// table, CSV, TSV and HTML first. Tables are aligned before they are coloured, so colour
// codes never upset the alignment.
package render

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/sorting"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// Output is a shape a data command can write.
type Output string

// The shapes.
const (
	Table Output = "table"
	JSON  Output = "json"
	CSV   Output = "csv"
	TSV   Output = "tsv"
	HTML  Output = "html"
)

// Outputs are every shape, in the order help lists them.
var Outputs = []Output{Table, JSON, CSV, TSV, HTML}

// ParseOutput reads an -o value.
func ParseOutput(value string) (Output, error) {
	for _, output := range Outputs {
		if strings.EqualFold(value, string(output)) {
			return output, nil
		}
	}
	return "", errs.Inputf("%q is not an output shape", value).WithHint("use table, json, csv, tsv or html")
}

// Cell is one table cell: its text, and a colour name ("green", "yellow", "red",
// "bright_black" and the rest) or empty for none.
type Cell struct {
	Text   string
	Colour string
}

// Plain is a cell without colour.
func Plain(text string) Cell { return Cell{Text: text} }

// Coloured is a cell in a colour.
func Coloured(text, name string) Cell { return Cell{Text: text, Colour: name} }

// Cells is plain cells, one for each text.
func Cells(texts ...string) []Cell {
	row := make([]Cell, len(texts))
	for index, text := range texts {
		row[index] = Plain(text)
	}
	return row
}

// Order is one --sort: a column by name, and which way.
type Order struct {
	Column     string
	Descending bool
}

// Console is where a run writes: data to Out, everything else to Err.
type Console struct {
	Out io.Writer
	Err io.Writer
	// OutFile and ErrFile are the files behind Out and Err, when they are files: whether
	// to colour, and how wide a terminal is, are asked of them.
	OutFile *os.File
	ErrFile *os.File
	// Width cuts a table's last column to fit, when set; else a terminal's width is used.
	Width int
	// Structured is a json or otlp log format: notes, warnings and errors become log
	// records then, so stderr is clean JSON Lines.
	Structured bool
	Logger     *slog.Logger
	// Arrange is --sort and --unique.
	Sort   []Order
	Unique []string
	// Report gathers what -o html writes, when the command finishes.
	Report *Report
	// Now is time.Now when nil, for relative ages.
	Now func() time.Time
}

// Stdio is a console on the process's own stdout and stderr.
func Stdio() *Console {
	return &Console{Out: os.Stdout, Err: os.Stderr, OutFile: os.Stdout, ErrFile: os.Stderr}
}

func (c *Console) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ColourOut reports whether to colour stdout.
func (c *Console) ColourOut() bool { return colour.Wanted(c.OutFile) }

// ColourErr reports whether to colour stderr.
func (c *Console) ColourErr() bool { return colour.Wanted(c.ErrFile) }

func (c *Console) styleErr(text, fg string, bold, dim bool) string {
	if !c.ColourErr() {
		return text
	}
	return colour.Style(text, fg, bold, dim)
}

// Note is a dim informational line on stderr (an INFO record when structured).
func (c *Console) Note(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.Report.add("note", text)
	if c.Structured && c.Logger != nil {
		c.Logger.Info(text)
		return
	}
	fmt.Fprintln(c.Err, c.styleErr(text, "bright_black", false, false))
}

// Warn is a warning on stderr (a WARNING record when structured).
func (c *Console) Warn(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.Report.add("warning", text)
	if c.Structured && c.Logger != nil {
		c.Logger.Warn(text)
		return
	}
	fmt.Fprintln(c.Err, c.styleErr("warning: "+text, "yellow", false, false))
}

// Error is an error, and what to do about it, on stderr (an ERROR record, the hint an
// attribute, when structured).
func (c *Console) Error(text, hint string) {
	if c.Structured && c.Logger != nil {
		if hint != "" {
			c.Logger.Error(text, "hint", hint)
		} else {
			c.Logger.Error(text)
		}
		return
	}
	fmt.Fprintln(c.Err, c.styleErr("error: "+text, "red", false, false))
	if hint != "" {
		fmt.Fprintln(c.Err, c.styleErr("hint: "+hint, "yellow", false, false))
	}
}

// Notify is something the person must see now, such as a sign-in code: never filtered.
func (c *Console) Notify(text string) {
	if c.Structured && c.Logger != nil {
		c.Logger.Warn(text)
		return
	}
	fmt.Fprintln(c.Err, c.styleErr(text, "cyan", false, false))
}

// Println writes a line of data to stdout.
func (c *Console) Println(text string) { fmt.Fprintln(c.Out, text) }

// JSONText is data as indented JSON, without HTML escaping, as json.dumps writes it.
func JSONText(data any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return "", fmt.Errorf("cannot write JSON: %w", err)
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// PrintJSON writes data as JSON to stdout: coloured on a terminal, plain when piped.
func (c *Console) PrintJSON(data any) error {
	text, err := JSONText(data)
	if err != nil {
		return err
	}
	if c.ColourOut() {
		text = colour.JSON(text)
	}
	fmt.Fprintln(c.Out, text)
	return nil
}

// Emit writes data in the chosen shape: rows for a table, CSV, TSV or HTML, records for
// JSON. The rows are sorted and made unique first, as --sort and --unique asked.
func (c *Console) Emit(output Output, headers []string, rows [][]Cell, records any) error {
	if output == JSON {
		c.jsonIsNotArranged()
		if records == nil {
			records = []any{}
		}
		return c.PrintJSON(records)
	}
	rows, err := c.Arranged(headers, rows)
	if err != nil {
		return err
	}
	switch output {
	case HTML:
		c.Report.gather(headers, rows)
	case CSV:
		_, err = io.WriteString(c.Out, CSVText(headers, rows))
	case TSV:
		_, err = io.WriteString(c.Out, TSVText(rows))
	default:
		fmt.Fprintln(c.Out, c.Table(headers, rows))
	}
	return err
}

func (c *Console) jsonIsNotArranged() {
	if len(c.Sort) > 0 || len(c.Unique) > 0 {
		c.Warn("--sort and --unique arrange table, CSV and TSV rows; for JSON use jq's sort_by")
	}
}

// Arranged is rows sorted by the --sort columns, then one for each --unique value.
func (c *Console) Arranged(headers []string, rows [][]Cell) ([][]Cell, error) {
	if len(c.Sort) == 0 && len(c.Unique) == 0 {
		return rows, nil
	}
	var keys []sorting.Key
	for _, order := range c.Sort {
		index, err := sorting.ColumnIndex(headers, order.Column)
		if err != nil {
			return nil, err
		}
		keys = append(keys, sorting.Key{Column: index, Descending: order.Descending})
	}
	text := func(row []Cell, column int) string {
		if column < len(row) {
			return row[column].Text
		}
		return ""
	}
	arranged := sorting.SortRows(rows, keys, text)
	if len(c.Unique) > 0 {
		var columns []int
		for _, name := range c.Unique {
			index, err := sorting.ColumnIndex(headers, name)
			if err != nil {
				return nil, err
			}
			columns = append(columns, index)
		}
		arranged = sorting.Unique(arranged, columns, text)
	}
	return arranged, nil
}

// CSVText is CSV with a header row, colours dropped.
func CSVText(headers []string, rows [][]Cell) string {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write(headers)
	for _, row := range rows {
		record := make([]string, len(row))
		for index, cell := range row {
			record[index] = cell.Text
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	return strings.ReplaceAll(buffer.String(), "\r\n", "\n")
}

var tsvUnsafe = regexp.MustCompile(`[\t\r\n]+`)

// TSVText is tab-separated values, one row a line, with no header: the Azure CLI's
// -o tsv, for shell pipelines. A tab or line break inside a value becomes a space, so a
// row is always one line.
func TSVText(rows [][]Cell) string {
	var out strings.Builder
	for _, row := range rows {
		cells := make([]string, len(row))
		for index, cell := range row {
			cells[index] = tsvUnsafe.ReplaceAllString(cell.Text, " ")
		}
		out.WriteString(strings.Join(cells, "\t") + "\n")
	}
	return out.String()
}

// Table is rows as a table on this console: coloured and cut to fit when stdout is a
// terminal, plain and whole when it is piped.
func (c *Console) Table(headers []string, rows [][]Cell) string {
	width := c.Width
	if width == 0 && c.OutFile != nil && colour.IsTerminal(c.OutFile) {
		width = terminalWidth(c.OutFile)
	}
	return FormatTable(headers, rows, width, c.ColourOut())
}

// FormatTable is left-aligned columns separated by two spaces, with a rule under the
// header. With width, the last column (a detail or description, usually) is cut to fit,
// with an ellipsis, so a long message never wraps across the table.
func FormatTable(headers []string, rows [][]Cell, width int, coloured bool) string {
	body := make([][]Cell, len(rows))
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = runeLen(header)
	}
	for r, row := range rows {
		body[r] = make([]Cell, len(headers))
		for index := range headers {
			cell := Cell{}
			if index < len(row) {
				cell = row[index]
			}
			if cell.Text == "" {
				cell.Text = "-"
			}
			body[r][index] = cell
			widths[index] = max(widths[index], runeLen(cell.Text))
		}
	}
	gaps := 2 * (len(widths) - 1)
	if width > 0 && len(widths) > 0 && sum(widths)+gaps > width {
		if room := width - sum(widths[:len(widths)-1]) - gaps; room >= 20 {
			widths[len(widths)-1] = room
			for _, row := range body {
				last := &row[len(row)-1]
				if runeLen(last.Text) > room {
					last.Text = string([]rune(last.Text)[:room-1]) + "…"
				}
			}
		}
	}
	headerCells := make([]Cell, len(headers))
	rules := make([]Cell, len(headers))
	for index, header := range headers {
		headerCells[index] = Plain(header)
		rules[index] = Plain(strings.Repeat("-", widths[index]))
	}
	lines := []string{line(headerCells, widths, true, coloured), line(rules, widths, false, coloured)}
	for _, row := range body {
		lines = append(lines, line(row, widths, false, coloured))
	}
	return strings.Join(lines, "\n")
}

func line(cells []Cell, widths []int, bold, coloured bool) string {
	parts := make([]string, len(cells))
	for index, cell := range cells {
		padded := cell.Text
		if index < len(cells)-1 {
			padded += strings.Repeat(" ", widths[index]-runeLen(cell.Text))
		}
		if coloured {
			padded = colour.Style(padded, cell.Colour, bold, false)
		}
		parts[index] = padded
	}
	return strings.Join(parts, "  ")
}

func runeLen(text string) int { return utf8.RuneCountInString(text) }

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

// Pairs is aligned "label  value" lines; an empty value shows as "-".
func Pairs(items [][2]string, coloured bool) string {
	width := 0
	for _, item := range items {
		width = max(width, runeLen(item[0]))
	}
	lines := make([]string, len(items))
	for index, item := range items {
		label := item[0] + strings.Repeat(" ", width-runeLen(item[0]))
		if coloured {
			label = colour.Style(label, nil, true, false)
		}
		value := item[1]
		if value == "" {
			value = "-"
		}
		lines[index] = label + "  " + value
	}
	return strings.Join(lines, "\n")
}

// When is a time in local time plus a relative age: 2026-09-24 14:05 (3h 02m ago); "-"
// for the zero time.
func When(value, now time.Time) string {
	if value.IsZero() {
		return "-"
	}
	relative := util.FormatDuration(now.Sub(value)) + " ago"
	if value.After(now) {
		relative = "in " + util.FormatDuration(value.Sub(now))
	}
	return value.Local().Format("2006-01-02 15:04") + " (" + relative + ")"
}

// When is a time as this console shows it: local, with its age.
func (c *Console) When(value time.Time) string { return When(value, c.now()) }

// Moment is local time to the second, for events in order: 2026-09-24 14:05:31.
func Moment(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

// YesNo is "yes", "no", or "-" when it is not known.
func YesNo(value *bool) string {
	switch {
	case value == nil:
		return "-"
	case *value:
		return "yes"
	}
	return "no"
}

// ISO is a time as JSON records carry it (RFC 3339 in UTC), or nil for the zero time.
func ISO(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
