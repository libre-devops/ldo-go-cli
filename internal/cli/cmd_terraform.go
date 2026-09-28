package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/terraform"
	"github.com/libre-devops/ldo-go-cli/internal/terraform/docs"
	tfsort "github.com/libre-devops/ldo-go-cli/internal/terraform/sort"
)

// kindNames are how each kind of block reads in a table.
var kindNames = map[string]string{"variable": "variables", "output": "outputs"}

func terraformCommand(rt *Runtime) *cobra.Command {
	group := newGroup("terraform", "Terraform modules: their variables and outputs in name order, and their README from "+
		"HEADER.md and terraform-docs. Changes only the files of the folders named.")
	group.AddCommand(terraformSortCommand(rt), terraformDocsCommand(rt))
	return group
}

// runner runs the tools other than az.
func (r *Runtime) runner() process.Runner {
	if r.Runner != nil {
		return r.Runner
	}
	return process.Exec{}
}

func terraformSortCommand(rt *Runtime) *cobra.Command {
	var common Common
	var inputs, outputs, recursive, check, noFmt bool
	command := &cobra.Command{
		Use:   "sort [PATH]...",
		Short: "Put a module's variables and outputs in name order, as terraform-docs lists them, then run terraform fmt on it.",
		Long: "Put a module's variables and outputs in name order, as terraform-docs lists them, then run terraform fmt on it. " +
			"PATH is a module folder (its variables.tf and outputs.tf) or a .tf file; the current folder by default.\n\n" +
			"Only the blocks move: the comments just above one move with it, and all else in the file stays where it was. " +
			"Without --inputs or --outputs, both. terraform fmt is terraform's, or OpenTofu's tofu when terraform is not on " +
			"PATH; with neither, the files are still sorted, and a warning says so. --check changes nothing and exits 3 " +
			"when a file is not in order, for a pipeline.",
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			chosen := args
			if len(chosen) == 0 {
				chosen = []string{"."}
			}
			kinds := tfsort.Kinds
			if inputs != outputs {
				kinds = []string{"variable"}
				if outputs {
					kinds = []string{"output"}
				}
			}
			found, err := sortTargets(chosen, kinds, recursive, !check)
			if err != nil {
				return err
			}
			if err := showSorted(rt, output, found, check); err != nil || check || noFmt {
				return err
			}
			return formatModules(rt, chosen, recursive)
		},
	}
	flags := command.Flags()
	flags.BoolVar(&inputs, "inputs", false, "Only the variables: a folder's variables.tf.")
	flags.BoolVar(&outputs, "outputs", false, "Only the outputs: a folder's outputs.tf.")
	flags.BoolVarP(&recursive, "recursive", "r", false, "Every folder beneath too: examples/, modules/ and the like.")
	flags.BoolVar(&check, "check", false, "Change nothing: say which would change, and exit 3 if any.")
	flags.BoolVar(&noFmt, "no-fmt", false, "Do not run terraform fmt after sorting.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func sortTargets(chosen, kinds []string, recursive, write bool) ([]tfsort.Sorting, error) {
	targets, err := tfsort.Targets(chosen, kinds, recursive)
	if err != nil {
		return nil, err
	}
	var found []tfsort.Sorting
	for _, target := range targets {
		sorted, err := tfsort.File(target.Path, target.Kinds, write)
		if err != nil {
			return nil, err
		}
		found = append(found, sorted...)
	}
	return found, nil
}

func sortState(item tfsort.Sorting, check bool) string {
	switch {
	case item.InOrder:
		return "in order"
	case check:
		return "out of order"
	}
	return "sorted"
}

func showSorted(rt *Runtime, output render.Output, found []tfsort.Sorting, check bool) error {
	rows := make([][]render.Cell, len(found))
	records := make([]any, len(found))
	moved := 0
	for index, item := range found {
		state := sortState(item, check)
		colour := "green"
		if state == "out of order" {
			colour = "yellow"
		}
		rows[index] = []render.Cell{render.Plain(item.Path), render.Plain(kindNames[item.Kind]), render.Plain(fmt.Sprint(item.Count)),
			render.Coloured(state, colour)}
		records[index] = map[string]any{"file": item.Path, "blocks": kindNames[item.Kind], "count": item.Count, "in_order": item.InOrder,
			"state": state}
		if !item.InOrder {
			moved++
		}
	}
	if err := rt.Console.Emit(output, []string{"FILE", "BLOCKS", "COUNT", "STATE"}, rows, records); err != nil {
		return err
	}
	if !check {
		rt.Console.Note("sorted %d of %d; the rest were in order", moved, len(found))
		return nil
	}
	if moved == 0 {
		rt.Console.Note("0 of %d not in order", len(found))
		return nil
	}
	rt.Console.Note("%d of %d not in order: run without --check", moved, len(found))
	return Attention
}

func formatModules(rt *Runtime, chosen []string, recursive bool) error {
	tool := terraform.Formatter(rt.lookPath(), rt.runner())
	if tool == nil {
		rt.Console.Warn("terraform fmt not run: neither terraform nor tofu is on PATH (--no-fmt)")
		return nil
	}
	changed, err := terraform.FormatCode(rt.Ctx(), tool, chosen, recursive)
	if err != nil {
		return err
	}
	rt.Console.Note("%s fmt formatted %d file(s)", tool.Name, len(changed))
	return nil
}

func terraformDocsCommand(rt *Runtime) *cobra.Command {
	var common Common
	var header, readme string
	var recursive, check bool
	command := &cobra.Command{
		Use:   "docs [FOLDER]...",
		Short: "Write each module's README: its HEADER.md at the top, then the section terraform-docs writes between its markers.",
		Long: "Write each module's README: its HEADER.md at the top, then the section terraform-docs writes between its " +
			"markers. FOLDER is a module folder; the current one by default.\n\n" +
			"terraform-docs is not part of this, and must be on PATH: it reads the module's own .terraform-docs.yml when it " +
			"has one, and writes a Markdown table when not. A folder without a HEADER.md keeps its README's own top. With " +
			"--recursive, every folder beneath with a HEADER.md of its own (an example, a submodule) too. --check changes " +
			"nothing and exits 3 when a README is out of date.",
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			for _, given := range [][2]string{{"--header", header}, {"--readme", readme}} {
				if name := given[1]; filepath.Base(name) != name || name == "" || name == "." || name == ".." {
					return Usagef(given[0], "give a file name, found in each folder")
				}
			}
			tool, err := terraform.TerraformDocs(rt.lookPath(), rt.runner())
			if err != nil {
				return err
			}
			chosen := args
			if len(chosen) == 0 {
				chosen = []string{"."}
			}
			folders, err := docs.Folders(chosen, recursive, header)
			if err != nil {
				return err
			}
			var found []docs.Result
			for _, folder := range folders {
				result, err := docs.Document(rt.Ctx(), folder, tool, docs.Options{Check: check, Header: header, Readme: readme})
				if err != nil {
					return err
				}
				found = append(found, result)
			}
			return showReadmes(rt, output, found, check)
		},
	}
	flags := command.Flags()
	flags.StringVar(&header, "header", docs.Header, "The file in each folder with the README's top.")
	flags.StringVar(&readme, "readme", docs.Readme, "The README to write, in each folder.")
	flags.BoolVarP(&recursive, "recursive", "r", false, "Every folder beneath too: examples/, modules/ and the like.")
	flags.BoolVar(&check, "check", false, "Change nothing: say which would change, and exit 3 if any.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func showReadmes(rt *Runtime, output render.Output, found []docs.Result, check bool) error {
	rows := make([][]render.Cell, len(found))
	records := make([]any, len(found))
	stale := 0
	for index, item := range found {
		colour := "green"
		if item.State == "out of date" {
			colour = "yellow"
			stale++
		}
		shownHeader := "-"
		if item.Header != "" {
			shownHeader = filepath.Base(item.Header)
		}
		rows[index] = []render.Cell{render.Plain(item.Path), render.Plain(shownHeader), render.Coloured(item.State, colour)}
		records[index] = map[string]any{"folder": item.Folder, "readme": item.Path, "header": orNil(item.Header), "state": item.State}
	}
	if err := rt.Console.Emit(output, []string{"README", "HEADER", "STATE"}, rows, records); err != nil {
		return err
	}
	if check && stale > 0 {
		rt.Console.Note("%d of %d out of date: run without --check", stale, len(found))
		return Attention
	}
	return nil
}
