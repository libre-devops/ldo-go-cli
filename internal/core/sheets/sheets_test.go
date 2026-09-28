package sheets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/workbookfake"
)

func cells(t *testing.T, path, name string) [][]string {
	t.Helper()
	book, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	sheet := book.Sheets[0]
	if name != "" {
		if sheet, err = book.Sheet(name); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := book.Rows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	var found [][]string
	for _, row := range rows {
		found = append(found, row.Cells)
	}
	return found
}

func withSheetXML(t *testing.T, sheetData, strings string) string {
	t.Helper()
	parts := workbookfake.Parts([]workbookfake.Sheet{{Name: "Plan"}}, workbookfake.Options{})
	parts["xl/worksheets/sheet1.xml"] = `<worksheet xmlns="` + workbookfake.MainNS + `"><sheetData>` + sheetData + `</sheetData></worksheet>`
	parts["xl/sharedStrings.xml"] = `<sst xmlns="` + workbookfake.MainNS + `">` + strings + `</sst>`
	return workbookfake.WriteZip(t, filepath.Join(t.TempDir(), "plan.xlsx"), parts)
}

func TestRowsComeBackAsTextWithSharedStringsResolved(t *testing.T) {
	path := workbookfake.Write(t, "plan.xlsx", []workbookfake.Sheet{{Name: "Plan", Rows: [][]any{
		{"Host", "Ring", "Critical"}, {"web01", 1, true}, {"db01", 2.5, false}}}}, workbookfake.Options{})
	want := [][]string{{"Host", "Ring", "Critical"}, {"web01", "1", "TRUE"}, {"db01", "2.5", "FALSE"}}
	if got := cells(t, path, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
}

func TestEveryKindOfCellValueIsReadAsExcelSavedIt(t *testing.T) {
	path := withSheetXML(t, `<row r="1">`+
		`<c r="A1" t="s"><v>0</v></c>`+
		`<c r="B1" t="inlineStr"><is><t>inline</t></is></c>`+
		`<c r="C1" t="str"><f>A1&amp;"x"</f><v>cached result</v></c>`+
		`<c r="D1" t="e"><v>#N/A</v></c>`+
		`<c r="E1" t="d"><v>2026-09-24T00:00:00</v></c>`+
		`<c r="F1" s="1"/>`+
		`</row>`, `<si><r><t>web</t></r><r><rPr><b/></rPr><t>01</t></r><rPh><t>x</t></rPh></si>`)
	want := [][]string{{"web01", "inline", "cached result", "", "2026-09-24T00:00:00", ""}}
	if got := cells(t, path, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
}

func TestCellReferencesPlaceValuesInTheirColumns(t *testing.T) {
	path := withSheetXML(t, `<row r="1"><c r="B1" t="inlineStr"><is><t>b</t></is></c><c r="AA1"><v>27</v></c></row>`+
		`<row r="2"><c><v>1</v></c><c><v>2</v></c></row>`, "")
	got := cells(t, path, "")
	if len(got[0]) != 27 || got[0][1] != "b" || got[0][26] != "27" || !reflect.DeepEqual(got[1], []string{"1", "2"}) {
		t.Errorf("%q", got)
	}
}

func TestDatesReadAsTheyShow(t *testing.T) {
	path := workbookfake.Write(t, "plan.xlsx", []workbookfake.Sheet{{Name: "Plan", Rows: [][]any{{
		workbookfake.Styled{Value: 46290, Format: 14},
		workbookfake.Styled{Value: 0.375, Format: "hh:mm"},
		workbookfake.Styled{Value: 46290.5, Format: "dd/mm/yyyy hh:mm"},
		workbookfake.Styled{Value: 1.5, Format: "[h]:mm"},
		workbookfake.Styled{Value: 46290, Format: `"Day "0`},
		workbookfake.Styled{Value: 60, Format: 14},
	}}}}, workbookfake.Options{})
	want := []string{"2026-09-25", "09:00:00", "2026-09-25T12:00:00", "1.5", "46290", "60"}
	if got := cells(t, path, ""); !reflect.DeepEqual(got[0], want) {
		t.Errorf("%q", got)
	}
	mac := workbookfake.Write(t, "mac.xlsx", []workbookfake.Sheet{{Name: "Plan", Rows: [][]any{{
		workbookfake.Styled{Value: 0, Format: 14}}}}}, workbookfake.Options{Date1904: true})
	if got := cells(t, mac, ""); got[0][0] != "1904-01-01" {
		t.Errorf("%q", got)
	}
}

func TestSerialsAndFormats(t *testing.T) {
	for serial, want := range map[float64]string{1: "1900-01-01", 59: "1900-02-28", 61: "1900-03-01", 46290: "2026-09-25"} {
		if got := FromSerial(serial, Date, false); got != want {
			t.Errorf("%v: %s", serial, got)
		}
	}
	if FromSerial(-1, Date, false) != "" || FromSerial(1e12, Date, false) != "" {
		t.Error("impossible days")
	}
	code := "mmm yyyy"
	if FormatKind(0, &code) != Date || FormatKind(22, nil) != DateTime || FormatKind(46, nil) != "" {
		t.Error("formats")
	}
	code = "[Red]0.00"
	if FormatKind(0, &code) != "" {
		t.Error("a colour is not a date")
	}
	code = "h:mm AM/PM"
	if FormatKind(0, &code) != Time {
		t.Error("am/pm is a time")
	}
}

func TestSheetsByNameAndHiddenOnes(t *testing.T) {
	path := workbookfake.Write(t, "plan.xlsx", []workbookfake.Sheet{
		{Name: "Notes", Rows: [][]any{{"x"}}, Hidden: true},
		{Name: "Plan", Rows: [][]any{{"Host"}, {"web01"}}, HiddenRows: []int{1}},
	}, workbookfake.Options{Strict: true})
	book, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	if len(book.Sheets) != 2 || !book.Sheets[0].Hidden || book.Sheets[1].Hidden {
		t.Errorf("%+v", book.Sheets)
	}
	sheet, err := book.Sheet(" plan ")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := book.Rows(sheet)
	if !rows[1].Hidden || rows[0].Hidden {
		t.Errorf("%+v", rows)
	}
	if _, err := book.Sheet("Other"); !errs.Is(err, errs.Input) || !strings.Contains(errs.HintOf(err), "Notes, Plan") {
		t.Errorf("%v", err)
	}
}

func TestWhatIsNotAWorkbookIsRefused(t *testing.T) {
	folder := t.TempDir()
	text := filepath.Join(folder, "plan.xlsx")
	_ = os.WriteFile(text, []byte("Host\nweb01\n"), 0o600)
	protected := filepath.Join(folder, "locked.xlsx")
	_ = os.WriteFile(protected, append([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, make([]byte, 100)...), 0o600)
	empty := workbookfake.WriteZip(t, filepath.Join(folder, "empty.xlsx"), map[string]string{"a.txt": "x"})
	for path, want := range map[string]string{text: "not an Excel workbook", protected: "protected with a password",
		empty: "not an Excel workbook", filepath.Join(folder, "missing.xlsx"): "cannot read"} {
		if _, err := Open(path); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
	if err := CheckReadable("plan.xls"); !errs.Is(err, errs.Input) || errs.HintOf(err) != SaveAsHint {
		t.Errorf("%v", err)
	}
	if !IsWorkbook("Plan.XLSM") || IsWorkbook("plan.csv") {
		t.Error("suffixes")
	}
}

func TestADTDIsRefused(t *testing.T) {
	path := withSheetXML(t, "", "")
	parts := workbookfake.Parts([]workbookfake.Sheet{{Name: "Plan"}}, workbookfake.Options{})
	parts["xl/sharedStrings.xml"] = `<?xml version="1.0"?><!DOCTYPE sst [<!ENTITY a "aaaa">]><sst xmlns="` + workbookfake.MainNS + `"/>`
	workbookfake.WriteZip(t, path, parts)
	if _, err := Open(path); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "declares a DTD") {
		t.Errorf("%v", err)
	}
}

func TestAPartNotInUTF8IsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.xlsx")
	parts := workbookfake.Parts([]workbookfake.Sheet{{Name: "Plan"}}, workbookfake.Options{})
	parts["xl/sharedStrings.xml"] = `<?xml version="1.0" encoding="UTF-16"?><sst/>`
	workbookfake.WriteZip(t, path, parts)
	if _, err := Open(path); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "not UTF-8") {
		t.Errorf("%v", err)
	}
}

func TestAPartTooLargeIsRefused(t *testing.T) {
	saved := MaxPartBytes
	MaxPartBytes = 200
	defer func() { MaxPartBytes = saved }()
	path := workbookfake.Write(t, "plan.xlsx", []workbookfake.Sheet{{Name: "Plan", Rows: [][]any{{strings.Repeat("x", 500)}}}}, workbookfake.Options{})
	if _, err := Open(path); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "too large") {
		t.Errorf("%v", err)
	}
}

func TestDamagedParts(t *testing.T) {
	missing := withSheetXML(t, `<row r="1"><c r="A1" t="s"><v>9</v></c></row>`, "")
	book, err := Open(missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Rows(book.Sheets[0]); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "missing shared string") {
		t.Errorf("%v", err)
	}
	book.Close()
	broken := withSheetXML(t, `<row r="1"><c>`, "")
	book, err = Open(broken)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	if _, err := book.Rows(book.Sheets[0]); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "is damaged") {
		t.Errorf("%v", err)
	}
}
