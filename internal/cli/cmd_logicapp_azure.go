package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/jsontext"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/logicapps"
)

// logicappSubscription is the subscription a Logic App command reads: --subscription, the
// profile's, or its only one.
func logicappSubscription(rt *Runtime, profile microsoft.Profile, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if profile.SubscriptionID != "" {
		return profile.SubscriptionID, nil
	}
	client, err := azureClient(rt, profile)
	if err != nil {
		return "", err
	}
	found, err := subscriptionIDs(rt, client, profile, nil)
	if err != nil {
		return "", err
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", errs.Inputf("profile '%s' can see %d subscriptions", profile.Name, len(found)).WithHint("pass --subscription, or pin the profile to one")
}

func logicappClient(rt *Runtime, common *Common, subscription string) (*logicapps.Client, string, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return nil, "", err
	}
	sub, err := logicappSubscription(rt, profile, subscription)
	if err != nil {
		return nil, "", err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, "", err
	}
	client, err := logicapps.New(api)
	return client, sub, err
}

func logicappExport(rt *Runtime) *cobra.Command {
	common := &Common{}
	var group, out, shape, subscription string
	var names []string
	command := &cobra.Command{
		Use:   "export",
		Short: "Export deployed workflows to files, one per workflow. Reads Azure; writes only files.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if group == "" || out == "" {
				return Usagef("--resource-group", "--resource-group and --out are required")
			}
			if shape != "code-view" && shape != "arm" {
				return Usagef("--shape", "--shape must be code-view or arm")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, sub, err := logicappClient(rt, common, subscription)
			if err != nil {
				return err
			}
			listed, err := client.Workflows(rt.Ctx(), sub, group)
			if err != nil {
				return err
			}
			var wanted []fields.Object
			for _, item := range listed {
				if len(names) == 0 || containsFold(names, fields.Text(item, "name")) {
					wanted = append(wanted, item)
				}
			}
			if len(wanted) == 0 {
				rt.Console.Warn("no Logic App workflows matched in %s", group)
				return nil
			}
			return exportWorkflows(rt, output, client, sub, group, out, shape, wanted)
		},
	}
	command.Flags().StringVarP(&group, "resource-group", "g", "", "The resource group.")
	command.Flags().StringVar(&out, "out", "", "The folder to write into.")
	command.Flags().StringArrayVar(&names, "name", nil, "Only this workflow. Repeatable.")
	command.Flags().StringVar(&shape, "shape", "code-view", "code-view (definition and values) or arm (the whole resource).")
	command.Flags().StringVarP(&subscription, "subscription", "s", "", "Subscription id. Default: the profile's, or its only one.")
	common.AddOutput(command, true)
	return command
}

func containsFold(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func exportWorkflows(rt *Runtime, output render.Output, client *logicapps.Client, sub, group, out, shape string, wanted []fields.Object) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return errs.Inputf("cannot make %s: %v", out, err)
	}
	var rows [][]render.Cell
	records := []any{}
	for _, item := range wanted {
		workflow := fields.Text(item, "name")
		resource, err := client.Workflow(rt.Ctx(), sub, group, workflow)
		if err != nil {
			return err
		}
		document := resource
		if shape == "code-view" {
			document = codeView(resource)
		}
		path := filepath.Join(out, workflow+".json")
		if err := os.WriteFile(path, []byte(jsontext.Dumps(document, 2)+"\n"), 0o644); err != nil {
			return errs.Inputf("cannot write %s: %v", path, err)
		}
		id, _ := resource.Get("id")
		rows = append(rows, render.Cells(workflow, path))
		records = append(records, yamltext.Ordered{{Key: "workflow", Value: workflow}, {Key: "id", Value: id}, {Key: "shape", Value: shape},
			{Key: "path", Value: path}})
	}
	if err := rt.Console.Emit(output, []string{"WORKFLOW", "PATH"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("exported %d workflow(s) to %s", len(rows), out)
	return nil
}

func logicappValidate(rt *Runtime) *cobra.Command {
	common := &Common{}
	var group, location, name, subscription string
	command := &cobra.Command{
		Use:   "validate FILE",
		Short: "Ask Azure whether it would accept a definition, without deploying anything.",
		Long: "Ask Azure whether it would accept a definition, without deploying anything.\n\n" +
			"FILE is the definition, in any of the three shapes. The provider's own verdict: it type-checks the whole " +
			"definition and creates nothing. Exits 3 when it is rejected.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if group == "" {
				return Usagef("--resource-group", "--resource-group is required")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			document, err := logicapps.Load(args[0])
			if err != nil {
				return err
			}
			client, sub, err := logicappClient(rt, common, subscription)
			if err != nil {
				return err
			}
			region := location
			if region == "" {
				if region, err = client.LocationOf(rt.Ctx(), sub, group); err != nil {
					return err
				}
			}
			verdict, err := client.Validate(rt.Ctx(), document, sub, group, region, name)
			if err != nil {
				return err
			}
			valid := render.Coloured("no", "red")
			if verdict.Valid {
				valid = render.Coloured("yes", "green")
			}
			if err := rt.Console.Emit(output, []string{"WORKFLOW", "LOCATION", "VALID", "MESSAGE"}, [][]render.Cell{{render.Plain(verdict.Workflow),
				render.Plain(verdict.Location), valid, render.Plain(verdict.Message)}}, map[string]any{"workflow": verdict.Workflow,
				"location": verdict.Location, "valid": verdict.Valid, "code": orNil(verdict.Code), "message": verdict.Message}); err != nil {
				return err
			}
			if !verdict.Valid {
				return Attention
			}
			return nil
		},
	}
	command.Flags().StringVarP(&group, "resource-group", "g", "", "The resource group.")
	command.Flags().StringVar(&location, "location", "", "Region, e.g. uksouth. Default: the resource group's.")
	command.Flags().StringVar(&name, "name", "", "Workflow name for the call. Default: from the file.")
	command.Flags().StringVarP(&subscription, "subscription", "s", "", "Subscription id. Default: the profile's, or its only one.")
	common.AddOutput(command, false)
	return command
}
