package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/azure"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
)

// azureCommand is the azure group; the automation commands join it.
func azureCommand(rt *Runtime) *cobra.Command {
	group := newGroup("azure", "Azure: subscriptions, Resource Graph, RBAC, Defender for Cloud, and resource ids.")
	group.AddCommand(azureSubscriptions(rt), azureResourceGraph(rt), azureRBAC(rt), azureSecureScore(rt), azureRecommendations(rt),
		azureDefenderPlans(rt), azureParseID(rt), automationCommand(rt))
	return group
}

const subscriptionHelp = "Subscription id to cover. Repeatable. Default: the profile's subscription, else every one in the tenant."

// azureClient is an Azure client for a profile.
func azureClient(rt *Runtime, profile microsoft.Profile) (*azure.Client, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	return azure.New(api)
}

// subscriptionIDs are the subscriptions an Azure command covers: chosen, the profile's
// pinned one, or every subscription the credential can see in the profile's tenant.
func subscriptionIDs(rt *Runtime, client *azure.Client, profile microsoft.Profile, chosen []string) ([]string, error) {
	if len(chosen) > 0 {
		return chosen, nil
	}
	if profile.SubscriptionID != "" {
		return []string{profile.SubscriptionID}, nil
	}
	found, err := client.Subscriptions(rt.Ctx(), profile.TenantID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(found))
	for index, subscription := range found {
		ids[index] = subscription.ID
	}
	return ids, nil
}

// azureSetup is the profile, its Azure client and the subscriptions a command covers.
func azureSetup(rt *Runtime, common *Common, chosen []string) (microsoft.Profile, *azure.Client, []string, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return profile, nil, nil, err
	}
	client, err := azureClient(rt, profile)
	if err != nil {
		return profile, nil, nil, err
	}
	ids, err := subscriptionIDs(rt, client, profile, chosen)
	return profile, client, ids, err
}

func azureSubscriptions(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "subscriptions",
		Short: "List the subscriptions the profile's credential can see in its tenant.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			client, err := azureClient(rt, profile)
			if err != nil {
				return err
			}
			found, err := client.Subscriptions(rt.Ctx(), profile.TenantID)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []fields.Object{}
			for index, item := range found {
				rows[index] = render.Cells(item.Name, item.ID, item.State)
				records = append(records, item.Raw)
			}
			return rt.Console.Emit(output, []string{"SUBSCRIPTION", "ID", "STATE"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	return command
}

func azureResourceGraph(rt *Runtime) *cobra.Command {
	common := &Common{}
	var file string
	var limit int
	var subscriptions []string
	command := &cobra.Command{
		Use:   "resource-graph [QUERY]",
		Short: "Run an Azure Resource Graph (KQL) query across subscriptions.",
		Long:  "Run an Azure Resource Graph (KQL) query across subscriptions. Omit the query, or pass -, to read it from stdin.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			text, err := readQuery(rt, query, file)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			client, err := azureClient(rt, profile)
			if err != nil {
				return err
			}
			scope := subscriptions
			if len(scope) == 0 && profile.SubscriptionID != "" {
				scope = []string{profile.SubscriptionID}
			}
			result, err := client.ResourceGraph(rt.Ctx(), text, scope, limit)
			if err != nil {
				return err
			}
			if err := rt.Console.Query(result, output); err != nil {
				return err
			}
			rt.Console.Note("%d row(s)", len(result.Rows))
			return nil
		},
	}
	command.Flags().StringVar(&file, "file", "", "Read the query from a file.")
	command.Flags().IntVar(&limit, "limit", 1000, "Most rows to fetch.")
	command.Flags().StringArrayVarP(&subscriptions, "subscription", "s", nil, subscriptionHelp)
	common.AddOutput(command, true)
	return command
}

func azureRBAC(rt *Runtime) *cobra.Command {
	common := &Common{}
	var subscriptions []string
	command := &cobra.Command{
		Use:   "rbac PRINCIPAL",
		Short: "List every Azure role assignment that applies to a principal, across subscriptions.",
		Long: "List every Azure role assignment that applies to a principal, across subscriptions.\n\n" +
			"PRINCIPAL is a user principal name, a group or service principal name, or an object id. Includes " +
			"assignments made to groups the principal is in, and those inherited from management groups.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, client, ids, err := azureSetup(rt, common, subscriptions)
			if err != nil {
				return err
			}
			principalID, label, err := principalOf(rt, profile, args[0])
			if err != nil {
				return err
			}
			assignments, err := client.RoleAssignments(rt.Ctx(), principalID, ids)
			if err != nil {
				return err
			}
			if output == render.Table {
				rt.Console.Println(title(rt, label+"  object "+principalID))
			}
			rows := make([][]render.Cell, len(assignments))
			records := []any{}
			for index, item := range assignments {
				assigned := "this principal"
				if strings.ToLower(item.PrincipalID) != principalID {
					assigned = item.PrincipalType + " " + item.PrincipalID
				}
				condition := ""
				if item.Condition != "" {
					condition = "yes"
				}
				rows[index] = render.Cells(item.RoleName, item.Scope, assigned, condition)
				record := fields.Object{}
				for key, value := range item.Raw {
					record[key] = value
				}
				record["roleName"] = item.RoleName
				records = append(records, record)
			}
			if err := rt.Console.Emit(output, []string{"ROLE", "SCOPE", "ASSIGNED TO", "CONDITION"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d assignment(s) across %d subscription(s)", len(assignments), len(ids))
			return nil
		},
	}
	command.Flags().StringArrayVarP(&subscriptions, "subscription", "s", nil, subscriptionHelp)
	common.AddOutput(command, true)
	return command
}

// principalOf is a principal's object id and how to name it: an id as it is, a name
// through Entra.
func principalOf(rt *Runtime, profile microsoft.Profile, principal string) (string, string, error) {
	if util.IsGUID(principal) {
		return strings.ToLower(strings.TrimSpace(principal)), principal, nil
	}
	api, err := rt.API(profile)
	if err != nil {
		return "", "", err
	}
	client, err := entra.New(api)
	if err != nil {
		return "", "", err
	}
	found, err := client.FindPrincipal(rt.Ctx(), principal)
	if err != nil {
		return "", "", err
	}
	return found.ID, found.DisplayName + " (" + found.Kind + ")", nil
}

func formatNumber(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'g', 6, 64)
}

func azureSecureScore(rt *Runtime) *cobra.Command {
	common := &Common{}
	var controls bool
	var subscriptions []string
	command := &cobra.Command{
		Use:   "secure-score",
		Short: "Show the Defender for Cloud secure score for each subscription.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			_, client, ids, err := azureSetup(rt, common, subscriptions)
			if err != nil {
				return err
			}
			var found []azure.SecureScore
			for _, id := range ids {
				score, err := client.SecureScore(rt.Ctx(), id)
				if err != nil {
					return err
				}
				if score != nil {
					found = append(found, *score)
				}
			}
			if err := showScores(rt, output, found); err != nil {
				return err
			}
			if missing := len(ids) - len(found); missing > 0 {
				rt.Console.Warn("%d subscription(s) have no secure score", missing)
			}
			if controls && output != render.JSON {
				return showControls(rt, output, client, found)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&controls, "controls", false, "Also list the controls costing the most points.")
	command.Flags().StringArrayVarP(&subscriptions, "subscription", "s", nil, subscriptionHelp)
	common.AddOutput(command, false)
	return command
}

func showScores(rt *Runtime, output render.Output, found []azure.SecureScore) error {
	rows := make([][]render.Cell, len(found))
	records := []fields.Object{}
	for index, score := range found {
		percent := ""
		if score.Percentage != nil {
			percent = fmt.Sprintf("%.0f%%", *score.Percentage*100)
		}
		rows[index] = render.Cells(score.SubscriptionID, formatNumber(score.Current), formatNumber(score.Max), percent)
		records = append(records, score.Raw)
	}
	return rt.Console.Emit(output, []string{"SUBSCRIPTION", "SCORE", "MAX", "PERCENT"}, rows, records)
}

func showControls(rt *Runtime, output render.Output, client *azure.Client, found []azure.SecureScore) error {
	for _, score := range found {
		rt.Console.Println("")
		rt.Console.Println(title(rt, "controls for "+score.SubscriptionID))
		items, err := client.SecureScoreControls(rt.Ctx(), score.SubscriptionID)
		if err != nil {
			return err
		}
		var rows [][]render.Cell
		for _, item := range items {
			if item.Max != nil && *item.Max != 0 {
				rows = append(rows, render.Cells(item.Name, fmt.Sprintf("%.2f", item.PointsLost()), formatNumber(item.Current),
					formatNumber(item.Max), strconv.Itoa(item.Unhealthy), strconv.Itoa(item.Healthy)))
			}
		}
		if err := rt.Console.Emit(output, []string{"CONTROL", "POINTS LOST", "SCORE", "MAX", "UNHEALTHY", "HEALTHY"}, rows, nil); err != nil {
			return err
		}
	}
	return nil
}

func azureRecommendations(rt *Runtime) *cobra.Command {
	common := &Common{}
	var showAll bool
	var severity string
	var subscriptions []string
	command := &cobra.Command{
		Use:   "recommendations",
		Short: "List Defender for Cloud recommendations that resources fail, most severe first.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if severity != "" && !strings.Contains(" high medium low ", " "+strings.ToLower(severity)+" ") {
				return Usagef("--severity", "--severity must be high, medium or low")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			_, client, ids, err := azureSetup(rt, common, subscriptions)
			if err != nil {
				return err
			}
			var found []azure.Assessment
			for _, id := range ids {
				items, err := client.Assessments(rt.Ctx(), id, !showAll)
				if err != nil {
					return err
				}
				for _, item := range items {
					if severity == "" || strings.EqualFold(item.Severity, severity) {
						found = append(found, item)
					}
				}
			}
			colours := map[string]string{"high": "red", "medium": "yellow"}
			rows := make([][]render.Cell, len(found))
			records := []fields.Object{}
			for index, item := range found {
				rows[index] = []render.Cell{render.Coloured(item.Severity, colours[strings.ToLower(item.Severity)]), render.Plain(item.Status),
					render.Plain(item.Name), render.Plain(item.ResourceID)}
				records = append(records, item.Raw)
			}
			if err := rt.Console.Emit(output, []string{"SEVERITY", "STATUS", "RECOMMENDATION", "RESOURCE"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d result(s)", len(found))
			return nil
		},
	}
	command.Flags().BoolVar(&showAll, "all", false, "Include healthy and not applicable results.")
	command.Flags().StringVar(&severity, "severity", "", "Only this severity: high, medium or low.")
	command.Flags().StringArrayVarP(&subscriptions, "subscription", "s", nil, subscriptionHelp)
	common.AddOutput(command, true)
	return command
}

func azureDefenderPlans(rt *Runtime) *cobra.Command {
	common := &Common{}
	var subscriptions []string
	command := &cobra.Command{
		Use:   "defender-plans",
		Short: "List every Defender for Cloud plan on each subscription and whether it is on.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			_, client, ids, err := azureSetup(rt, common, subscriptions)
			if err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			for _, id := range ids {
				plans, err := client.DefenderPlans(rt.Ctx(), id)
				if err != nil {
					return err
				}
				for _, plan := range plans {
					rows = append(rows, planRow(rt, id, plan))
					record := fields.Object{"subscriptionId": id}
					for key, value := range plan.Raw {
						record[key] = value
					}
					records = append(records, record)
				}
			}
			return rt.Console.Emit(output, []string{"SUBSCRIPTION", "PLAN", "STATE", "SUB PLAN", "SINCE", "NOTE"}, rows, records)
		},
	}
	command.Flags().StringArrayVarP(&subscriptions, "subscription", "s", nil, subscriptionHelp)
	common.AddOutput(command, true)
	return command
}

func planRow(rt *Runtime, subscription string, plan azure.DefenderPlan) []render.Cell {
	state := render.Coloured("off", "yellow")
	if plan.Enabled() {
		state = render.Coloured("on", "green")
	}
	since, note := "", ""
	if !plan.EnabledSince.IsZero() {
		since = rt.Console.When(plan.EnabledSince)
	}
	if plan.Deprecated {
		note = "deprecated"
	}
	return []render.Cell{render.Plain(subscription), render.Plain(plan.Name), state, render.Plain(plan.SubPlan), render.Plain(since), render.Plain(note)}
}

func azureParseID(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	command := &cobra.Command{
		Use:   "parse-id [RESOURCE_ID]...",
		Short: "Split Azure resource ids into their parts: subscription, resource group, name, type.",
		Long: "Split Azure resource ids into their parts: subscription, resource group, name, type.\n\n" +
			"Works offline: nothing is looked up, so the resource need not exist. -o json gives the keys Terraform's " +
			"provider::azurerm::parse_resource_id does (resource_group_name, resource_name, full_resource_type, " +
			"parent_resources, resource_scope and the rest), plus id and management_group_name. SCOPE is what an " +
			"extension resource (a lock, a role assignment) is on. Exits 1 when any id cannot be read, after showing the rest.",
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			var parsed []microsoft.ResourceID
			var failed []error
			for _, text := range wanted {
				id, err := microsoft.ParseResourceID(text)
				if err != nil {
					if len(wanted) == 1 {
						return err
					}
					failed = append(failed, err)
					continue
				}
				parsed = append(parsed, id)
			}
			rows := make([][]render.Cell, len(parsed))
			records := []any{}
			for index, id := range parsed {
				rows[index] = idRow(id)
				records = append(records, id.Parts())
			}
			if err := rt.Console.Emit(output, []string{"NAME", "TYPE", "RESOURCE GROUP", "SUBSCRIPTION", "PARENTS", "SCOPE"}, rows, records); err != nil {
				return err
			}
			for _, problem := range failed {
				rt.Console.Warn("%s", problem)
			}
			if len(failed) > 0 {
				return errs.Inputf("%d of %d id(s) are not Azure resource ids", len(failed), len(wanted)).WithHint("%s", errs.HintOf(failed[0]))
			}
			return nil
		},
	}
	names.Add(command)
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func idRow(id microsoft.ResourceID) []render.Cell {
	var parents []string
	for index := 0; index+1 < len(id.Types); index++ {
		parents = append(parents, id.Types[index]+"/"+id.Names[index])
	}
	scope := ""
	if id.Parent != nil {
		scope = id.Parent.ID
	}
	return render.Cells(id.Name(), id.Type(), id.ResourceGroup, id.Subscription, strings.Join(parents, ", "), scope)
}
