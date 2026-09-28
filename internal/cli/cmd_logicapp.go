package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/jsontext"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/logicapps"
)

// The offline commands (check, params, references, connections, order, diff, defaults,
// rewrite) read files and never touch the network; they take files or folders, and a
// folder means every .json and .json.tftpl in it. export and validate go to Azure, and
// only read: validate asks the provider for its verdict and creates nothing.

var findingSeverityColours = map[string]string{"error": "red", "warning": "yellow"}

// logicappCommand is the logicapp group.
func logicappCommand(rt *Runtime) *cobra.Command {
	group := newGroup("logicapp", "Consumption Logic Apps: check, audit, compare, export and validate workflows.")
	group.AddCommand(logicappCheck(rt), logicappParams(rt), logicappReferences(rt), logicappConnections(rt), logicappOrder(rt),
		logicappDiff(rt), logicappDefaults(rt), logicappRewrite(rt), logicappExport(rt), logicappValidate(rt))
	return group
}

const filesUse = " FILES..."

// documents is every definition named, folders expanded to their .json and .json.tftpl
// files.
func documents(paths []string) ([]logicapps.Document, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			files = append(files, path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, errs.Inputf("cannot read %s: %v", path, err)
		}
		var found []string
		for _, entry := range entries {
			if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".json.tftpl")) {
				found = append(found, filepath.Join(path, entry.Name()))
			}
		}
		sort.Strings(found)
		files = append(files, found...)
	}
	if len(files) == 0 {
		return nil, errs.Inputf("no definition files found").WithHint("pass .json or .json.tftpl files")
	}
	var loaded []logicapps.Document
	for _, file := range files {
		document, err := logicapps.Load(file)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, document)
	}
	return loaded, nil
}

func workflowOf(workflow, source string) string { return or(workflow, source) }

func logicappCheck(rt *Runtime) *cobra.Command {
	common := &Common{}
	var supplied, connections []string
	var callback string
	var strict bool
	command := &cobra.Command{
		Use:   "check" + filesUse,
		Short: "Check definitions offline against what Azure rejects, or what fails once it runs.",
		Long: "Check definitions offline against what Azure rejects, or what fails once it runs.\n\n" +
			"FILES are definition files (.json, .json.tftpl), or folders of them. Exits 3 when any definition has an error " +
			"(or, with --strict, a warning), so a build step can gate on it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			loaded, err := documents(args)
			if err != nil {
				return err
			}
			var findings []logicapps.Finding
			for _, document := range loaded {
				findings = append(findings, logicapps.Check(document, logicapps.CheckOptions{Supplied: supplied, Connections: connections,
					CallbackTrigger: callback})...)
			}
			rows := make([][]render.Cell, len(findings))
			records := []any{}
			errors := 0
			for index, finding := range findings {
				rows[index] = []render.Cell{render.Plain(workflowOf(finding.Workflow, finding.Source)),
					render.Coloured(finding.Severity, findingSeverityColours[finding.Severity]), render.Plain(finding.Rule), render.Plain(finding.Message)}
				records = append(records, map[string]any{"workflow": orNil(finding.Workflow), "severity": finding.Severity, "rule": finding.Rule,
					"message": finding.Message, "source": finding.Source})
				if finding.Severity == "error" {
					errors++
				}
			}
			if err := rt.Console.Emit(output, []string{"WORKFLOW", "SEVERITY", "RULE", "MESSAGE"}, rows, records); err != nil {
				return err
			}
			warnings := len(findings) - errors
			rt.Console.Note("%d definition(s): %d error(s), %d warning(s)", len(loaded), errors, warnings)
			if errors > 0 || (strict && warnings > 0) {
				return Attention
			}
			return nil
		},
	}
	command.Flags().StringArrayVar(&supplied, "supplied", nil, "A parameter the deployment tool supplies (a Terraform parameters input). Repeatable.")
	command.Flags().StringArrayVar(&connections, "connection", nil, "A connection key the deployment wires. Repeatable.")
	command.Flags().StringVar(&callback, "callback-trigger", "", "A trigger that must exist, to be called.")
	command.Flags().BoolVar(&strict, "strict", false, "Warnings fail the check too.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func logicappParams(rt *Runtime) *cobra.Command {
	common := &Common{}
	var supplied []string
	var unsatisfied bool
	command := &cobra.Command{
		Use:   "params" + filesUse,
		Short: "Whether each declared parameter will have a value at deploy time, and from where.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			loaded, err := documents(args)
			if err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			for _, document := range loaded {
				for _, status := range logicapps.ParameterStatuses(document, supplied) {
					if unsatisfied && status.Satisfied {
						continue
					}
					satisfied := render.Coloured("no", "red")
					if status.Satisfied {
						satisfied = render.Coloured("yes", "green")
					}
					rows = append(rows, []render.Cell{render.Plain(workflowOf(status.Workflow, status.Source)), render.Plain(status.Name),
						render.Plain(status.Type), satisfied, render.Plain(status.SatisfiedBy), render.Plain(status.Reason)})
					records = append(records, map[string]any{"workflow": orNil(status.Workflow), "name": status.Name, "type": orNil(status.Type),
						"secure": status.Secure, "satisfied": status.Satisfied, "satisfied_by": status.SatisfiedBy, "reason": status.Reason,
						"source": status.Source})
				}
			}
			return rt.Console.Emit(output, []string{"WORKFLOW", "PARAMETER", "TYPE", "SATISFIED", "BY", "REASON"}, rows, records)
		},
	}
	command.Flags().StringArrayVar(&supplied, "supplied", nil, "A parameter the deployment tool supplies (a Terraform parameters input). Repeatable.")
	command.Flags().BoolVar(&unsatisfied, "unsatisfied", false, "Only the parameters that will fail.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func logicappReferences(rt *Runtime) *cobra.Command {
	common := &Common{}
	var unwired bool
	command := &cobra.Command{
		Use:   "references" + filesUse,
		Short: "The $connections keys each definition uses, where, and whether each is wired.",
		Long: "The $connections keys each definition uses, where, and whether each is wired.\n\n" +
			"A key the definition uses but nobody wired saves and deploys, then fails when the workflow runs. Exits 3 when " +
			"any key is unwired. A bare definition carries no values, so its keys show as unknown.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			loaded, err := documents(args)
			if err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			missing := false
			for _, document := range loaded {
				for _, found := range logicapps.ConnectionReferences(document) {
					if unwired && (found.Wired == nil || *found.Wired) {
						continue
					}
					wired := render.Plain("?")
					var state any
					if found.Wired != nil {
						state = *found.Wired
						wired = render.Coloured("no", "red")
						if *found.Wired {
							wired = render.Coloured("yes", "green")
						} else {
							missing = true
						}
					}
					rows = append(rows, []render.Cell{render.Plain(workflowOf(found.Workflow, found.Source)), render.Plain(found.Key),
						render.Plain(strings.Join(found.UsedBy, ", ")), wired})
					records = append(records, map[string]any{"workflow": orNil(found.Workflow), "key": found.Key, "used_by": found.UsedBy,
						"wired": state, "source": found.Source})
				}
			}
			if err := rt.Console.Emit(output, []string{"WORKFLOW", "KEY", "USED BY", "WIRED"}, rows, records); err != nil {
				return err
			}
			if missing {
				return Attention
			}
			return nil
		},
	}
	command.Flags().BoolVar(&unwired, "unwired", false, "Only keys the wrapper does not wire.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func logicappConnections(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "connections" + filesUse,
		Short: "The managed API connections each export resolves, and whether they use managed identity.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			loaded, err := documents(args)
			if err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			for _, document := range loaded {
				for _, found := range logicapps.ConnectionsOf(document) {
					identity := found.ManagedIdentity()
					rows = append(rows, render.Cells(workflowOf(found.Workflow, found.Source), found.Key, found.ConnectionName, found.Authentication,
						render.YesNo(&identity), found.ConnectionID))
					records = append(records, map[string]any{"workflow": orNil(found.Workflow), "key": found.Key, "connection_name": found.ConnectionName,
						"connection_id": orNil(found.ConnectionID), "managed_api_id": orNil(found.ManagedAPIID),
						"authentication": orNil(found.Authentication), "source": found.Source, "managed_identity": identity})
				}
			}
			return rt.Console.Emit(output, []string{"WORKFLOW", "KEY", "CONNECTION", "AUTHENTICATION", "MANAGED IDENTITY", "CONNECTION ID"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func logicappOrder(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "order" + filesUse,
		Short: "The order a set of workflows must deploy in: tier 0 first.",
		Long: "The order a set of workflows must deploy in: tier 0 first.\n\n" +
			"A workflow that dispatches to a sibling (a native Workflow action) deploys after it, since Azure checks the " +
			"target when the caller is written.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			loaded, err := documents(args)
			if err != nil {
				return err
			}
			steps, err := logicapps.DeployOrder(loaded)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(steps))
			records := []any{}
			for index, step := range steps {
				rows[index] = render.Cells(strconv.Itoa(step.Tier), step.Workflow, strings.Join(step.DependsOn, ", "))
				records = append(records, map[string]any{"workflow": step.Workflow, "tier": step.Tier, "depends_on": step.DependsOn, "source": step.Source})
			}
			return rt.Console.Emit(output, []string{"TIER", "WORKFLOW", "DEPENDS ON"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

// short is a value as one cell: its JSON, cut to 60 characters.
func short(value any) string {
	text, ok := value.(string)
	if !ok {
		text = jsontext.Dumps(value, 0)
	}
	if len([]rune(text)) <= 60 {
		return text
	}
	return string([]rune(text)[:57]) + "..."
}

func logicappDiff(rt *Runtime) *cobra.Command {
	common := &Common{}
	var parameters bool
	command := &cobra.Command{
		Use:   "diff REFERENCE DIFFERENCE",
		Short: "Compare two definitions, whatever their shapes, and list where they differ.",
		Long: "Compare two definitions, whatever their shapes, and list where they differ.\n\n" +
			"REFERENCE is the baseline, e.g. the rendered template; DIFFERENCE the other, e.g. the deployed export. Exits 3 when they differ.",
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			reference, err := logicapps.Load(args[0])
			if err != nil {
				return err
			}
			difference, err := logicapps.Load(args[1])
			if err != nil {
				return err
			}
			found := logicapps.Compare(reference, difference, parameters)
			rows := make([][]render.Cell, len(found))
			records := []any{}
			for index, item := range found {
				rows[index] = render.Cells(item.Path, item.Change, short(item.Reference), short(item.Difference))
				records = append(records, map[string]any{"path": item.Path, "change": item.Change, "reference": item.Reference, "difference": item.Difference})
			}
			if err := rt.Console.Emit(output, []string{"PATH", "CHANGE", "REFERENCE", "DIFFERENCE"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d difference(s)", len(found))
			if len(found) > 0 {
				return Attention
			}
			return nil
		},
	}
	command.Flags().BoolVar(&parameters, "parameters", false, "Compare the wrapper parameter values too.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

// writeOut writes text to out, or prints it when out is empty.
func writeOut(rt *Runtime, text, out string) error {
	if out == "" {
		rt.Console.Println(strings.TrimSuffix(text, "\n"))
		return nil
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return errs.Inputf("cannot write %s: %v", out, err)
	}
	rt.Console.Note("wrote %s", out)
	return nil
}

func logicappDefaults(rt *Runtime) *cobra.Command {
	var force bool
	var out string
	command := &cobra.Command{
		Use:   "defaults FILE",
		Short: "Copy each wrapper value onto its declaration as a default, so the definition stands alone.",
		Long: "Copy each wrapper value onto its declaration as a default, so the definition stands alone.\n\n" +
			"FILE is an export (code view or ARM shape). $connections and secure parameters are left alone. The result carries " +
			"the source estate's values; rewrite re-points them.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			document, err := logicapps.Load(args[0])
			if err != nil {
				return err
			}
			definition, added, err := logicapps.WithParameterDefaults(document, force)
			if err != nil {
				return err
			}
			if err := writeOut(rt, jsontext.Dumps(definition, 2)+"\n", out); err != nil {
				return err
			}
			rt.Console.Note("added %d default(s)", added)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Replace defaults that are already there.")
	command.Flags().StringVar(&out, "out", "", "Write here instead of printing.")
	return command
}

func logicappRewrite(rt *Runtime) *cobra.Command {
	var replace []string
	var out string
	command := &cobra.Command{
		Use:   "rewrite FILE",
		Short: "Rewrite estate-specific references (ids, names, URLs) to lift a definition elsewhere.",
		Long: "Rewrite estate-specific references (ids, names, URLs) to lift a definition elsewhere.\n\n" +
			"Replacements are literal and applied in the order given, most specific first.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(replace) == 0 {
				return Usagef("--replace", "--replace is required")
			}
			var pairs [][2]string
			for _, item := range replace {
				old, new, found := strings.Cut(item, "=")
				if !found || old == "" {
					return Usagef("--replace", "--replace takes OLD=NEW, not '%s'", item)
				}
				pairs = append(pairs, [2]string{old, new})
			}
			document, err := logicapps.Load(args[0])
			if err != nil {
				return err
			}
			text, err := logicapps.RewriteReferences(document.Text, pairs)
			if err != nil {
				return err
			}
			return writeOut(rt, text, out)
		},
	}
	command.Flags().StringArrayVar(&replace, "replace", nil, "OLD=NEW, a literal find and replace. Repeatable; applied in order.")
	command.Flags().StringVar(&out, "out", "", "Write here instead of printing.")
	return command
}

// used by export: the code view of a resource.
func codeView(resource yamltext.Ordered) yamltext.Ordered {
	properties, _ := resource.Get("properties")
	props, _ := properties.(yamltext.Ordered)
	definition, _ := props.Get("definition")
	document := yamltext.Ordered{{Key: "definition", Value: definition}}
	if parameters, ok := props.Get("parameters"); ok {
		document = append(document, yamltext.Pair{Key: "parameters", Value: parameters})
	}
	return document
}
