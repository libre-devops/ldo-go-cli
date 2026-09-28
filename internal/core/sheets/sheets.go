// Package sheets reads the cells of an Excel workbook (.xlsx, .xlsm, .xltx, .xltm) with the
// standard library.
//
// An Office Open XML workbook is a zip of XML parts: the workbook lists its sheets,
// relationship parts say which file holds each one, and most text lives once in a shared
// strings table that cells point into. Only cell values are read, and each cell's number
// format, so that a date reads as the date it shows (2026-09-25), not the number Excel
// keeps it as. Formulas are never evaluated (the value Excel saved with the file is used),
// and macros in an .xlsm are never touched.
//
// A workbook is untrusted input, so each part is read with a size cap (a zip can claim
// any size, and a small file can inflate to gigabytes) and a document type declaration is
// refused. Office never writes one. Parts must be UTF-8, as Office writes them.
package sheets

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Suffixes are the workbook suffixes this package reads.
var Suffixes = []string{".xlsx", ".xlsm", ".xltx", ".xltm"}

// otherFormats are spreadsheet formats this package cannot read, and what they are.
var otherFormats = map[string]string{
	".xls":     "the old binary Excel format",
	".xlsb":    "the binary Excel format",
	".ods":     "an OpenDocument spreadsheet",
	".numbers": "a Numbers spreadsheet",
}

// SaveAsHint is what to do with a spreadsheet this package cannot read.
const SaveAsHint = "save it as .xlsx or .csv"

// MaxPartBytes is how far any one part may inflate. A sheet of a million short rows fits.
var MaxPartBytes int64 = 128 * 1024 * 1024

// MaxColumns is Excel's own limit, column XFD.
const MaxColumns = 16384

var (
	oleMagic   = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}
	doctype    = []byte("<!DOCTYPE")
	utf8BOM    = []byte{0xef, 0xbb, 0xbf}
	encoding   = regexp.MustCompile(`^<\?xml[^>]*?\sencoding\s*=\s*["']([^"']*)["']`)
	cellColumn = regexp.MustCompile(`^([A-Za-z]{1,3})`)
)

// Row is the cell values of one row, left to right, and whether Excel hides it (by hand
// or by a filter; either way it is still in the file).
type Row struct {
	Cells  []string
	Hidden bool
}

// Sheet is one worksheet: its tab name, whether the tab is hidden, and its part.
type Sheet struct {
	Name   string
	Hidden bool
	Part   string
}

// Workbook is an open workbook. Close it when done.
type Workbook struct {
	Path     string
	Sheets   []Sheet
	archive  *zip.ReadCloser
	strings  []string
	formats  []DateKind
	date1904 bool
}

// IsWorkbook reports whether the file's suffix says it is a workbook this package reads.
func IsWorkbook(file string) bool {
	suffix := strings.ToLower(filepath.Ext(file))
	for _, known := range Suffixes {
		if suffix == known {
			return true
		}
	}
	return false
}

// CheckReadable refuses, with advice, a spreadsheet format this package cannot read.
func CheckReadable(file string) error {
	if kind, found := otherFormats[strings.ToLower(filepath.Ext(file))]; found {
		return errs.Inputf("%s is %s, which cannot be read", file, kind).WithHint(SaveAsHint)
	}
	return nil
}

// Open is file as a workbook, or an Input error saying why it cannot be one.
func Open(file string) (*Workbook, error) {
	archive, err := zip.OpenReader(file)
	if err != nil {
		return nil, openError(file, err)
	}
	book := &Workbook{Path: file, archive: archive}
	if err := book.readStructure(); err != nil {
		archive.Close()
		return nil, err
	}
	return book, nil
}

func openError(file string, err error) error {
	if errors.Is(err, zip.ErrFormat) {
		if head, readErr := readHead(file); readErr == nil && bytes.Equal(head, oleMagic) {
			return errs.Inputf("%s is protected with a password, or in the old binary format", file).
				WithHint("remove the password, or %s", SaveAsHint)
		}
		return errs.Inputf("%s is not an Excel workbook", file).WithHint(SaveAsHint)
	}
	return errs.Inputf("cannot read %s: %v", file, err)
}

func readHead(file string) ([]byte, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	head := make([]byte, len(oleMagic))
	_, err = io.ReadFull(handle, head)
	return head, err
}

// Close closes the workbook's zip file.
func (w *Workbook) Close() error { return w.archive.Close() }

// Sheet is the sheet with this tab name, matched without case. Hidden ones count.
func (w *Workbook) Sheet(name string) (Sheet, error) {
	wanted := strings.TrimSpace(name)
	var names []string
	for _, sheet := range w.Sheets {
		if strings.EqualFold(sheet.Name, wanted) {
			return sheet, nil
		}
		names = append(names, sheet.Name)
	}
	return Sheet{}, errs.Inputf("%s has no sheet '%s'", w.Path, name).WithHint("sheets: %s", strings.Join(names, ", "))
}

type xmlRich struct {
	Items []struct {
		XMLName xml.Name
		Text    string `xml:",chardata"`
		Runs    []struct {
			XMLName xml.Name
			Text    string `xml:",chardata"`
		} `xml:",any"`
	} `xml:",any"`
}

// text is a string item's text: a plain t, or the t of each run, in order. Phonetic
// guides (rPh) are not part of the text, so they are left out.
func (r xmlRich) text() string {
	var parts strings.Builder
	for _, item := range r.Items {
		switch item.XMLName.Local {
		case "t":
			parts.WriteString(item.Text)
		case "r":
			for _, run := range item.Runs {
				if run.XMLName.Local == "t" {
					parts.WriteString(run.Text)
				}
			}
		}
	}
	return parts.String()
}

type xmlCell struct {
	Ref    string   `xml:"r,attr"`
	Type   string   `xml:"t,attr"`
	Style  string   `xml:"s,attr"`
	Value  *string  `xml:"v"`
	Inline *xmlRich `xml:"is"`
}

type xmlRow struct {
	Hidden string    `xml:"hidden,attr"`
	Cells  []xmlCell `xml:"c"`
}

// Rows is every row the sheet stores, in order. Empty rows Excel left out stay out.
func (w *Workbook) Rows(sheet Sheet) ([]Row, error) {
	var rows []Row
	err := w.elements(sheet.Part, "row", func(decoder *xml.Decoder, start xml.StartElement) error {
		var row xmlRow
		if err := decoder.DecodeElement(&row, &start); err != nil {
			return err
		}
		cells, err := w.rowCells(row)
		if err != nil {
			return err
		}
		rows = append(rows, Row{Cells: cells, Hidden: row.Hidden == "1" || row.Hidden == "true"})
		return nil
	})
	return rows, err
}

func (w *Workbook) rowCells(row xmlRow) ([]string, error) {
	values := map[int]string{}
	position, last := -1, -1
	for _, cell := range row.Cells {
		if match := cellColumn.FindStringSubmatch(cell.Ref); match != nil {
			position = columnIndex(match[1])
		} else {
			position++
		}
		if position < MaxColumns {
			value, err := w.cellValue(cell)
			if err != nil {
				return nil, err
			}
			values[position] = value
			last = max(last, position)
		}
	}
	if last < 0 {
		return nil, nil
	}
	cells := make([]string, last+1)
	for index, value := range values {
		cells[index] = value
	}
	return cells, nil
}

func (w *Workbook) cellValue(cell xmlCell) (string, error) {
	if cell.Type == "inlineStr" {
		if cell.Inline == nil {
			return "", nil
		}
		return cell.Inline.text(), nil
	}
	text := ""
	if cell.Value != nil {
		text = *cell.Value
	}
	switch cell.Type {
	case "s":
		index, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || index < 0 || index >= len(w.strings) {
			return "", errs.Inputf("%s is damaged: a cell points at a missing shared string", w.Path)
		}
		return w.strings[index], nil
	case "b":
		if text == "1" {
			return "TRUE", nil
		}
		return "FALSE", nil
	case "e":
		return "", nil // #N/A, #REF! and the like hold no value worth reading
	case "", "n":
		if text != "" {
			return w.asDate(cell, text), nil
		}
	}
	return text, nil
}

// asDate is a number as the date or time its format shows, else the number as it is.
func (w *Workbook) asDate(cell xmlCell, text string) string {
	style, err := strconv.Atoi(cell.Style)
	if err != nil || style < 0 || style >= len(w.formats) || w.formats[style] == "" {
		return text
	}
	serial, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text
	}
	if shown := FromSerial(serial, w.formats[style], w.date1904); shown != "" {
		return shown
	}
	return text
}

type relationship struct {
	kind string
	part string
}

func (w *Workbook) has(part string) bool {
	for _, file := range w.archive.File {
		if file.Name == part {
			return true
		}
	}
	return false
}

func (w *Workbook) readStructure() error {
	if !w.has("_rels/.rels") {
		return errs.Inputf("%s is not an Excel workbook", w.Path).WithHint(SaveAsHint)
	}
	top, err := w.relationships("_rels/.rels", "")
	if err != nil {
		return err
	}
	workbookPart := target(top, "officeDocument")
	if workbookPart == "" {
		return errs.Inputf("%s is not an Excel workbook", w.Path).WithHint(SaveAsHint)
	}
	folder := path.Dir(workbookPart)
	related, err := w.relationships(path.Join(folder, "_rels", path.Base(workbookPart)+".rels"), folder)
	if err != nil {
		return err
	}
	if err := w.readSheets(workbookPart, related); err != nil {
		return err
	}
	if part := target(related, "sharedStrings"); part != "" {
		if err := w.readStrings(part); err != nil {
			return err
		}
	}
	if part := target(related, "styles"); part != "" && w.has(part) {
		return w.readFormats(part)
	}
	return nil
}

func (w *Workbook) readSheets(part string, related map[string]relationship) error {
	err := w.elements(part, "", func(decoder *xml.Decoder, start xml.StartElement) error {
		switch start.Name.Local {
		case "sheet":
			var sheet struct {
				Name  string `xml:"name,attr"`
				State string `xml:"state,attr"`
				// r:id, in a namespace that differs between the transitional and strict
				// flavours of the format, so matched by local name.
				ID string `xml:"id,attr"`
			}
			if err := decoder.DecodeElement(&sheet, &start); err != nil {
				return err
			}
			// Chart sheets and dialog sheets hold no cells.
			if found := related[sheet.ID]; found.kind == "worksheet" {
				hidden := sheet.State != "" && sheet.State != "visible"
				w.Sheets = append(w.Sheets, Sheet{Name: sheet.Name, Hidden: hidden, Part: found.part})
			}
		case "workbookPr":
			for _, attr := range start.Attr {
				if attr.Name.Local == "date1904" {
					w.date1904 = attr.Value == "1" || attr.Value == "true"
				}
			}
		}
		return nil
	})
	if err == nil && len(w.Sheets) == 0 {
		return errs.Inputf("%s has no worksheets", w.Path)
	}
	return err
}

// relationships are relationship id to type and part path, for a relationships part.
func (w *Workbook) relationships(part, folder string) (map[string]relationship, error) {
	found := map[string]relationship{}
	err := w.elements(part, "Relationship", func(decoder *xml.Decoder, start xml.StartElement) error {
		var item struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		}
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return err
		}
		if item.Mode == "External" {
			return nil
		}
		location := path.Join(folder, item.Target)
		if strings.HasPrefix(item.Target, "/") {
			location = item.Target[1:]
		}
		kind := item.Type[strings.LastIndex(item.Type, "/")+1:]
		found[item.ID] = relationship{kind: kind, part: path.Clean(location)}
		return nil
	})
	return found, err
}

func target(relationships map[string]relationship, kind string) string {
	for _, found := range relationships {
		if found.kind == kind {
			return found.part
		}
	}
	return ""
}

// readFormats is what each cell format in the styles part shows, in order.
func (w *Workbook) readFormats(part string) error {
	codes := map[int]string{}
	var ids []int
	var inCellFormats bool
	err := w.elements(part, "", func(decoder *xml.Decoder, start xml.StartElement) error {
		switch start.Name.Local {
		case "numFmt":
			var format struct {
				ID   string `xml:"numFmtId,attr"`
				Code string `xml:"formatCode,attr"`
			}
			if err := decoder.DecodeElement(&format, &start); err != nil {
				return err
			}
			codes[number(format.ID)] = format.Code
		case "cellXfs":
			var formats struct {
				Items []struct {
					ID string `xml:"numFmtId,attr"`
				} `xml:"xf"`
			}
			if err := decoder.DecodeElement(&formats, &start); err != nil {
				return err
			}
			inCellFormats = true
			for _, item := range formats.Items {
				ids = append(ids, number(item.ID))
			}
		}
		return nil
	})
	if err != nil || !inCellFormats {
		return err
	}
	for _, id := range ids {
		var code *string
		if found, ok := codes[id]; ok {
			code = &found
		}
		w.formats = append(w.formats, FormatKind(id, code))
	}
	return nil
}

func (w *Workbook) readStrings(part string) error {
	return w.elements(part, "si", func(decoder *xml.Decoder, start xml.StartElement) error {
		var item xmlRich
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return err
		}
		w.strings = append(w.strings, item.text())
		return nil
	})
}

// number is an id attribute as a number; a missing or odd one is 0, the General format.
func number(value string) int {
	found, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return found
}

// columnIndex is a zero-based column: A is 0, Z is 25, AA is 26.
func columnIndex(letters string) int {
	index := 0
	for _, letter := range strings.ToUpper(letters) {
		index = index*26 + int(letter-'A') + 1
	}
	return index - 1
}

// elements calls each for every element called name (any namespace; every element when
// name is empty) in part, reading it through the size and DTD guard.
func (w *Workbook) elements(part, name string, each func(*xml.Decoder, xml.StartElement) error) error {
	var file *zip.File
	for _, candidate := range w.archive.File {
		if candidate.Name == part {
			file = candidate
		}
	}
	if file == nil {
		return errs.Inputf("%s is damaged: it has no %s", w.Path, part)
	}
	stream, err := file.Open()
	if err != nil {
		return errs.Inputf("cannot read %s in %s: %v", part, w.Path, err)
	}
	defer stream.Close()
	decoder := xml.NewDecoder(&guard{reader: stream, part: part, path: w.Path})
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return w.damaged(part, err)
		}
		if start, ok := token.(xml.StartElement); ok && (name == "" || start.Name.Local == name) {
			if err := each(decoder, start); err != nil {
				return w.damaged(part, err)
			}
		}
	}
}

func (w *Workbook) damaged(part string, err error) error {
	var guarded *errs.Error
	if errors.As(err, &guarded) {
		return guarded
	}
	return errs.Inputf("%s is damaged: %s: %v", w.Path, part, err)
}

// guard is a part's bytes, capped in size, refusing a document type declaration and any
// encoding but UTF-8.
type guard struct {
	reader  io.Reader
	part    string
	path    string
	total   int64
	tail    []byte
	checked bool
}

func (g *guard) Read(buffer []byte) (int, error) {
	var count int
	var err error
	if g.checked {
		count, err = g.reader.Read(buffer)
	} else {
		// The first read is long enough to hold the whole XML declaration.
		count, err = io.ReadAtLeast(g.reader, buffer, min(len(buffer), 1024))
		if errors.Is(err, io.ErrUnexpectedEOF) {
			err = nil
		}
	}
	if count > 0 {
		chunk := buffer[:count]
		if !g.checked {
			g.checked = true
			if problem := g.checkUTF8(chunk); problem != nil {
				return 0, problem
			}
		}
		g.total += int64(count)
		if g.total > MaxPartBytes {
			return 0, errs.Inputf("%s is too large to read: %s inflates past %d MiB", g.path, g.part, MaxPartBytes/(1024*1024))
		}
		// Keep the end of the previous chunk, so a declaration split across two is seen.
		if bytes.Contains(append(append([]byte(nil), g.tail...), chunk...), doctype) {
			return 0, errs.Inputf("%s is not a workbook Office wrote: %s declares a DTD", g.path, g.part)
		}
		keep := min(len(chunk), len(doctype)-1)
		g.tail = append(g.tail[:0], chunk[len(chunk)-keep:]...)
	}
	return count, err
}

// checkUTF8 refuses a part that is not UTF-8, so the byte check for a DTD means what it
// says. The first read of a zip entry holds its XML declaration.
func (g *guard) checkUTF8(start []byte) error {
	text := bytes.TrimPrefix(start, utf8BOM)
	declared := encoding.FindSubmatch(text)
	unfinished := bytes.HasPrefix(text, []byte("<?xml")) && !bytes.Contains(text, []byte("?>"))
	if !bytes.HasPrefix(text, []byte("<")) || unfinished ||
		(declared != nil && !strings.EqualFold(string(declared[1]), "utf-8") && !strings.EqualFold(string(declared[1]), "utf8")) {
		return errs.Inputf("%s is not a workbook Office wrote: %s is not UTF-8", g.path, g.part)
	}
	return nil
}
