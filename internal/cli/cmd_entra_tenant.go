package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
)

func entraAppCredentials(rt *Runtime) *cobra.Command {
	common := &Common{}
	var within string
	var showAll, servicePrincipals, hideExpired bool
	includeExpired := true
	command := &cobra.Command{
		Use:   "app-credentials",
		Short: "List app registration secrets and certificates close to expiry.",
		Long: "List app registration secrets and certificates close to expiry.\n\n" +
			"Exits 3 when any credential shown is expiring or expired, so a scheduled job can alert.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			window, err := Duration("--expiring", within)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := entraClient(rt, common.Profile)
			if err != nil {
				return err
			}
			credentials, err := client.AppCredentials(rt.Ctx(), servicePrincipals)
			if err != nil {
				return err
			}
			now := rt.Clock()
			shown := entra.Expiring(credentials, window, now, includeExpired && !hideExpired)
			if showAll {
				shown = append([]entra.AppCredential(nil), credentials...)
				entra.SortByEnd(shown, now)
			}
			return showCredentials(rt, output, credentials, shown, now, window)
		},
	}
	command.Flags().StringVar(&within, "expiring", "30d", "Show credentials ending within this, e.g. 30d.")
	command.Flags().BoolVar(&showAll, "all", false, "Show every credential, not only expiring ones.")
	command.Flags().BoolVar(&servicePrincipals, "service-principals", false, "Include enterprise apps (service principals), e.g. SAML signing certificates.")
	command.Flags().BoolVar(&includeExpired, "include-expired", true, "Show expired credentials.")
	command.Flags().BoolVar(&hideExpired, "hide-expired", false, "Hide expired credentials.")
	common.AddOutput(command, true)
	return command
}

func showCredentials(rt *Runtime, output render.Output, all, shown []entra.AppCredential, now time.Time, window time.Duration) error {
	rows := make([][]render.Cell, len(shown))
	records := []any{}
	attention := 0
	for index, item := range shown {
		rows[index] = credentialRow(rt, item, now, window)
		var days any
		if left, ok := item.DaysLeft(now); ok {
			days = left
		}
		records = append(records, map[string]any{"owner_kind": item.OwnerKind, "owner_name": item.OwnerName, "owner_id": item.OwnerID,
			"app_id": item.AppID, "kind": item.Kind, "credential": item.Raw, "days_left": days})
		if !item.Ends.IsZero() && item.Ends.Sub(now) <= window {
			attention++
		}
	}
	if err := rt.Console.Emit(output, []string{"APP", "KIND", "CREDENTIAL", "ENDS", "DAYS LEFT", "OWNER", "APP ID"}, rows, records); err != nil {
		return err
	}
	if output == render.Table {
		rt.Console.Note("%d credential(s) end within %s (of %d checked)", attention, util.FormatSpan(window), len(all))
	}
	if attention > 0 {
		return Attention
	}
	return nil
}

func credentialRow(rt *Runtime, item entra.AppCredential, now time.Time, window time.Duration) []render.Cell {
	left := render.Plain("-")
	if days, ok := item.DaysLeft(now); ok {
		switch {
		case days < 0:
			left = render.Coloured(fmt.Sprintf("expired %dd ago", -days), "red")
		case item.Ends.Sub(now) <= window:
			left = render.Coloured(fmt.Sprint(days), "yellow")
		default:
			left = render.Plain(fmt.Sprint(days))
		}
	}
	return []render.Cell{render.Plain(item.OwnerName), render.Plain(item.Kind), render.Plain(item.Name),
		render.Plain(rt.Console.When(item.Ends)), left, render.Plain(item.OwnerKind), render.Plain(item.AppID)}
}

var policyStates = [][2]string{{"enabled", "enabled"}, {"disabled", "disabled"}, {"report-only", "enabledForReportingButNotEnforced"}}

func entraCAPolicies(rt *Runtime) *cobra.Command {
	common := &Common{}
	var state string
	command := &cobra.Command{
		Use:   "ca-policies",
		Short: "List Conditional Access policies: who and what each targets, and what it requires.",
		Long: "List Conditional Access policies: who and what each targets, and what it requires.\n\n" +
			"Needs Policy.Read.All, which the Azure CLI's token does not carry: use a profile with its own app registration.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			wanted := ""
			if state != "" {
				for _, known := range policyStates {
					if state == known[0] {
						wanted = known[1]
					}
				}
				if wanted == "" {
					return Usagef("--state", "--state must be one of enabled, disabled, report-only")
				}
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := entraClient(rt, common.Profile)
			if err != nil {
				return err
			}
			policies, err := client.CAPolicies(rt.Ctx())
			if err != nil {
				return err
			}
			var kept []entra.ConditionalAccessPolicy
			for _, policy := range policies {
				if wanted == "" || policy.State == wanted {
					kept = append(kept, policy)
				}
			}
			if err := showPolicies(rt, output, kept); err != nil {
				return err
			}
			if len(kept) == 0 && wanted == "" {
				// Graph answers an unauthorised listing with an empty list rather than an error.
				rt.Console.Warn("no Conditional Access policies returned; if you expected some, the token may lack " +
					"Policy.Read.All, which the Azure CLI's token never has")
			}
			return nil
		},
	}
	command.Flags().StringVar(&state, "state", "", "Only this state: enabled, disabled or report-only.")
	common.AddOutput(command, true)
	return command
}

func showPolicies(rt *Runtime, output render.Output, policies []entra.ConditionalAccessPolicy) error {
	names := map[string]string{}
	for _, known := range policyStates {
		names[known[1]] = known[0]
	}
	rows := make([][]render.Cell, len(policies))
	records := []fields.Object{}
	for index, policy := range policies {
		state := policy.State
		if name, ok := names[state]; ok {
			state = name
		}
		grant := strings.Join(policy.GrantControls, " "+policy.GrantOperator+" ")
		if grant == "" {
			grant = "-"
		}
		users := targets(append(append(append([]string(nil), policy.IncludeUsers...), policy.IncludeGroups...), policy.IncludeRoles...),
			append(append([]string(nil), policy.ExcludeUsers...), policy.ExcludeGroups...))
		rows[index] = render.Cells(policy.DisplayName, state, users, targets(policy.IncludeApplications, policy.ExcludeApplications),
			grant, strings.Join(policy.SessionControls, ", "), rt.Console.When(policy.Modified))
		records = append(records, policy.Raw)
	}
	return rt.Console.Emit(output, []string{"POLICY", "STATE", "USERS", "APPS", "GRANT", "SESSION", "MODIFIED"}, rows, records)
}

// targets is All or "3 included", plus ", 1 excluded": ids alone mean little in a table.
func targets(include, exclude []string) string {
	text := fmt.Sprintf("%d included", len(include))
	switch {
	case len(include) == 0:
		text = "none"
	case len(include) == 1 && !util.IsGUID(include[0]):
		text = include[0]
	}
	if len(exclude) > 0 {
		return fmt.Sprintf("%s, %d excluded", text, len(exclude))
	}
	return text
}
