package inputs

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/rowfilters"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/workbookfake"
)

func write(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func names(t *testing.T, values []string, opts Options) string {
	t.Helper()
	found, err := ReadNames(values, opts)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(found, ",")
}

func where(t *testing.T, texts ...string) []rowfilters.Condition {
	t.Helper()
	conditions, err := rowfilters.Parse(texts, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return conditions
}

func TestArgumentsStdinAndATextFile(t *testing.T) {
	file := write(t, "hosts.txt", "\xef\xbb\xbfweb03 # the new one\n\nWEB01, db01\n")
	got := names(t, []string{"web01,web02", "-"}, Options{Stdin: strings.NewReader("web02 db02\n"), File: file})
	if got != "web01,web02,db02,web03,db01" {
		t.Error(got)
	}
}

func TestACSVColumnAfterTitleRows(t *testing.T) {
	file := write(t, "plan.csv", "Patching plan,,\n\nHost,Owner\nweb01,ana\n\"web 02\",ben\n,\n")
	if got := names(t, nil, Options{File: file, Column: "host"}); got != "web01,web 02" {
		t.Error(got)
	}
	single := write(t, "one.csv", "FQDN\nweb01.corp.example\n")
	if got := names(t, nil, Options{File: single}); got != "web01.corp.example" {
		t.Error(got)
	}
	if got := names(t, []string{"-"}, Options{Stdin: strings.NewReader("Host,Env\nweb01,Dev\n"), Column: "Host"}); got != "web01" {
		t.Error(got)
	}
}

func TestCSVMistakes(t *testing.T) {
	several := write(t, "plan.csv", "Host,Owner\nweb01,ana\n")
	cases := map[string]Options{
		"has several columns":     {File: several},
		"has no column 'Machine'": {File: several, Column: "Machine"},
		"has no header row":       {File: write(t, "empty.csv", "\n\n"), Column: "Host"},
		"is not a text file":      {File: write(t, "bin.txt", "\xff\xfe\x00")},
		"which cannot be read":    {File: write(t, "old.xls", "x")},
		"workbook only":           {File: several, Sheet: "Plan"},
		"needs the column":        {File: several, Where: where(t, "Owner=ana")},
	}
	for want, opts := range cases {
		if _, err := ReadNames(nil, opts); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := ReadNames([]string{"web01"}, Options{Column: "Host", Where: where(t, "Owner=ana")}); err == nil ||
		!strings.Contains(err.Error(), "rows of a file") {
		t.Errorf("%v", err)
	}
	if _, err := ReadNames([]string{"-"}, Options{}); err == nil {
		t.Error("- read no stdin")
	}
}

func TestWhereKeepsTheRowsAndSaysWhatTheColumnHolds(t *testing.T) {
	file := write(t, "plan.csv", "Host,Date,Env\nweb01,2026-09-24,Dev\nweb02,2026-09-25,Prod\n")
	if got := names(t, nil, Options{File: file, Column: "Host", Where: where(t, "Date=today")}); got != "web01" {
		t.Error(got)
	}
	_, err := ReadNames(nil, Options{File: file, Column: "Host", Where: where(t, "Env=Test")})
	if !errs.Is(err, errs.Input) || errs.HintOf(err) != "Env holds: Dev, Prod" {
		t.Errorf("%v (%s)", err, errs.HintOf(err))
	}
}

func TestAWorkbookColumnFromTheOneSheetThatHasIt(t *testing.T) {
	path := workbookfake.Write(t, "plan.xlsx", []workbookfake.Sheet{
		{Name: "Notes", Rows: [][]any{{"Notes"}, {"see Plan"}}},
		{Name: "Hidden", Rows: [][]any{{"FQDN"}, {"old01"}}, Hidden: true},
		{Name: "Plan", Rows: [][]any{{"Patch plan"}, {"FQDN", "Date"}, {"web01", workbookfake.Styled{Value: 46289, Format: 14}},
			{"web02", workbookfake.Styled{Value: 46290, Format: 14}}}, HiddenRows: []int{3}},
	}, workbookfake.Options{})
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)
	if got := names(t, nil, Options{File: path, Column: "fqdn"}); got != "web01,web02" {
		t.Error(got)
	}
	if !strings.Contains(logged.String(), "1 name(s) in sheet 'Plan'") {
		t.Errorf("no warning: %s", logged.String())
	}
	if got := names(t, nil, Options{File: path, Column: "FQDN", Where: where(t, "Date=today")}); got != "web01" {
		t.Error(got)
	}
	if got := names(t, nil, Options{File: path, Column: "FQDN", Sheet: "hidden"}); got != "old01" {
		t.Error(got)
	}
	if got := names(t, nil, Options{File: path}); got != "see Plan" {
		t.Error(got)
	}
}

func TestSheetChoicesThatCannotBeMade(t *testing.T) {
	two := workbookfake.Write(t, "two.xlsx", []workbookfake.Sheet{
		{Name: "A", Rows: [][]any{{"Host"}, {"x"}}}, {Name: "B", Rows: [][]any{{"Host"}, {"y"}}}}, workbookfake.Options{})
	hidden := workbookfake.Write(t, "hidden.xlsx", []workbookfake.Sheet{{Name: "A", Rows: [][]any{{"Host"}}, Hidden: true}}, workbookfake.Options{})
	cases := map[string]Options{
		"several sheets":   {File: two, Column: "Host"},
		"no sheet of":      {File: two, Column: "FQDN"},
		"only hidden":      {File: hidden},
		"has no sheet 'C'": {File: two, Sheet: "C"},
	}
	for want, opts := range cases {
		if _, err := ReadNames(nil, opts); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}
