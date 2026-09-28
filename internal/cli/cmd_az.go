package cli

import (
	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/azcli"
)

// azCommand is the az group: the Azure CLI's context.
func azCommand(rt *Runtime) *cobra.Command {
	group := newGroup("az", "Azure CLI context: switch profiles, show the active account.")
	group.AddCommand(azUse(rt), azWhoami(rt))
	return group
}

func azUse(rt *Runtime) *cobra.Command {
	var deviceCode, noLogin bool
	command := &cobra.Command{
		Use:   "use PROFILE",
		Short: "Switch the Azure CLI's active account to a profile, signing in when needed.",
		Long: "Switch the Azure CLI's active account to a profile, signing in when needed.\n\n" +
			"This changes the az context for every shell. The other commands do not need it: they pass the profile's " +
			"tenant to az explicitly.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ms, err := rt.Microsoft()
			if err != nil {
				return err
			}
			profile, err := ms.Get(args[0])
			if err != nil {
				return err
			}
			switched, err := rt.Az().Switch(rt.Ctx(), profile, !noLogin, deviceCode)
			if err != nil {
				return err
			}
			account := switched.Account
			target := account.Name + " (" + account.ID + ")"
			if account.TenantLevel() {
				target = "tenant-level account"
			}
			rt.Console.Println("Switched to " + args[0] + ": " + target + " in tenant " + account.TenantID + " as " + or(account.User, "unknown user"))
			return nil
		},
	}
	command.Flags().BoolVar(&deviceCode, "device-code", false, "Sign in with a device code, not a browser.")
	command.Flags().BoolVar(&noLogin, "no-login", false, "Fail instead of prompting when not signed in.")
	return command
}

func azWhoami(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Show the Azure CLI's active account and the profile it matches.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			account, found, err := rt.Az().Current(rt.Ctx())
			if err != nil {
				return err
			}
			if !found {
				return errs.Commandf("the Azure CLI is not signed in").WithHint("run %s", brand.Suggest("az use <profile>"))
			}
			ms, err := rt.OptionalMicrosoft()
			if err != nil {
				return err
			}
			return showAzAccount(rt, output, account, ms)
		},
	}
	common.AddOutput(command, false)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func showAzAccount(rt *Runtime, output render.Output, account azcli.Account, ms *microsoft.Config) error {
	var profile, subscription, subscriptionName any
	if ms != nil {
		var profiles []microsoft.Profile
		for _, name := range ms.Names() {
			profiles = append(profiles, ms.Profiles[name])
		}
		if matched, ok := azcli.MatchProfile(profiles, account); ok {
			profile = matched.Name
		}
	}
	shown := "tenant-level account"
	if !account.TenantLevel() {
		subscription, subscriptionName = account.ID, account.Name
		shown = account.Name + " (" + account.ID + ")"
	}
	if output != render.Table {
		record := map[string]any{"profile": profile, "tenant_id": account.TenantID, "subscription_id": subscription,
			"subscription_name": subscriptionName, "user": account.User, "state": account.State}
		headers := []string{"profile", "tenant_id", "subscription_id", "subscription_name", "user", "state"}
		row := make([]render.Cell, len(headers))
		for index, header := range headers {
			text, _ := record[header].(string)
			row[index] = render.Plain(text)
		}
		return rt.Console.Emit(output, headers, [][]render.Cell{row}, record)
	}
	name, _ := profile.(string)
	rt.Console.Println(pairs(rt, [][2]string{{"Profile", or(name, "(no matching profile)")}, {"Tenant", account.TenantID},
		{"Subscription", shown}, {"User", account.User}, {"State", account.State}}))
	return nil
}
