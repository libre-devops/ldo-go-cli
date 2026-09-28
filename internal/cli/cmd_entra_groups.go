package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
)

func entraGroupDevices(rt *Runtime) *cobra.Command {
	common := &Common{}
	var direct bool
	command := &cobra.Command{
		Use:   "group-devices GROUP",
		Short: "List the devices in an Entra group.",
		Long:  "List the devices in an Entra group. GROUP is its display name or object id.",
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
			found, err := client.GetGroup(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			devices, err := client.GroupDevices(rt.Ctx(), found.ID, !direct)
			if err != nil {
				return err
			}
			if output == render.Table {
				membership := "assigned"
				if found.Dynamic {
					membership = "dynamic"
				}
				rt.Console.Println(title(rt, fmt.Sprintf("%s  object %s  %s  %d device(s)", found.DisplayName, found.ID, membership, len(devices))))
			}
			rows := make([][]render.Cell, len(devices))
			records := []fields.Object{}
			for index, device := range devices {
				rows[index] = render.Cells(device.DisplayName, device.OperatingSystem, device.OSVersion, render.YesNo(device.Enabled),
					device.TrustType, rt.Console.When(device.LastSignIn), device.DeviceID)
				records = append(records, device.Raw)
			}
			return rt.Console.Emit(output, []string{"DEVICE", "OS", "VERSION", "ENABLED", "TRUST", "LAST SIGN-IN", "DEVICE ID"}, rows,
				map[string]any{"group": found.Raw, "devices": records})
		},
	}
	command.Flags().BoolVar(&direct, "direct", false, directHelp)
	common.AddOutput(command, true)
	return command
}

func entraGroupMembers(rt *Runtime) *cobra.Command {
	common := &Common{}
	var direct bool
	var kind string
	command := &cobra.Command{
		Use:   "group-members GROUP",
		Short: "List the members of an Entra group, of every kind or of one.",
		Long:  "List the members of an Entra group, of every kind or of one. GROUP is its display name or object id.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			wanted := ""
			if kind != "" {
				for _, known := range entra.MemberKinds {
					if strings.EqualFold(kind, known) {
						wanted = known
					}
				}
				if wanted == "" {
					return Usagef("--kind", "--kind must be one of %s", strings.Join(entra.MemberKinds, ", "))
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
			found, err := client.GetGroup(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			members, err := client.GroupMembers(rt.Ctx(), found.ID, !direct, wanted)
			if err != nil {
				return err
			}
			if output == render.Table {
				rt.Console.Println(title(rt, fmt.Sprintf("%s  %d member(s)", found.DisplayName, len(members))))
			}
			rows := make([][]render.Cell, len(members))
			records := []fields.Object{}
			for index, member := range members {
				rows[index] = render.Cells(member.Kind, member.DisplayName, member.Detail, member.ID)
				records = append(records, member.Raw)
			}
			return rt.Console.Emit(output, []string{"KIND", "NAME", "DETAIL", "OBJECT ID"}, rows,
				map[string]any{"group": found.Raw, "members": records})
		},
	}
	command.Flags().StringVar(&kind, "kind", "", "Only this kind: user, device, group or servicePrincipal.")
	command.Flags().BoolVar(&direct, "direct", false, directHelp)
	common.AddOutput(command, true)
	return command
}
