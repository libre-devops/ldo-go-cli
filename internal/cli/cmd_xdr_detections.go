package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/detections"
)

var ruleColours = map[string]string{"enabled": "green", "disabled": "yellow", "autodisabled": "red"}

var ruleSeverities = []string{"informational", "low", "medium", "high"}

var exporter = strings.Trim(brand.Suggest("xdr detections export"), "'")

const noIDHelp = "Leave the rule's id out, for a backup that creates the rules anew. By default it is kept, so " +
	"terraform import and later plans line up."

func detectionsClient(rt *Runtime, name string) (*detections.Client, string, error) {
	profile, err := rt.Profile(name)
	if err != nil {
		return nil, "", err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, "", err
	}
	client, err := detections.New(api)
	return client, profile.Name, err
}

// detectionsCommand is xdr detections: list, show, export.
func detectionsCommand(rt *Runtime) *cobra.Command {
	group := newGroup("detections", "Custom detection rules: list them, show one, export them as YAML for Terraform.")
	group.AddCommand(detectionsList(rt), detectionsShow(rt), detectionsExport(rt))
	return group
}

// chosen is the values given, folded (auto-disabled is autodisabled); a usage error for
// one that is not known.
func chosen(values, known []string, option string) (map[string]bool, error) {
	found := map[string]bool{}
	var unknown []string
	for _, value := range values {
		folded := strings.ReplaceAll(strings.ToLower(value), "-", "")
		found[folded] = true
		if !slices.Contains(known, folded) {
			unknown = append(unknown, folded)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, Usagef(option, "%s: use %s", strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return found, nil
}

func detectionsList(rt *Runtime) *cobra.Command {
	common := &Common{}
	var statuses, severities []string
	command := &cobra.Command{
		Use:   "list",
		Short: "The custom detection rules: status, schedule, severity, tactic and who changed them.",
		Long: "The custom detection rules: status, schedule, severity, tactic and who changed them.\n\n" +
			"Exits 3 when Defender has turned a rule off itself (autoDisabled), usually after its query failed again " +
			"and again: since 1 October 2026 the API no longer reports how each run went, so that is the sign left. " +
			"Needs CustomDetection.Read.All on the Graph token.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var known []string
			for _, status := range detections.Statuses {
				known = append(known, status[0])
			}
			wantedStatus, err := chosen(statuses, known, "--status")
			if err != nil {
				return err
			}
			wantedSeverity, err := chosen(severities, ruleSeverities, "--severity")
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, profile, err := detectionsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			rules, err := client.Rules(rt.Ctx())
			if err != nil {
				return err
			}
			var found []detections.Rule
			for _, rule := range rules {
				if (len(wantedStatus) == 0 || wantedStatus[strings.ToLower(rule.Status)]) &&
					(len(wantedSeverity) == 0 || wantedSeverity[strings.ToLower(rule.Severity)]) {
					found = append(found, rule)
				}
			}
			return showRules(rt, output, found, profile)
		},
	}
	command.Flags().StringArrayVar(&statuses, "status", nil, "Only rules enabled, disabled or autoDisabled. Repeatable.")
	command.Flags().StringArrayVar(&severities, "severity", nil, "Only informational, low, medium or high rules. Repeatable.")
	common.AddOutput(command, true)
	return command
}

func statusCell(rule detections.Rule) render.Cell {
	return render.Coloured(or(rule.Status, "-"), ruleColours[strings.ToLower(rule.Status)])
}

func showRules(rt *Runtime, output render.Output, found []detections.Rule, profile string) error {
	rows := make([][]render.Cell, len(found))
	records := []fields.Object{}
	off := 0
	for index, rule := range found {
		tactic := ""
		if len(rule.Tactics) > 0 {
			tactic = rule.Tactics[0]
		}
		rows[index] = []render.Cell{render.Plain(rule.DisplayName), statusCell(rule), render.Plain(rule.Schedule()),
			render.Plain(rule.Severity), render.Plain(tactic), render.Plain(rt.Console.When(rule.NextRun)),
			render.Plain(rt.Console.When(rule.Modified)), render.Plain(rule.ModifiedBy), render.Plain(rule.ID)}
		records = append(records, rule.Raw)
		if rule.AutoDisabled() {
			off++
		}
	}
	if err := rt.Console.Emit(output, []string{"RULE", "STATUS", "SCHEDULE", "SEVERITY", "TACTIC", "NEXT RUN", "CHANGED", "BY", "ID"},
		rows, records); err != nil {
		return err
	}
	rt.Console.Note("%s (profile %s)", ruleCounts(found), profile)
	if off > 0 {
		rt.Console.Warn("Defender turned %d rule(s) off itself, usually after their queries failed: see one with %s",
			off, brand.Suggest("xdr detections show NAME"))
		return Attention
	}
	return nil
}

func ruleCounts(rules []detections.Rule) string {
	var labels []string
	counts := map[string]int{}
	for _, rule := range rules {
		label := detections.StatusLabel(rule.Status)
		if label == "" {
			label = "unknown"
		}
		if counts[label] == 0 {
			labels = append(labels, label)
		}
		counts[label]++
	}
	text := fmt.Sprintf("%d rule(s)", len(rules))
	var parts []string
	for _, label := range labels {
		parts = append(parts, fmt.Sprintf("%d %s", counts[label], label))
	}
	if len(parts) > 0 {
		text += ": " + strings.Join(parts, ", ")
	}
	return text
}

func detectionsShow(rt *Runtime) *cobra.Command {
	common := &Common{}
	var asYAML, noID bool
	command := &cobra.Command{
		Use:   "show NAME_OR_ID",
		Short: "One custom detection rule: its settings and its query, or --yaml as a file for Terraform.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := detectionsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			raw, err := client.RawRule(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if asYAML {
				exported := detections.ExportRule(raw, !noID, rt.Clock(), exporter)
				rt.Console.Println(strings.TrimRight(exported.Text, "\n"))
				for _, note := range exported.Notes {
					rt.Console.Warn("review: %s", note)
				}
				return nil
			}
			found := detections.RuleFrom(raw)
			if output != render.Table {
				row := []render.Cell{render.Plain(found.DisplayName), statusCell(found), render.Plain(found.Schedule()),
					render.Plain(found.Severity), render.Plain(found.ID)}
				return rt.Console.Emit(output, []string{"RULE", "STATUS", "SCHEDULE", "SEVERITY", "ID"}, [][]render.Cell{row}, raw)
			}
			rt.Console.Println(pairs(rt, rulePairs(rt, found)))
			rt.Console.Println("")
			rt.Console.Println(title(rt, "Query"))
			rt.Console.Println(or(found.Query, "-"))
			return nil
		},
	}
	command.Flags().BoolVar(&asYAML, "yaml", false, "As the YAML file terraform-msgraph-xdr-custom-detection-rules reads, as the export writes it.")
	command.Flags().BoolVar(&noID, "no-id", false, noIDHelp)
	common.AddOutput(command, false)
	return command
}

func rulePairs(rt *Runtime, rule detections.Rule) [][2]string {
	return [][2]string{
		{"Rule", rule.DisplayName}, {"Id", rule.ID}, {"Status", detections.StatusLabel(rule.Status)},
		{"Schedule", rule.Schedule()}, {"Next run", rt.Console.When(rule.NextRun)}, {"Alert", rule.Title},
		{"Severity", rule.Severity}, {"Tactics", strings.Join(rule.Tactics, ", ")}, {"Techniques", strings.Join(rule.Techniques, ", ")},
		{"Description", rule.Description}, {"Created", rt.Console.When(rule.Created) + " by " + or(rule.CreatedBy, "-")},
		{"Changed", rt.Console.When(rule.Modified) + " by " + or(rule.ModifiedBy, "-")},
	}
}

func detectionsExport(rt *Runtime) *cobra.Command {
	common := &Common{}
	var names []string
	var noID, force bool
	command := &cobra.Command{
		Use:   "export FOLDER",
		Short: "Export custom detection rules as YAML, for terraform-msgraph-xdr-custom-detection-rules.",
		Long: "Export custom detection rules as YAML, for terraform-msgraph-xdr-custom-detection-rules.\n\n" +
			"One file per rule at TACTIC/RULE-NAME.yaml, in the layout and schema the module reads. The rule's id is " +
			"kept, so terraform import lines up (--no-id for a backup that creates the rules anew). What needs review " +
			"is a TODO(export) comment in the file. Files already there are kept unless --force. Only reads the tenant.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := detectionsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			var raw []fields.Object
			if len(names) > 0 {
				for _, name := range names {
					item, err := client.RawRule(rt.Ctx(), name)
					if err != nil {
						return err
					}
					raw = append(raw, item)
				}
			} else if raw, err = client.RawRules(rt.Ctx()); err != nil {
				return err
			}
			results, err := detections.WriteRules(detections.ExportRules(raw, !noID, rt.Clock(), exporter), args[0], force)
			if err != nil {
				return err
			}
			return showWritten(rt, output, results, args[0])
		},
	}
	command.Flags().StringArrayVar(&names, "name", nil, "Only this rule, by display name or id. Repeatable.")
	command.Flags().BoolVar(&noID, "no-id", false, noIDHelp)
	command.Flags().BoolVar(&force, "force", false, "Overwrite files that are there already.")
	common.AddOutput(command, true)
	return command
}

func showWritten(rt *Runtime, output render.Output, results []detections.Written, folder string) error {
	colours := map[string]string{"written": "green", "kept": "yellow", "refused": "red"}
	rows := make([][]render.Cell, len(results))
	records := []any{}
	counts := map[string]int{}
	review := 0
	for index, item := range results {
		notes := ""
		if len(item.Exported.Notes) > 0 {
			notes = fmt.Sprint(len(item.Exported.Notes))
		}
		rows[index] = []render.Cell{render.Plain(item.Exported.Rule.DisplayName), render.Plain(item.Target),
			render.Coloured(item.Result, colours[item.Result]), render.Plain(notes)}
		records = append(records, map[string]any{"rule": item.Exported.Rule.DisplayName, "id": item.Exported.Rule.ID,
			"file": item.Target, "result": item.Result, "to_review": nonNil(item.Exported.Notes)})
		counts[item.Result]++
		if item.Result == "written" && len(item.Exported.Notes) > 0 {
			review++
		}
	}
	if err := rt.Console.Emit(output, []string{"RULE", "FILE", "RESULT", "TO REVIEW"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("wrote %d of %d rule(s) to %s", counts["written"], len(results), folder)
	if review > 0 {
		rt.Console.Warn("%d file(s) have TODO(export) comments to review before committing", review)
	}
	if counts["kept"] > 0 {
		rt.Console.Warn("kept %d file(s) that were there already: --force overwrites them", counts["kept"])
	}
	if counts["refused"] > 0 {
		rt.Console.Warn("did not write %d file(s): a link was in the way", counts["refused"])
	}
	return nil
}
