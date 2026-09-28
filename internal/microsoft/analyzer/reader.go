package analyzer

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// MaxXMLBytes is the largest results XML read.
var MaxXMLBytes = 16 * 1024 * 1024

const (
	windowsResult = "systeminfologs/mdeclientanalyzer.xml"
	unixResult    = "mde.xml"
	unixCatalogue = "events.xml"
)

// windowsSections are Windows' sections of device facts, as the analyzer names them, and
// as they are shown.
var windowsSections = map[string]string{"general": "General", "devInfo": "Device", "EDRCompInfo": "EDR",
	"MDEDevConfig": "Configuration", "AVCompInfo": "Antivirus"}

var (
	link      = regexp.MustCompile(`(?is)<a\b[^>]*?href=['"]([^'"]+)['"][^>]*>(.*?)</a>`)
	lineBreak = regexp.MustCompile(`(?i)<br\s*/?>`)
	tag       = regexp.MustCompile(`<[^>]+>`)
)

// ReadReport is the analyzer result at path: a zip, the folder it unpacks to, or its XML.
func ReadReport(path string) (Report, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Report{}, errs.Inputf("cannot read %s: %v", path, err)
	}
	if info.IsDir() {
		return fromFolder(path)
	}
	if archive, err := zip.OpenReader(path); err == nil {
		defer archive.Close()
		return fromZip(&archive.Reader, filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, errs.Inputf("cannot read %s: %v", path, err)
	}
	if err := capped(data, filepath.Base(path)); err != nil {
		return Report{}, err
	}
	return Parse(data, filepath.Base(path), nil)
}

// Parse is a report from the results XML itself; catalogue is Linux's events.xml, when
// the result carries one, for the checks' own names and guidance.
func Parse(data []byte, source string, catalogue []byte) (Report, error) {
	root, err := parseXML(data, source)
	if err != nil {
		return Report{}, err
	}
	switch root.name {
	case "MDEResults":
		return windows(root, source), nil
	case "mdatp":
		return unix(root, source, readCatalogue(catalogue, source)), nil
	}
	return Report{}, errs.Inputf("%s: not an MDE Client Analyzer result (its XML is <%s>)", source, root.name).
		WithHint("give the result zip, its folder, or MDEClientAnalyzer.xml or mde.xml")
}

// Plain is the analyzer's HTML snippets as text: a link as "words (url)", markup dropped.
func Plain(text string) string {
	text = link.ReplaceAllStringFunc(text, func(found string) string {
		parts := link.FindStringSubmatch(found)
		return strings.TrimSpace(tag.ReplaceAllString(parts[2], "")) + " (" + parts[1] + ")"
	})
	text = tag.ReplaceAllString(lineBreak.ReplaceAllString(text, "\n"), "")
	var lines []string
	for _, line := range strings.Split(html.UnescapeString(text), "\n") {
		if joined := strings.Join(strings.Fields(line), " "); joined != "" {
			lines = append(lines, joined)
		}
	}
	return strings.Join(lines, "\n")
}

// Finding the results --------------------------------------------------------------------

func fromZip(archive *zip.Reader, source string) (Report, error) {
	windowsFile := member(archive, windowsResult)
	if windowsFile != nil {
		data, err := readMember(windowsFile, source)
		if err != nil {
			return Report{}, err
		}
		return Parse(data, source, nil)
	}
	unixFile := member(archive, unixResult)
	if unixFile == nil {
		return Report{}, errs.Inputf("%s holds no MDE Client Analyzer result", source).
			WithHint(`it should hold SystemInfoLogs\MDEClientAnalyzer.xml (Windows) or mde.xml (Linux, macOS)`)
	}
	var events []byte
	if catalogue := member(archive, unixCatalogue); catalogue != nil {
		found, err := readMember(catalogue, source)
		if err != nil {
			return Report{}, err
		}
		events = found
	}
	data, err := readMember(unixFile, source)
	if err != nil {
		return Report{}, err
	}
	return Parse(data, source, events)
}

// member is the zip's file called wanted, in any folder; Windows' zips name their files
// with backslashes.
func member(archive *zip.Reader, wanted string) *zip.File {
	for _, file := range archive.File {
		name := strings.ToLower(strings.ReplaceAll(file.Name, `\`, "/"))
		if name == wanted || strings.HasSuffix(name, "/"+wanted) {
			return file
		}
	}
	return nil
}

// readMember reads one byte past the cap rather than trust the size the zip's header
// gives.
func readMember(file *zip.File, source string) ([]byte, error) {
	handle, err := file.Open()
	if err != nil {
		return nil, errs.Inputf("%s: cannot read %s: %v", source, file.Name, err)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, int64(MaxXMLBytes)+1))
	if err != nil {
		return nil, errs.Inputf("%s: cannot read %s: %v", source, file.Name, err)
	}
	return data, capped(data, source)
}

func fromFolder(folder string) (Report, error) {
	for _, pattern := range []string{"MDEClientAnalyzer.xml", unixResult} {
		found := find(folder, pattern)
		if len(found) == 0 {
			continue
		}
		var events []byte
		if pattern == unixResult {
			if catalogue := find(folder, unixCatalogue); len(catalogue) > 0 {
				data, err := readCapped(catalogue[0], folder)
				if err != nil {
					return Report{}, err
				}
				events = data
			}
		}
		data, err := readCapped(found[0], folder)
		if err != nil {
			return Report{}, err
		}
		return Parse(data, filepath.Base(folder), events)
	}
	return Report{}, errs.Inputf("%s holds no MDE Client Analyzer result", folder)
}

// find is every file under folder called name, in order.
func find(folder, name string) []string {
	var found []string
	_ = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == name {
			found = append(found, path)
		}
		return nil
	})
	sort.Strings(found)
	return found
}

func readCapped(path, source string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Inputf("cannot read %s: %v", path, err)
	}
	return data, capped(data, source)
}

func capped(data []byte, source string) error {
	if len(data) > MaxXMLBytes {
		return errs.Inputf("%s: the result is larger than %d MB", source, MaxXMLBytes/(1024*1024))
	}
	return nil
}

func declaresAType(data []byte) bool {
	return bytes.Contains(data, []byte("<!DOCTYPE")) || bytes.Contains(data, []byte("<!ENTITY"))
}

// element is a parsed XML element: its name, attributes, text and children.
type element struct {
	name     string
	attrs    map[string]string
	text     string
	children []*element
}

func (e *element) child(name string) *element {
	if e == nil {
		return nil
	}
	for _, child := range e.children {
		if child.name == name {
			return child
		}
	}
	return nil
}

// childText is the text of the first child called name, trimmed, or "".
func (e *element) childText(name string) string {
	if found := e.child(name); found != nil {
		return strings.TrimSpace(found.text)
	}
	return ""
}

// under is every element at path (events/event) below this one.
func (e *element) under(path ...string) []*element {
	current := []*element{e}
	for _, name := range path {
		var next []*element
		for _, parent := range current {
			for _, child := range parent.children {
				if child.name == name {
					next = append(next, child)
				}
			}
		}
		current = next
	}
	return current
}

// parseXML is data parsed, refused when it declares a document type.
func parseXML(data []byte, source string) (*element, error) {
	if declaresAType(data) {
		return nil, errs.Inputf("%s: refusing XML that declares a document type", source)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []*element
	var root *element
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errs.Inputf("%s: cannot be read as XML (%v)", source, err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			found := &element{name: value.Name.Local, attrs: map[string]string{}}
			for _, attr := range value.Attr {
				found.attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, found)
			} else if root == nil {
				root = found
			}
			stack = append(stack, found)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			// Text before the first child, as ElementTree's text is.
			if len(stack) > 0 && len(stack[len(stack)-1].children) == 0 {
				stack[len(stack)-1].text += string(value)
			}
		}
	}
	if root == nil {
		return nil, errs.Inputf("%s: cannot be read as XML (no root element)", source)
	}
	return root, nil
}

// Windows ---------------------------------------------------------------------------------

func windows(root *element, source string) Report {
	var facts []Fact
	for _, section := range root.children {
		if section.name != "events" {
			facts = append(facts, windowsFacts(section)...)
		}
	}
	var findings []Finding
	for _, event := range root.under("events", "event") {
		severity := strings.ToLower(event.childText("severity"))
		if severity == "" {
			severity = "informational"
		}
		findings = append(findings, Finding{ID: event.attrs["id"], Severity: severity, Category: event.childText("category"),
			Check: event.childText("check"), Result: Plain(event.childText("checkresult")), Guidance: Plain(event.childText("guidance"))})
	}
	return identified(Report{Source: source, Platform: "windows", Facts: facts, Findings: worstFirst(findings)}, "Device Host Name", "Script Version")
}

func windowsFacts(section *element) []Fact {
	label, ok := windowsSections[section.name]
	if !ok {
		label = section.name
	}
	var facts []Fact
	for _, item := range section.children {
		name := item.attrs["displayName"]
		if name == "" {
			name = item.name
		}
		name = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(name), ":"))
		facts = append(facts, Fact{Section: label, Name: name, Value: item.childText("value"), Alert: item.childText("alert")})
	}
	return facts
}

// Linux and macOS -------------------------------------------------------------------------

func unix(root *element, source string, catalogue map[string][2]string) Report {
	facts := append(unixFacts(root.child("general"), "General"), unixFacts(root.child("device_info"), "Device")...)
	platform := "linux"
	for _, fact := range facts {
		if fact.Name == "OS Family" && fact.Value == "Darwin" {
			platform = "macos"
		}
	}
	var findings []Finding
	for _, event := range root.under("events", "event") {
		findings = append(findings, unixFinding(event.attrs["id"], platform, catalogue))
	}
	return identified(Report{Source: source, Platform: platform, Facts: facts, Findings: worstFirst(findings)}, "Host Name", "Script Version")
}

func unixFacts(section *element, label string) []Fact {
	if section == nil {
		return nil
	}
	var facts []Fact
	for _, item := range section.children {
		name := item.attrs["display_name"]
		if name == "" {
			words := strings.ReplaceAll(item.name, "_", " ")
			name = strings.ToUpper(words[:1]) + strings.ToLower(words[1:])
		}
		facts = append(facts, Fact{Section: label, Name: name, Value: strings.TrimSpace(item.text)})
	}
	return facts
}

func unixFinding(id, platform string, catalogue map[string][2]string) Finding {
	_, severity, category, check, result := Describe(id)
	named := catalogue[id]
	guidance := named[1]
	if guidance == "" && severity != "informational" {
		guidance = Guidance[[2]string{platform, category}]
	}
	if named[0] != "" {
		check = named[0]
	}
	return Finding{ID: id, Severity: severity, Category: category, Check: check, Result: result, Guidance: guidance}
}

// readCatalogue is Linux's own events.xml: each id's check name and guidance.
func readCatalogue(data []byte, source string) map[string][2]string {
	entries := map[string][2]string{}
	if len(data) == 0 || declaresAType(data) {
		return entries
	}
	root, err := parseXML(data, source)
	if err != nil {
		return entries
	}
	for _, event := range root.under("event") {
		entries[event.attrs["id"]] = [2]string{event.childText("check_name"), Plain(event.childText("tsg"))}
	}
	return entries
}

// Shared ----------------------------------------------------------------------------------

// worstFirst is errors, then warnings, then the rest, each in the order the analyzer
// wrote them.
func worstFirst(findings []Finding) []Finding {
	sort.SliceStable(findings, func(a, b int) bool { return findings[a].Rank() < findings[b].Rank() })
	return findings
}

func identified(report Report, host, version string) Report {
	report.RunAt = report.Fact("Script RunTime")
	if report.RunAt == "" {
		report.RunAt = report.Fact("Script run time")
	}
	report.Host = report.Fact(host)
	if report.Host == "" {
		report.Host = report.Fact("Device Name")
	}
	report.AnalyzerVersion = report.Fact(version)
	return report
}
