package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
)

const userArgHelp = "USER is a user principal name, object id or display name."

func entraUserGroups(rt *Runtime) *cobra.Command {
	common := &Common{}
	var direct bool
	command := &cobra.Command{
		Use:   "user-groups USER",
		Short: "List the Entra groups a user belongs to.",
		Long:  "List the Entra groups a user belongs to. " + userArgHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := entraClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.GetUser(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			groups, err := client.UserGroups(rt.Ctx(), found.ID, !direct)
			if err != nil {
				return err
			}
			if output == render.Table {
				rt.Console.Println(title(rt, fmt.Sprintf("%s  object %s  %d group(s)", found.UserPrincipalName, found.ID, len(groups))))
			}
			return rt.Console.Emit(output, []string{"GROUP", "OBJECT ID", "MEMBERSHIP", "SECURITY"}, groupRows(groups),
				map[string]any{"user": found.Raw, "groups": groupRecords(groups)})
		},
	}
	command.Flags().BoolVar(&direct, "direct", false, directHelp)
	common.AddOutput(command, true)
	return command
}

func entraUserRoles(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "user-roles USER",
		Short: "List the directory roles a user holds now, and those PIM makes them eligible for.",
		Long:  "List the directory roles a user holds now, and those PIM makes them eligible for. " + userArgHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := entraClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.GetUser(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			report, err := client.UserRoles(rt.Ctx(), found.ID)
			if err != nil {
				return err
			}
			if output == render.Table {
				rt.Console.Println(title(rt, fmt.Sprintf("%s  object %s", found.UserPrincipalName, found.ID)))
			}
			if err := rt.Console.Emit(output, []string{"ROLE", "STATE", "SCOPE", "ENDS"}, roleRows(rt, report), roleRecord(found, report)); err != nil {
				return err
			}
			if report.Eligible == nil {
				rt.Console.Warn("PIM eligibility could not be read: %s", report.EligibleError)
			}
			return nil
		},
	}
	common.AddOutput(command, true)
	return command
}

func roleRows(rt *Runtime, report entra.RoleReport) [][]render.Cell {
	var rows [][]render.Cell
	for _, role := range append(append([]entra.RoleAssignment(nil), report.Active...), report.Eligible...) {
		state := render.Coloured("eligible", "yellow")
		if role.State == "active" {
			state = render.Coloured("active", "green")
		}
		ends := "permanent"
		if !role.Ends.IsZero() {
			ends = rt.Console.When(role.Ends)
		}
		rows = append(rows, []render.Cell{render.Plain(role.RoleName), state, render.Plain(role.Scope), render.Plain(ends)})
	}
	return rows
}

func roleRecord(user entra.User, report entra.RoleReport) map[string]any {
	raw := func(roles []entra.RoleAssignment) []fields.Object {
		found := []fields.Object{}
		for _, role := range roles {
			found = append(found, role.Raw)
		}
		return found
	}
	var eligible, problem any
	if report.Eligible != nil {
		eligible = raw(report.Eligible)
	}
	if report.EligibleError != "" {
		problem = report.EligibleError
	}
	return map[string]any{"user": user.Raw, "active": raw(report.Active), "eligible": eligible, "eligible_error": problem}
}

func entraSignIns(rt *Runtime) *cobra.Command {
	common := &Common{}
	var user, since string
	var failures bool
	var limit int
	command := &cobra.Command{
		Use:   "sign-ins",
		Short: "List recent sign-ins, newest first. Needs Entra ID P1.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			window, err := Duration("--since", since)
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
			events, err := client.SignIns(rt.Ctx(), entra.SignInQuery{User: user, Since: rt.Clock().Add(-window),
				FailuresOnly: failures, Limit: limit})
			if err != nil {
				return err
			}
			return showSignIns(rt, output, events, window)
		},
	}
	command.Flags().StringVarP(&user, "user", "u", "", "Only this user (UPN or object id).")
	command.Flags().StringVar(&since, "since", "24h", "How far back to look, e.g. 1h, 24h, 7d.")
	command.Flags().BoolVar(&failures, "failures", false, "Only failed sign-ins.")
	command.Flags().IntVar(&limit, "limit", 50, "Most sign-ins to show.")
	common.AddOutput(command, true)
	return command
}

func showSignIns(rt *Runtime, output render.Output, events []entra.SignIn, window time.Duration) error {
	rows := make([][]render.Cell, len(events))
	records := []fields.Object{}
	for index, event := range events {
		result := render.Coloured("success", "green")
		if !event.Succeeded() {
			result = render.Coloured(fmt.Sprintf("%d %s", event.ErrorCode, event.FailureReason), "red")
		}
		rows[index] = []render.Cell{render.Plain(rt.Console.When(event.Created)), render.Plain(event.User), render.Plain(event.App),
			result, render.Plain(event.IPAddress), render.Plain(event.Location), render.Plain(event.DeviceName),
			render.Plain(event.ConditionalAccess)}
		records = append(records, event.Raw)
	}
	if err := rt.Console.Emit(output, []string{"WHEN", "USER", "APP", "RESULT", "IP", "LOCATION", "DEVICE", "CA"}, rows, records); err != nil {
		return err
	}
	if output == render.Table {
		rt.Console.Note("%d sign-in(s) in the last %s", len(events), util.FormatSpan(window))
	}
	return nil
}
