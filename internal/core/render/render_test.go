package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func console() (*Console, *bytes.Buffer, *bytes.Buffer) {
	var out, err bytes.Buffer
	return &Console{Out: &out, Err: &err}, &out, &err
}

func TestATableIsAlignedWithARuleAndDashesForBlanks(t *testing.T) {
	got := FormatTable([]string{"DEVICE", "STATE", "DETAIL"},
		[][]Cell{Cells("web01", "ok", "fine"), {Plain("web002"), Coloured("", "red"), Plain("gone")}}, 0, false)
	want := "DEVICE  STATE  DETAIL\n------  -----  ------\nweb01   ok     fine\nweb002  -      gone"
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestTheLastColumnIsCutToFitAWindow(t *testing.T) {
	detail := strings.Repeat("x", 60)
	got := FormatTable([]string{"A", "DETAIL"}, [][]Cell{Cells("a", detail)}, 40, false)
	last := strings.Split(got, "\n")[2]
	if len([]rune(last)) != 40 || !strings.HasSuffix(last, "…") {
		t.Fatalf("%q", last)
	}
}

func TestEveryShape(t *testing.T) {
	c, out, _ := console()
	rows := [][]Cell{Cells("web01", "a,b"), Cells("web02", "tab\there")}
	c.Emit(CSV, []string{"NAME", "NOTE"}, rows, nil)
	if out.String() != "NAME,NOTE\nweb01,\"a,b\"\nweb02,tab\there\n" {
		t.Fatalf("csv %q", out.String())
	}
	out.Reset()
	c.Emit(TSV, []string{"NAME", "NOTE"}, rows, nil)
	if out.String() != "web01\ta,b\nweb02\ttab here\n" {
		t.Fatalf("tsv %q", out.String())
	}
	out.Reset()
	c.Emit(JSON, nil, nil, []map[string]any{{"name": "web01", "tag": "<b>"}})
	if out.String() != "[\n  {\n    \"name\": \"web01\",\n    \"tag\": \"<b>\"\n  }\n]\n" {
		t.Fatalf("json %q", out.String())
	}
	out.Reset()
	c.Emit(JSON, nil, nil, nil)
	if out.String() != "[]\n" {
		t.Fatalf("empty json %q", out.String())
	}
}

func TestSortAndUniqueArrangeRows(t *testing.T) {
	c, out, errOut := console()
	c.Sort = []Order{{Column: "seen", Descending: true}}
	c.Unique = []string{"device"}
	rows := [][]Cell{Cells("web01", "2026-09-01"), Cells("web02", "2026-09-03"), Cells("WEB01", "2026-09-05")}
	if err := c.Emit(TSV, []string{"DEVICE", "SEEN"}, rows, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "WEB01\t2026-09-05\nweb02\t2026-09-03\n" {
		t.Fatalf("%q", out.String())
	}
	c.Emit(JSON, nil, nil, []int{1})
	if !strings.Contains(errOut.String(), "jq's sort_by") {
		t.Fatal("no warning that JSON is not arranged")
	}
	c.Sort = []Order{{Column: "owner"}}
	if err := c.Emit(Table, []string{"DEVICE"}, nil, nil); err == nil {
		t.Fatal("an unknown sort column passed")
	}
}

func TestNotesWarningsAndErrorsGoToStderr(t *testing.T) {
	c, out, errOut := console()
	c.Report = &Report{}
	c.Note("%d found", 3)
	c.Warn("partial")
	c.Error("it failed", "try again")
	if out.Len() != 0 {
		t.Fatal("stdout written")
	}
	if errOut.String() != "3 found\nwarning: partial\nerror: it failed\nhint: try again\n" {
		t.Fatalf("%q", errOut.String())
	}
	if len(c.Report.Notes) != 2 {
		t.Fatal("notes not gathered for a page")
	}
}

func TestTimesAndPairs(t *testing.T) {
	now := time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC)
	if got := When(now.Add(-3*time.Hour-2*time.Minute), now); !strings.HasSuffix(got, "(3h 02m ago)") {
		t.Fatal(got)
	}
	if got := When(now.Add(time.Hour), now); !strings.HasSuffix(got, "(in 1h 00m)") {
		t.Fatal(got)
	}
	if When(time.Time{}, now) != "-" || Moment(time.Time{}) != "-" || ISO(time.Time{}) != nil {
		t.Fatal("zero times")
	}
	yes := true
	if YesNo(&yes) != "yes" || YesNo(nil) != "-" {
		t.Fatal("yes no")
	}
	if Pairs([][2]string{{"name", "web01"}, {"os", ""}}, false) != "name  web01\nos    -" {
		t.Fatal(Pairs([][2]string{{"name", "web01"}, {"os", ""}}, false))
	}
}

func TestQueryResultsKeepTheirColumnOrder(t *testing.T) {
	c, out, errOut := console()
	result := FromColumns([]string{"b", "a"}, [][]any{{1.0, map[string]any{"x": true}}}, true)
	result.Warnings = []string{"partial"}
	c.Query(result, CSV)
	if out.String() != "b,a\n1,\"{\"\"x\"\":true}\"\n" {
		t.Fatalf("%q", out.String())
	}
	if !strings.Contains(errOut.String(), "stopped after 1 rows") || !strings.Contains(errOut.String(), "partial") {
		t.Fatal(errOut.String())
	}
	if got := FromRecords([]map[string]any{{"z": 1, "a": 2}, {"m": 3}}, false).Columns; strings.Join(got, ",") != "a,z,m" {
		t.Fatal(got)
	}
}
