package analyzer

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// expected is what the Python ldo read from the same zips, in its dataclasses' shape.
type expected struct {
	File   string `json:"file"`
	Report struct {
		Source   string `json:"source"`
		Platform string `json:"platform"`
		Host     string `json:"host"`
		Facts    []struct {
			Section, Name, Value, Alert string
		} `json:"facts"`
		Findings []struct {
			ID, Severity, Category, Check, Result, Guidance string
		} `json:"findings"`
		AnalyzerVersion string `json:"analyzer_version"`
		RunAt           string `json:"run_at"`
	} `json:"report"`
}

func load(t *testing.T) map[string]expected {
	t.Helper()
	data, err := os.ReadFile("testdata/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var found map[string]expected
	if err := json.Unmarshal(data, &found); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestEveryZipReadsAsThePythonReadIt(t *testing.T) {
	for name, want := range load(t) {
		report, err := ReadReport(filepath.Join("testdata", want.File))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := json.Marshal(report)
		var gotReport, wantReport map[string]any
		_ = json.Unmarshal(got, &gotReport)
		wantJSON, _ := json.Marshal(want.Report)
		_ = json.Unmarshal(wantJSON, &wantReport)
		for key, value := range map[string]any{"Source": want.Report.Source, "Platform": want.Report.Platform,
			"Host": want.Report.Host, "AnalyzerVersion": want.Report.AnalyzerVersion, "RunAt": want.Report.RunAt} {
			if gotReport[key] != value {
				t.Errorf("%s %s: %v, want %v", name, key, gotReport[key], value)
			}
		}
		if len(report.Facts) != len(want.Report.Facts) || len(report.Findings) != len(want.Report.Findings) {
			t.Fatalf("%s: %d facts %d findings", name, len(report.Facts), len(report.Findings))
		}
		for index, fact := range want.Report.Facts {
			if !reflect.DeepEqual(report.Facts[index], Fact(fact)) {
				t.Errorf("%s fact %d: %+v, want %+v", name, index, report.Facts[index], fact)
			}
		}
		for index, finding := range want.Report.Findings {
			if !reflect.DeepEqual(report.Findings[index], Finding(finding)) {
				t.Errorf("%s finding %d: %+v, want %+v", name, index, report.Findings[index], finding)
			}
		}
	}
}

func TestAnUnpackedFolderOrTheXMLItselfReadsTheSame(t *testing.T) {
	folder := t.TempDir()
	archive, err := zip.OpenReader(filepath.Join("testdata", "MDEClientAnalyzerResult.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		handle, _ := file.Open()
		path := filepath.Join(folder, filepath.FromSlash(strings.ReplaceAll(file.Name, `\`, "/")))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		data := make([]byte, file.UncompressedSize64)
		_, _ = handle.Read(data)
		handle.Close()
		_ = os.WriteFile(path, data, 0o600)
	}
	fromFolder, err := ReadReport(folder)
	if err != nil || fromFolder.Host != "web01" || fromFolder.Count("error") != 1 {
		t.Errorf("%+v %v", fromFolder, err)
	}
	fromXML, err := ReadReport(filepath.Join(folder, "SystemInfoLogs", "MDEClientAnalyzer.xml"))
	if err != nil || fromXML.Host != "web01" || fromXML.Source != "MDEClientAnalyzer.xml" {
		t.Errorf("%+v %v", fromXML, err)
	}
	if _, err := ReadReport(t.TempDir()); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestWhatIsNotAResultIsRefused(t *testing.T) {
	for data, message := range map[string]string{
		"<other/>":                               "not an MDE Client Analyzer result",
		"<a><b></a>":                             "cannot be read as XML",
		`<!DOCTYPE x [<!ENTITY a "b">]><mdatp/>`: "declares a document type",
		"":                                       "no root element",
	} {
		if _, err := Parse([]byte(data), "x.xml", nil); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), message) {
			t.Errorf("%q: %v", data, err)
		}
	}
	report, err := Parse([]byte(`<mdatp><events><event id="331004"/></events></mdatp>`), "x", []byte(`<!DOCTYPE x><events/>`))
	if err != nil || report.Findings[0].Check != edrCyber {
		t.Errorf("%+v %v", report, err)
	}
}

func TestAZipWithoutAResultOrPastTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.zip")
	file, _ := os.Create(path)
	writer := zip.NewWriter(file)
	entry, _ := writer.Create("readme.txt")
	_, _ = entry.Write([]byte("nothing"))
	_ = writer.Close()
	_ = file.Close()
	if _, err := ReadReport(path); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "holds no MDE Client Analyzer result") {
		t.Errorf("%v", err)
	}
	saved := MaxXMLBytes
	MaxXMLBytes = 100
	defer func() { MaxXMLBytes = saved }()
	if _, err := ReadReport(filepath.Join("testdata", "MDEClientAnalyzerResult.zip")); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("%v", err)
	}
	if _, err := ReadReport(filepath.Join(t.TempDir(), "missing.zip")); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestPlainKeepsLinksAndDropsMarkup(t *testing.T) {
	got := Plain(`Update  it:<br/><a target='_blank' href="https://example.test/a">the <b>docs</b></a> &amp;amp; more<BR>`)
	if got != "Update it:\nthe docs (https://example.test/a) &amp; more" {
		t.Errorf("%q", got)
	}
}

func TestDescribeAnUnknownFinding(t *testing.T) {
	platform, severity, category, check, _ := Describe("339999")
	if platform != "linux" || severity != "informational" || category != "Other" || check != "check 339999" {
		t.Error(platform, severity, category, check)
	}
	if (Finding{Severity: "odd"}).Rank() != 3 {
		t.Error("rank")
	}
}
