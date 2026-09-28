// Package inputs reads a list of names from arguments, stdin, a text file, a CSV or an
// Excel workbook.
//
// Commands that take devices, users or vaults accept them however they are to hand:
// "a,b,c", several arguments, - for stdin, or a file. A text file holds names separated by
// commas, spaces or new lines, with # comments. A CSV file (a .csv suffix, or any file
// when a column is named) and an Excel workbook are read by column header, so a plan, an
// export from a portal or a spreadsheet someone emailed works as it is, title rows above
// the header and all. Its rows can be filtered by other columns (package rowfilters).
package inputs

import (
	"bytes"
	"encoding/csv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/rowfilters"
	"github.com/libre-devops/ldo-go-cli/internal/core/sheets"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// HeaderSearchRows is how far down a header is looked for: with a column named, it is
// the first of this many non-blank rows to have it.
const HeaderSearchRows = 25

// Options say where names come from besides the arguments.
type Options struct {
	// Stdin is read for a - among the values.
	Stdin io.Reader
	// File is a text file, CSV or workbook to read names from.
	File string
	// Column is the header of the column of names, in a CSV or workbook.
	Column string
	// Sheet picks a workbook's sheet by tab name.
	Sheet string
	// Where keeps only the rows of the table that meet every condition.
	Where []rowfilters.Condition
}

// ReadNames is every name given, in order, with blanks and repeats (whatever their case)
// dropped.
//
// - among values reads Stdin in its place. Column picks a column (by header, without
// case) of a CSV or workbook File, or of CSV on stdin when there is no file. Sheet picks a
// workbook's sheet by tab name; without it, the one visible sheet with that column is
// used, or the first visible sheet when no column is named. Where keeps only the rows of
// that table that meet every condition; names given as values are kept as they are.
func ReadNames(values []string, opts Options) ([]string, error) {
	if err := check(values, opts); err != nil {
		return nil, err
	}
	var collected []string
	for _, value := range values {
		if value != "-" {
			collected = append(collected, util.SplitNames([]string{value})...)
			continue
		}
		if opts.Stdin == nil {
			return nil, errs.Inputf("'-' reads names from stdin, but there is no stdin")
		}
		data, err := io.ReadAll(opts.Stdin)
		if err != nil {
			return nil, errs.Inputf("cannot read stdin: %v", err)
		}
		found, err := fromStdin(string(data), opts)
		if err != nil {
			return nil, err
		}
		collected = append(collected, found...)
	}
	if opts.File != "" {
		found, err := fromFile(opts)
		if err != nil {
			return nil, err
		}
		collected = append(collected, found...)
	}
	return dedupe(collected), nil
}

func check(values []string, opts Options) error {
	if opts.Sheet != "" && (opts.File == "" || !sheets.IsWorkbook(opts.File)) {
		return errs.Inputf("--sheet applies to an Excel workbook only")
	}
	if len(opts.Where) > 0 && opts.Column == "" {
		return errs.Inputf("--where filters the rows of a table, so it needs the column of names").
			WithHint("name it with --column, e.g. --column FQDN")
	}
	hasStdin := false
	for _, value := range values {
		hasStdin = hasStdin || value == "-"
	}
	if len(opts.Where) > 0 && opts.File == "" && !hasStdin {
		return errs.Inputf("--where filters the rows of a file").WithHint("read the names with -f FILE")
	}
	return nil
}

func fromStdin(text string, opts Options) ([]string, error) {
	if opts.Column != "" {
		return fromCSV(text, opts.Column, "stdin", opts.Where)
	}
	return fromText(text), nil
}

func fromFile(opts Options) ([]string, error) {
	if err := sheets.CheckReadable(opts.File); err != nil {
		return nil, err
	}
	if sheets.IsWorkbook(opts.File) {
		return fromWorkbook(opts)
	}
	data, err := os.ReadFile(opts.File)
	if err != nil {
		return nil, errs.Inputf("cannot read %s: %v", opts.File, err)
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) {
		return nil, errs.Inputf("%s is not a text file", opts.File).WithHint("use a text file, a CSV or an Excel workbook")
	}
	if opts.Column != "" || strings.EqualFold(filepath.Ext(opts.File), ".csv") {
		return fromCSV(string(data), opts.Column, opts.File, opts.Where)
	}
	return fromText(string(data)), nil
}

func fromText(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		content, _, _ := strings.Cut(line, "#")
		lines = append(lines, content)
	}
	return util.SplitNames(lines)
}

func dedupe(names []string) []string {
	seen := map[string]bool{}
	var kept []string
	for _, name := range names {
		key := strings.ToLower(name)
		if !seen[key] {
			seen[key] = true
			kept = append(kept, name)
		}
	}
	return kept
}

func fromCSV(text, column, source string, where []rowfilters.Condition) ([]string, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, errs.Inputf("cannot read %s as CSV: %v", source, err)
	}
	rows := make([]sheets.Row, len(records))
	for index, record := range records {
		rows[index] = sheets.Row{Cells: record}
	}
	return columnValues(rows, column, source, where)
}

func fromWorkbook(opts Options) ([]string, error) {
	book, err := sheets.Open(opts.File)
	if err != nil {
		return nil, err
	}
	defer book.Close()
	var chosen sheets.Sheet
	if opts.Sheet != "" {
		chosen, err = book.Sheet(opts.Sheet)
	} else {
		chosen, err = pickSheet(book, opts.Column)
	}
	if err != nil {
		return nil, err
	}
	rows, err := book.Rows(chosen)
	if err != nil {
		return nil, err
	}
	return columnValues(rows, opts.Column, "sheet '"+chosen.Name+"' of "+opts.File, opts.Where)
}

// pickSheet is the one visible sheet with column in its header, or the first visible one.
func pickSheet(book *sheets.Workbook, column string) (sheets.Sheet, error) {
	var visible, all []string
	var visibleSheets []sheets.Sheet
	for _, sheet := range book.Sheets {
		all = append(all, sheet.Name)
		if !sheet.Hidden {
			visibleSheets = append(visibleSheets, sheet)
			visible = append(visible, sheet.Name)
		}
	}
	if len(visibleSheets) == 0 {
		return sheets.Sheet{}, errs.Inputf("%s has only hidden sheets", book.Path).
			WithHint("name one with --sheet (%s)", strings.Join(all, ", "))
	}
	if column == "" {
		return visibleSheets[0], nil
	}
	var having []sheets.Sheet
	var havingNames []string
	for _, sheet := range visibleSheets {
		rows, err := book.Rows(sheet)
		if err != nil {
			return sheets.Sheet{}, err
		}
		if _, _, _, err := header(rows, column, ""); err == nil {
			having = append(having, sheet)
			havingNames = append(havingNames, sheet.Name)
		}
	}
	switch {
	case len(having) == 1:
		return having[0], nil
	case len(having) > 1:
		return sheets.Sheet{}, errs.Inputf("several sheets of %s have a column '%s'", book.Path, column).
			WithHint("pick one with --sheet (%s)", strings.Join(havingNames, ", "))
	}
	return sheets.Sheet{}, errs.Inputf("no sheet of %s has a column '%s'", book.Path, column).
		WithHint("check the header, or pick a sheet with --sheet (%s)", strings.Join(visible, ", "))
}

// columnValues is the non-blank values under the header column, or under the only
// header, from the rows that meet every where condition.
func columnValues(rows []sheets.Row, column, source string, where []rowfilters.Condition) ([]string, error) {
	index, headerCells, remaining, err := header(rows, column, source)
	if err != nil {
		return nil, err
	}
	if len(where) > 0 {
		if remaining, err = matching(remaining, headerCells, source, where); err != nil {
			return nil, err
		}
	}
	var values []string
	hidden := map[string]bool{}
	for _, row := range remaining {
		// Cells are kept whole, so a column may hold names with spaces in them.
		value := ""
		if index < len(row.Cells) {
			value = strings.TrimSpace(row.Cells[index])
		}
		if value != "" {
			values = append(values, value)
			if row.Hidden {
				hidden[strings.ToLower(value)] = true
			}
		}
	}
	// Rows --where chose are meant, whatever Excel's own filter shows.
	if len(hidden) > 0 && len(where) == 0 {
		slog.Warn(strconv.Itoa(len(hidden)) + " name(s) in " + source + " are in rows hidden or filtered out in Excel, and are " +
			"included: to read only some rows, pick them by value with --where")
	}
	return values, nil
}

// matching is the rows that meet every condition; an error naming what the column holds
// when none do, since a filter that matched nothing is more often a typo than an empty day.
func matching(rows []sheets.Row, headerCells []string, source string, where []rowfilters.Condition) ([]sheets.Row, error) {
	cells := make([][]string, len(rows))
	for index, row := range rows {
		cells[index] = row.Cells
	}
	test, err := rowfilters.RowTest(where, headerCells, cells, source)
	if err != nil {
		return nil, err
	}
	var kept []sheets.Row
	for _, row := range rows {
		if test(row.Cells) {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		var texts []string
		for _, condition := range where {
			texts = append(texts, "'"+condition.Text+"'")
		}
		return nil, errs.Inputf("no rows of %s match %s", source, strings.Join(texts, " and ")).
			WithHint("%s holds: %s", where[0].Column, rowfilters.Examples(cells, headerCells, where[0].Column))
	}
	return kept, nil
}

// header finds the header row: the column's index, the header, and the rows after it.
//
// Without column, the header is the first non-blank row, and it must name one column
// only. With it, the header is the first of the first few non-blank rows to have that
// column, so title rows above the header do no harm.
func header(rows []sheets.Row, column, source string) (int, []string, []sheets.Row, error) {
	wanted := strings.TrimSpace(column)
	var first []string
	seen := 0
	for position, row := range rows {
		if blank(row.Cells) {
			continue
		}
		if seen++; seen > HeaderSearchRows {
			break
		}
		cells := make([]string, len(row.Cells))
		for index, cell := range row.Cells {
			cells[index] = strings.TrimSpace(cell)
		}
		if first == nil {
			first = cells
		}
		if wanted == "" {
			return onlyColumn(cells, rows[position+1:], source)
		}
		for index, cell := range cells {
			if strings.EqualFold(cell, wanted) {
				return index, cells, rows[position+1:], nil
			}
		}
	}
	if first == nil {
		return 0, nil, nil, errs.Inputf("%s has no header row", source)
	}
	return 0, nil, nil, errs.Inputf("%s has no column '%s'", source, column).WithHint("columns: %s", strings.Join(named(first), ", "))
}

func onlyColumn(cells []string, rest []sheets.Row, source string) (int, []string, []sheets.Row, error) {
	var indexes []int
	for index, cell := range cells {
		if cell != "" {
			indexes = append(indexes, index)
		}
	}
	if len(indexes) == 1 {
		return indexes[0], cells, rest, nil
	}
	return 0, nil, nil, errs.Inputf("%s has several columns", source).
		WithHint("pick one with --column (%s)", strings.Join(named(cells), ", "))
}

func named(cells []string) []string {
	var found []string
	for _, cell := range cells {
		if cell != "" {
			found = append(found, cell)
		}
	}
	return found
}

func blank(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
