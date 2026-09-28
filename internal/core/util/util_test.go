package util

import (
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func TestGUIDsAreCheckedAndLowered(t *testing.T) {
	got, err := RequireGUID(" 7C917DB0-71F2-438E-9554-388FFCAB8764 ", "a tenant id")
	if err != nil || got != "7c917db0-71f2-438e-9554-388ffcab8764" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := RequireGUID("7c917db0\n", "a tenant id"); !errs.Is(err, errs.Input) {
		t.Fatalf("a bad id passed: %v", err)
	}
}

func TestHostNamesAreSafeForAQuery(t *testing.T) {
	if got, err := RequireHost(" web01.corp.example. "); err != nil || got != "web01.corp.example" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"web01' or 1=1", "web 01", "web\n01", ""} {
		if _, err := RequireHost(bad); err == nil {
			t.Fatalf("%q passed", bad)
		}
	}
	if names := CandidateNames("web01.corp.example"); len(names) != 2 || names[1] != "web01" {
		t.Fatalf("candidates %v", names)
	}
	if names := CandidateNames("WEB01"); len(names) != 1 {
		t.Fatalf("candidates %v", names)
	}
}

func TestNamesSplitOnCommasAndSpacesWithoutRepeats(t *testing.T) {
	got := SplitNames([]string{"a,b", "c  d", "A", ""})
	if len(got) != 4 || got[0] != "a" || got[3] != "d" {
		t.Fatalf("got %v", got)
	}
}

func TestODataLiterals(t *testing.T) {
	if ODataString("O'Brien") != "'O''Brien'" {
		t.Fatal(ODataString("O'Brien"))
	}
	when := time.Date(2026, 9, 24, 11, 11, 12, 5, time.FixedZone("BST", 3600))
	if ODataDatetime(when) != "2026-09-24T10:11:12Z" {
		t.Fatal(ODataDatetime(when))
	}
}

func TestDatetimesFromAPIs(t *testing.T) {
	cases := map[string]string{
		"2026-09-24T10:11:12Z":         "2026-09-24T10:11:12Z",
		"2026-09-24T10:11:12.1234567Z": "2026-09-24T10:11:12.1234567Z",
		"2026-09-24T11:11:12+01:00":    "2026-09-24T10:11:12Z",
		"2026-09-24T10:11:12":          "2026-09-24T10:11:12Z",
		"2026-09-24":                   "2026-09-24T00:00:00Z",
	}
	for in, want := range cases {
		if got := ParseDatetime(in).Format(time.RFC3339Nano); got != want {
			t.Fatalf("%s: got %s, want %s", in, got, want)
		}
	}
	for _, unknown := range []any{"0001-01-01T00:00:00Z", "1601-01-01T00:00:00Z", "", "soon", 3.0, nil} {
		if !ParseDatetime(unknown).IsZero() {
			t.Fatalf("%v read as a time", unknown)
		}
	}
}

func TestDurations(t *testing.T) {
	for in, want := range map[string]time.Duration{"90": 90 * time.Second, "15m": 15 * time.Minute,
		"1h30m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "2 h": 2 * time.Hour} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Fatalf("%s: got %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"0", "0s", "7x", "h", "1h tomorrow"} {
		if _, err := ParseDuration(bad); !errs.Is(err, errs.Input) {
			t.Fatalf("%q passed", bad)
		}
	}
	if FormatSpan(7*24*time.Hour) != "7d" || FormatSpan(36*time.Hour) != "36h" || FormatSpan(90*time.Second) != "1m 30s" {
		t.Fatal(FormatSpan(36 * time.Hour))
	}
	for span, want := range map[time.Duration]string{45 * time.Second: "45s", 725 * time.Second: "12m 05s",
		3*time.Hour + 7*time.Minute: "3h 07m", 52 * time.Hour: "2d 04h", -45 * time.Second: "45s"} {
		if got := FormatDuration(span); got != want {
			t.Fatalf("%v: got %s", span, got)
		}
	}
}

func TestGrouped(t *testing.T) {
	for value, want := range map[float64]string{1234.5: "1,234.50", 999: "999.00", -1234567.891: "-1,234,567.89", 0.0004: "0.00"} {
		if got := Grouped(value, 2); got != want {
			t.Errorf("%v: %s", value, got)
		}
	}
	if Grouped(1234.5678, 3) != "1,234.568" || Grouped(12345, 0) != "12,345" {
		t.Error(Grouped(12345, 0))
	}
}

// The cases and their answers are Python's own repr, run on each.
func TestPythonReprQuotesAsPythonDoes(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"PT5M", "'PT5M'"},
		{"Malware", "'Malware'"},
		{"it's", "\"it's\""},
		{"say \"hi\"", "'say \"hi\"'"},
		{"both ' and \"", "'both \\' and \"'"},
		{"back\\slash", "'back\\\\slash'"},
		{"line\nbreak\ttab\rret", "'line\\nbreak\\ttab\\rret'"},
		{"nul\u0000del\u007fbell\u0007", "'nul\\x00del\\x7fbell\\x07'"},
		{"caf\u00e9", "'caf\u00e9'"},
		{"", "''"},
		{"x\nquery: evil", "'x\\nquery: evil'"},
	} {
		if got := PythonRepr(test.value); got != test.want {
			t.Errorf("%q: %s, want %s", test.value, got, test.want)
		}
	}
}
