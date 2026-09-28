// Package analyzer reads an MDE Client Analyzer result: the zip it writes, its unpacked
// folder, or the XML.
//
// Windows' analyzer writes SystemInfoLogs\MDEClientAnalyzer.xml (root MDEResults),
// Linux's and macOS's mde.xml (root mdatp), beside everything else they collect. Only
// that one file is read, in memory, however large the zip: nothing is unpacked, so no
// name inside it can reach the disk, and a file past MaxXMLBytes is refused rather than
// read. XML that declares a document type is refused too: the analyzers never write one,
// and it is where entity expansion attacks start.
package analyzer

import (
	"slices"
	"strings"
)

// Severities are worst first: the order findings are shown in, and --severity compares by.
var Severities = []string{"error", "warning", "informational"}

// Fact is one thing the analyzer recorded about the device: its OS, a version, a
// service's state. Alert is the analyzer's own judgement of it, when it gave one.
type Fact struct {
	Section string
	Name    string
	Value   string
	Alert   string
}

// Finding is one check's result: its id, severity (one of Severities), category, the
// check, what it found, and what to do about it (plain text, links kept).
type Finding struct {
	ID       string
	Severity string
	Category string
	Check    string
	Result   string
	Guidance string
}

// Rank is where the severity comes in Severities: 0 is the worst.
func (f Finding) Rank() int {
	if index := slices.Index(Severities, f.Severity); index >= 0 {
		return index
	}
	return len(Severities)
}

// Report is one analyzer result: where it came from, the platform, the device, and what
// it found.
type Report struct {
	Source          string
	Platform        string
	Host            string
	Facts           []Fact
	Findings        []Finding
	AnalyzerVersion string
	RunAt           string
}

// Fact is the value of the first fact called name (ignoring case), or "".
func (r Report) Fact(name string) string {
	for _, fact := range r.Facts {
		if strings.EqualFold(fact.Name, name) {
			return fact.Value
		}
	}
	return ""
}

// Count is how many findings have severity.
func (r Report) Count(severity string) int {
	count := 0
	for _, finding := range r.Findings {
		if finding.Severity == severity {
			count++
		}
	}
	return count
}
