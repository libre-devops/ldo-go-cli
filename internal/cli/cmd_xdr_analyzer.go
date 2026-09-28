package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/analyzer"
)

var analyzerColours = map[string]string{"error": "red", "warning": "yellow"}

var analyzerPlurals = map[string]string{"error": "errors", "warning": "warnings", "informational": "informational"}

func xdrAnalyzer(rt *Runtime) *cobra.Command {
	common := &Common{}
	var severity string
	var guidance, facts bool
	command := &cobra.Command{
		Use:   "analyzer RESULT...",
		Short: "What MDE Client Analyzer results found: each device's findings, errors first.",
		Long: "What MDE Client Analyzer results found: each device's findings, errors first.\n\n" +
			"Reads the zip the analyzer writes (MDEClientAnalyzerResult.zip on Windows, the support tool's zip on " +
			"Linux and macOS), the folder it unpacks to, or the results XML itself. Everything is read here: " +
			"nothing is sent anywhere. Exits 3 when a device has an error or a warning.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			worst, err := severityLimit(severity)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			var reports []analyzer.Report
			for _, path := range args {
				if _, err := os.Stat(path); err != nil {
					return Usagef("RESULT", "path %q does not exist", path)
				}
				report, err := analyzer.ReadReport(path)
				if err != nil {
					return err
				}
				reports = append(reports, report)
			}
			return showAnalyzer(rt, output, reports, worst, guidance, facts)
		},
	}
	command.Flags().StringVar(&severity, "severity", "", "Only findings at least this bad: error or warning.")
	command.Flags().BoolVar(&guidance, "guidance", false, "Add each finding's guidance: what to do.")
	command.Flags().BoolVar(&facts, "facts", false, "Each device's facts instead: OS, versions, services.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

// severityLimit is how many of the severities to keep: all of them unless --severity says.
func severityLimit(severity string) (int, error) {
	if severity == "" {
		return len(analyzer.Severities), nil
	}
	index := slices.Index(analyzer.Severities, strings.ToLower(strings.TrimSpace(severity)))
	if index < 0 {
		return 0, errs.Inputf("'%s' is not a severity", severity).WithHint("use error, warning or informational")
	}
	return index + 1, nil
}

func kept(report analyzer.Report, worst int) []analyzer.Finding {
	var found []analyzer.Finding
	for _, finding := range report.Findings {
		if finding.Rank() < worst {
			found = append(found, finding)
		}
	}
	return found
}

// oneLine is text on one line: a table row is one line; JSON keeps the analyzer's own
// line breaks.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func showAnalyzer(rt *Runtime, output render.Output, reports []analyzer.Report, worst int, guidance, facts bool) error {
	var headers []string
	var rows [][]render.Cell
	records := []any{}
	attention := false
	for _, report := range reports {
		records = append(records, analyzerRecord(report, worst))
		attention = attention || report.Count("error") > 0 || report.Count("warning") > 0
		if facts {
			for _, fact := range report.Facts {
				alert := render.Plain(fact.Alert)
				if strings.EqualFold(fact.Alert, "high") {
					alert = render.Coloured(fact.Alert, "red")
				}
				rows = append(rows, []render.Cell{render.Plain(report.Host), render.Plain(fact.Section), render.Plain(fact.Name),
					render.Plain(fact.Value), alert})
			}
			continue
		}
		for _, finding := range kept(report, worst) {
			row := []render.Cell{render.Plain(report.Host), render.Coloured(finding.Severity, analyzerColours[finding.Severity]),
				render.Plain(finding.Category), render.Plain(finding.Check), render.Plain(oneLine(finding.Result))}
			if guidance {
				row = append(row, render.Plain(oneLine(finding.Guidance)))
			}
			rows = append(rows, row)
		}
	}
	headers = []string{"HOST", "SEVERITY", "CATEGORY", "CHECK", "RESULT"}
	if guidance {
		headers = append(headers, "GUIDANCE")
	}
	if facts {
		headers = []string{"HOST", "SECTION", "FACT", "VALUE", "ALERT"}
	}
	if err := rt.Console.Emit(output, headers, rows, records); err != nil {
		return err
	}
	for _, report := range reports {
		rt.Console.Note("%s", analyzerSummary(report))
	}
	if attention {
		return Attention
	}
	return nil
}

func analyzerSummary(report analyzer.Report) string {
	var counts []string
	for _, level := range analyzer.Severities {
		count := report.Count(level)
		word := analyzerPlurals[level]
		if count == 1 {
			word = level
		}
		counts = append(counts, fmt.Sprintf("%d %s", count, word))
	}
	return fmt.Sprintf("%s (%s, %s): %s", or(report.Host, "unknown host"), report.Platform, report.Source, strings.Join(counts, ", "))
}

func analyzerRecord(report analyzer.Report, worst int) map[string]any {
	facts := []any{}
	for _, fact := range report.Facts {
		facts = append(facts, map[string]any{"section": fact.Section, "name": fact.Name, "value": fact.Value, "alert": orNil(fact.Alert)})
	}
	findings := []any{}
	for _, finding := range kept(report, worst) {
		findings = append(findings, map[string]any{"id": finding.ID, "severity": finding.Severity, "category": finding.Category,
			"check": finding.Check, "result": finding.Result, "guidance": orNil(finding.Guidance)})
	}
	return map[string]any{"source": report.Source, "platform": report.Platform, "host": orNil(report.Host),
		"analyzer_version": orNil(report.AnalyzerVersion), "run_at": orNil(report.RunAt), "errors": report.Count("error"),
		"warnings": report.Count("warning"), "informational": report.Count("informational"), "facts": facts, "findings": findings}
}
