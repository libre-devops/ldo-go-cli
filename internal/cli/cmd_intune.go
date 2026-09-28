package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
)

// intuneCommand is the intune group.
func intuneCommand(rt *Runtime) *cobra.Command {
	group := newGroup("intune", "Intune: managed devices and compliance.")
	group.AddCommand(intuneDevices(rt))
	return group
}

func intuneDevices(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	command := &cobra.Command{
		Use:   "devices [NAMES]...",
		Short: "Look devices up in Intune: compliance, last sync, owner and Entra link.",
		Long: "Look devices up in Intune: compliance, last sync, owner and Entra link.\n\n" +
			"Needs DeviceManagementManagedDevices.Read.All, which the Azure CLI's token does not carry: use a profile " +
			"with its own app registration. Exits 3 when any device is not enrolled.",
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			api, err := rt.API(profile)
			if err != nil {
				return err
			}
			client, err := intune.New(api)
			if err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			missing := 0
			for _, name := range wanted {
				found, err := client.FindDevices(rt.Ctx(), name)
				if err != nil {
					return err
				}
				rows = append(rows, intuneRows(rt, name, found)...)
				raw := []fields.Object{}
				for _, device := range found {
					raw = append(raw, device.Raw)
				}
				records = append(records, map[string]any{"query": name, "devices": raw})
				if len(found) == 0 {
					missing++
				}
			}
			if err := rt.Console.Emit(output, []string{"DEVICE", "COMPLIANCE", "OS", "LAST SYNC", "USER", "ENTRA DEVICE ID", "SERIAL"},
				rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d of %d enrolled in Intune", len(wanted)-missing, len(wanted))
			if missing > 0 {
				return Attention
			}
			return nil
		},
	}
	names.Add(command)
	common.AddOutput(command, true)
	return command
}

func intuneRows(rt *Runtime, name string, found []intune.ManagedDevice) [][]render.Cell {
	if len(found) == 0 {
		return [][]render.Cell{{render.Plain(name), render.Coloured("not enrolled", "red"), render.Plain(""), render.Plain(""),
			render.Plain(""), render.Plain(""), render.Plain("")}}
	}
	var rows [][]render.Cell
	for index, device := range found {
		first := render.Plain(name)
		if index > 0 {
			first = render.Coloured("  older record", "bright_black")
		}
		compliance := "yellow"
		if device.Compliant() {
			compliance = "green"
		}
		rows = append(rows, []render.Cell{first, render.Coloured(device.ComplianceState, compliance),
			render.Plain(strings.TrimSpace(device.OperatingSystem + " " + device.OSVersion)), render.Plain(rt.Console.When(device.LastSync)),
			render.Plain(device.UserPrincipalName), render.Plain(device.AzureADDeviceID), render.Plain(device.SerialNumber)})
	}
	return rows
}
