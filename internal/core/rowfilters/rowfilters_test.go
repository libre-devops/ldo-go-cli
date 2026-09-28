package rowfilters

import (
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

var today = time.Date(2026, 9, 24, 15, 30, 0, 0, time.UTC)

var header = []string{"Host", "Scheduled Date", "Environment"}

func keep(t *testing.T, rows [][]string, where ...string) []string {
	t.Helper()
	conditions, err := Parse(where, today)
	if err != nil {
		t.Fatal(err)
	}
	test, err := RowTest(conditions, header, rows, "plan.csv")
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, row := range rows {
		if test(row) {
			kept = append(kept, row[0])
		}
	}
	return kept
}

func refused(t *testing.T, rows [][]string, where ...string) error {
	t.Helper()
	conditions, err := Parse(where, today)
	if err == nil {
		_, err = RowTest(conditions, header, rows, "plan.csv")
	}
	if !errs.Is(err, errs.Input) {
		t.Fatalf("%v: %v", where, err)
	}
	return err
}

var plan = [][]string{
	{"web01", "2026-09-24", "Dev"},
	{"web02", "2026-09-25 09:00", "Prod"},
	{"db01", "2026-09-23T10:00:00", "dev"},
	{"db02", "", "Test"},
}

func TestTextValuesMatchWithoutCase(t *testing.T) {
	if got := strings.Join(keep(t, plan, "Environment=DEV"), ","); got != "web01,db01" {
		t.Error(got)
	}
	if got := strings.Join(keep(t, plan, "environment!=dev"), ","); got != "web02,db02" {
		t.Error(got)
	}
	// Values for one column are alternatives; different columns must all hold.
	if got := strings.Join(keep(t, plan, "Environment=Dev", "Environment=Prod", "Host!=web02"), ","); got != "web01,db01" {
		t.Error(got)
	}
}

func TestDaysAndSpans(t *testing.T) {
	cases := map[string]string{
		"Scheduled Date=today":                  "web01",
		"Scheduled Date=tomorrow":               "web02",
		"Scheduled Date=yesterday":              "db01",
		"Scheduled Date=2026-09-23..2026-09-24": "web01,db01",
		"Scheduled Date=today..":                "web01,web02",
		"Scheduled Date=..today":                "web01,db01",
		"Scheduled Date=last 2d":                "web01,db01",
		"Scheduled Date=next 2d":                "web01,web02",
		"Scheduled Date=24/09/2026":             "web01",
	}
	for where, want := range cases {
		if got := strings.Join(keep(t, plan, where), ","); got != want {
			t.Errorf("%s: %s", where, got)
		}
	}
}

func TestSlashedDatesAreReadAsTheColumnSays(t *testing.T) {
	uk := [][]string{{"a", "25/09/2026", ""}, {"b", "01/02/2026", ""}}
	if got := strings.Join(keep(t, uk, "Scheduled Date=2026-02-01"), ","); got != "b" {
		t.Error(got)
	}
	us := [][]string{{"a", "09/25/2026", ""}, {"b", "01/02/2026", ""}}
	if got := strings.Join(keep(t, us, "Scheduled Date=01/02/2026"), ","); got != "b" {
		t.Error(got)
	}
	if got := strings.Join(keep(t, us, "Scheduled Date=2026-01-02"), ","); got != "b" {
		t.Error(got)
	}
}

func TestWhatCannotBeToldApartIsRefused(t *testing.T) {
	both := [][]string{{"a", "25/09/2026", ""}, {"b", "09/25/2026", ""}}
	says(t, refused(t, both, "Scheduled Date=today"), "both UK and US dates")
	unsure := [][]string{{"a", "01/02/2026", ""}}
	says(t, refused(t, unsure, "Scheduled Date=today"), "cannot tell whether")
	says(t, refused(t, plan, "Scheduled Date=01/02/2026"), "could be a UK or a US date")
	says(t, refused(t, plan, "Scheduled Date=2026-02-30"), "is not a date")
	says(t, refused(t, plan, "Scheduled Date=2026-09-25..2026-09-01"), "ends before it starts")
	says(t, refused(t, plan, "Scheduled Date=.."), "names no days")
	says(t, refused(t, plan, "Scheduled Date=last week"), "is not a span of days")
	says(t, refused(t, plan, "Scheduled Date=last 0d"), "is no days")
	says(t, refused(t, plan, "Scheduled Date=soon..today"), "is not a date")
	says(t, refused(t, plan, "=x"), "is not COLUMN=VALUE")
	says(t, refused(t, plan, "Owner=ana"), "no column 'Owner'")
}

func TestDaySpan(t *testing.T) {
	start, end, err := DaySpan("2026-09-01..", today)
	if err != nil || start.Format("2006-01-02") != "2026-09-01" || !end.IsZero() {
		t.Errorf("%v %v %v", start, end, err)
	}
	if _, _, err := DaySpan("01/02/2026", today); err == nil {
		t.Error("an unsure day was read")
	}
	if _, _, err := DaySpan("whenever", today); err == nil {
		t.Error("text is not a day")
	}
}

func TestExamples(t *testing.T) {
	if got := Examples(plan, header, "environment"); got != "Dev, Prod, dev, Test" {
		t.Error(got)
	}
	if got := Examples(nil, header, "Host"); got != "nothing" {
		t.Error(got)
	}
}

func says(t *testing.T, err error, want string) {
	t.Helper()
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%q lacks %q", err, want)
	}
}
