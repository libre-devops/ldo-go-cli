package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/keyvault"
)

// keyvaultCommand is the keyvault group.
func keyvaultCommand(rt *Runtime) *cobra.Command {
	group := newGroup("keyvault", "Key Vault: expiry of secrets, certificates and keys.")
	group.AddCommand(keyvaultExpiry(rt))
	return group
}

func keyvaultExpiry(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	var within string
	var kinds []string
	var includeDisabled bool
	command := &cobra.Command{
		Use:   "expiry [VAULTS]...",
		Short: "List secrets, certificates and keys that expire soon, or already have, in the vaults named.",
		Long: "List secrets, certificates and keys that expire soon, or already have, in the vaults named.\n\n" +
			"Name the vaults you look after: it has no way to search every vault, since a request to each one you " +
			"cannot read is refused and logged, which Defender for Key Vault can take for reconnaissance. Exits 3 " +
			"when anything is expiring, so a scheduled job can alert; exits 1 when no item is expiring but a vault " +
			"could not be read.",
		RunE: func(_ *cobra.Command, args []string) error {
			window, err := Duration("--within", within)
			if err != nil {
				return err
			}
			for _, kind := range kinds {
				if !slices.Contains(keyvault.Kinds, kind) {
					return Usagef("--kind", "--kind must be one of %s", strings.Join(keyvault.Kinds, ", "))
				}
			}
			if len(kinds) == 0 {
				kinds = keyvault.Kinds
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			vaults, err := names.Names(rt, args, "vaults")
			if err != nil {
				return err
			}
			items, failed, err := readVaults(rt, profile, vaults, kinds)
			if err != nil {
				return err
			}
			now := rt.Clock()
			shown := keyvault.Expiring(items, window, now, true, includeDisabled)
			return showExpiry(rt, output, shown, len(items), len(vaults), failed, window, now)
		},
	}
	names.Add(command)
	command.Flags().StringVar(&within, "within", "30d", "Show items expiring within this, e.g. 30d.")
	command.Flags().StringArrayVar(&kinds, "kind", nil, "Only this kind: secret, certificate or key. Repeatable.")
	command.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include disabled items.")
	common.AddOutput(command, true)
	return command
}

// readVaults is every item of the kinds in each vault, and how many vaults could not be
// read (each warned about, so one refused vault does not hide the rest).
func readVaults(rt *Runtime, profile microsoft.Profile, vaults, kinds []string) ([]keyvault.Item, int, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, 0, err
	}
	var items []keyvault.Item
	failed := 0
	for _, name := range vaults {
		client, err := keyvault.New(api, name)
		if err != nil {
			return nil, 0, err
		}
		found, err := client.Items(rt.Ctx(), kinds)
		if problem := errs.As(err); problem != nil && problem.Kind == errs.API {
			failed++
			rt.Console.Warn("cannot read vault %s: %s", name, problem.Message)
			if problem.Hint != "" {
				rt.Console.Note("hint: %s", problem.Hint)
			}
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		items = append(items, found...)
	}
	return items, failed, nil
}

func showExpiry(rt *Runtime, output render.Output, shown []keyvault.Item, checked, vaults, failed int, window time.Duration, now time.Time) error {
	rows := make([][]render.Cell, len(shown))
	records := []any{}
	for index, item := range shown {
		left := render.Plain("-")
		var days any
		if count, ok := item.DaysLeft(now); ok {
			days = count
			left = render.Coloured(fmt.Sprint(count), "yellow")
			if count < 0 {
				left = render.Coloured(fmt.Sprintf("expired %dd ago", -count), "red")
			}
		}
		var enabled any
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		rows[index] = []render.Cell{render.Plain(item.Vault), render.Plain(item.Kind), render.Plain(item.Name),
			render.Plain(rt.Console.When(item.Expires)), left, render.Plain(render.YesNo(item.Enabled)), render.Plain(item.ContentType)}
		records = append(records, map[string]any{"vault": item.Vault, "kind": item.Kind, "name": item.Name, "expires": render.ISO(item.Expires),
			"days_left": days, "enabled": enabled, "attributes": item.Raw["attributes"]})
	}
	if err := rt.Console.Emit(output, []string{"VAULT", "KIND", "NAME", "EXPIRES", "DAYS LEFT", "ENABLED", "CONTENT TYPE"}, rows, records); err != nil {
		return err
	}
	read := vaults - failed
	rt.Console.Note("%d item(s) expire within %s (%d checked in %d of %d vault(s))", len(shown), util.FormatSpan(window), checked, read, vaults)
	if failed > 0 && read == 0 {
		rt.Console.Warn("none of the %d vault(s) could be read: see why above", vaults)
	}
	switch {
	case len(shown) > 0:
		return Attention
	case failed > 0:
		return &ExitStatus{Code: ExitError}
	}
	return nil
}
