// Package workbookfake builds Excel workbooks by hand, laid out as Excel saves them, for
// tests of what reads them.
package workbookfake

import (
	"archive/zip"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Namespaces of the transitional and strict flavours of the format.
const (
	MainNS       = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	RelNS        = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	StrictMainNS = "http://purl.oclc.org/ooxml/spreadsheetml/main"
	StrictRelNS  = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	packageRelNS = "http://schemas.openxmlformats.org/package/2006/relationships"
)

// Styled is a number with a number format, as Excel keeps a date: Styled{46290, 14} is
// 25/09/2026 in built-in format 14, Styled{0.375, "hh:mm"} 09:00 in a format of the
// workbook's own.
type Styled struct {
	Value  float64
	Format any // an int built-in id, or a string format code
}

// Sheet is one tab: its name and rows of cells (string, int, float64, bool, Styled or nil).
type Sheet struct {
	Name       string
	Rows       [][]any
	Hidden     bool
	HiddenRows []int
}

// Options change how the workbook is laid out.
type Options struct {
	Strict   bool
	Date1904 bool
}

func escape(text string) string { return html.EscapeString(text) }

const declaration = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// Parts are the XML parts of a workbook of sheets, with shared strings. Blank cells (nil
// or "") are left out of the XML, as Excel leaves them out.
func Parts(sheets []Sheet, opts Options) map[string]string {
	main, rel := MainNS, RelNS
	if opts.Strict {
		main, rel = StrictMainNS, StrictRelNS
	}
	var shared []string
	var formats []any
	parts := map[string]string{}
	var entries, links []string
	for index, sheet := range sheets {
		number := index + 1
		state := ""
		if sheet.Hidden {
			state = ` state="hidden"`
		}
		entries = append(entries, fmt.Sprintf(`<sheet name="%s" sheetId="%d"%s r:id="rId%d"/>`, escape(sheet.Name), number, state, number))
		links = append(links, fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/worksheet" Target="worksheets/sheet%d.xml"/>`, number, rel, number))
		var body strings.Builder
		for rowIndex, row := range sheet.Rows {
			var cells strings.Builder
			for column, value := range row {
				ref := fmt.Sprintf("%c%d", 'A'+column, rowIndex+1)
				cells.WriteString(cell(ref, value, &shared, &formats))
			}
			hidden := ""
			if slices.Contains(sheet.HiddenRows, rowIndex) {
				hidden = ` hidden="1"`
			}
			fmt.Fprintf(&body, `<row r="%d"%s>%s</row>`, rowIndex+1, hidden, cells.String())
		}
		parts[fmt.Sprintf("xl/worksheets/sheet%d.xml", number)] = declaration +
			fmt.Sprintf(`<worksheet xmlns="%s"><sheetData>%s</sheetData></worksheet>`, main, body.String())
	}
	links = append(links, fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/sharedStrings" Target="sharedStrings.xml"/>`, len(sheets)+1, rel))
	var items strings.Builder
	for _, text := range shared {
		items.WriteString("<si><t>" + escape(text) + "</t></si>")
	}
	parts["xl/sharedStrings.xml"] = declaration + fmt.Sprintf(`<sst xmlns="%s" count="%d">%s</sst>`, main, len(shared), items.String())
	if len(formats) > 0 {
		links = append(links, fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/styles" Target="styles.xml"/>`, len(sheets)+2, rel))
		parts["xl/styles.xml"] = styles(main, formats)
	}
	properties := ""
	if opts.Date1904 {
		properties = `<workbookPr date1904="1"/>`
	}
	parts["xl/workbook.xml"] = declaration + fmt.Sprintf(`<workbook xmlns="%s" xmlns:r="%s">%s<sheets>%s</sheets></workbook>`,
		main, rel, properties, strings.Join(entries, ""))
	parts["xl/_rels/workbook.xml.rels"] = declaration + fmt.Sprintf(`<Relationships xmlns="%s">%s</Relationships>`, packageRelNS, strings.Join(links, ""))
	parts["_rels/.rels"] = declaration + fmt.Sprintf(`<Relationships xmlns="%s"><Relationship Id="rId1" Type="%s/officeDocument" Target="xl/workbook.xml"/></Relationships>`, packageRelNS, rel)
	parts["[Content_Types].xml"] = declaration + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`
	return parts
}

func cell(ref string, value any, shared *[]string, formats *[]any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case Styled:
		index := slices.Index(*formats, v.Format)
		if index < 0 {
			*formats = append(*formats, v.Format)
			index = len(*formats) - 1
		}
		return fmt.Sprintf(`<c r="%s" s="%d"><v>%v</v></c>`, ref, index+1, v.Value)
	case bool:
		flag := 0
		if v {
			flag = 1
		}
		return fmt.Sprintf(`<c r="%s" t="b"><v>%d</v></c>`, ref, flag)
	case int, float64:
		return fmt.Sprintf(`<c r="%s"><v>%v</v></c>`, ref, v)
	case string:
		if v == "" {
			return ""
		}
		index := slices.Index(*shared, v)
		if index < 0 {
			*shared = append(*shared, v)
			index = len(*shared) - 1
		}
		return fmt.Sprintf(`<c r="%s" t="s"><v>%d</v></c>`, ref, index)
	}
	panic(fmt.Sprintf("no cell for %T", value))
}

// styles is a styles part: a numFmt for each format code, and a cell format for each
// format, after General. The cellStyleXfs before them hold xf elements too, which a reader
// must not take for cell formats, so there is one here.
func styles(main string, formats []any) string {
	var custom []string
	for _, format := range formats {
		if code, ok := format.(string); ok {
			custom = append(custom, code)
		}
	}
	var codes, xfs strings.Builder
	for index, code := range custom {
		fmt.Fprintf(&codes, `<numFmt numFmtId="%d" formatCode="%s"/>`, 164+index, escape(code))
	}
	for _, format := range formats {
		id, ok := format.(int)
		if !ok {
			id = 164 + slices.Index(custom, format.(string))
		}
		fmt.Fprintf(&xfs, `<xf numFmtId="%d" applyNumberFormat="1"/>`, id)
	}
	return declaration + fmt.Sprintf(`<styleSheet xmlns="%s"><numFmts count="%d">%s</numFmts>`+
		`<cellStyleXfs count="1"><xf numFmtId="22"/></cellStyleXfs>`+
		`<cellXfs count="%d"><xf numFmtId="0"/>%s</cellXfs></styleSheet>`, main, len(custom), codes.String(), len(formats)+1, xfs.String())
}

// WriteZip writes parts into a zip at path, deflated as Office does.
func WriteZip(t testing.TB, path string, parts map[string]string) string {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(parts[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// Write writes a workbook of sheets to name in a temporary folder, and is its path.
func Write(t testing.TB, name string, sheets []Sheet, opts Options) string {
	t.Helper()
	return WriteZip(t, filepath.Join(t.TempDir(), name), Parts(sheets, opts))
}
