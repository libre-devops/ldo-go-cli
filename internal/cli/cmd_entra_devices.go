package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
)

// entraClient is an Entra client for a profile, by name.
func entraClient(rt *Runtime, name string) (*entra.Client, microsoft.Profile, error) {
	profile, err := rt.Profile(name)
	if err != nil {
		return nil, profile, err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, profile, err
	}
	client, err := entra.New(api)
	return client, profile, err
}

const directHelp = "Direct memberships only. Default includes nested groups."

func entraDevices(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	var groupRefs []string
	var direct bool
	var workers int
	command := &cobra.Command{
		Use:   "devices [NAMES]...",
		Short: "Look devices up in Entra ID, and check they are in the groups you name.",
		Long: "Look devices up in Entra ID, and check they are in the groups you name.\n\n" +
			"Each device is looked up by FQDN, then by short hostname, and every registration with the name is " +
			"shown. --group takes a group's object id or its display name (a name two groups share is refused), " +
			"and counts nested membership unless --direct. Exits 3 when a device is not in Entra, or not in " +
			`every group. NAMES are "a,b,c", several arguments, or - to read stdin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("workers") && (workers < 1 || workers > 32) {
				return Usagef("--workers", "%d is not in the range 1<=x<=32", workers)
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			client, profile, err := entraClient(rt, common.Profile)
			if err != nil {
				return err
			}
			var groups []entra.Group
			for _, ref := range groupRefs {
				group, err := client.GetGroup(rt.Ctx(), ref)
				if err != nil {
					return err
				}
				groups = append(groups, group)
			}
			lookups, err := client.LookUpDevices(rt.Ctx(), wanted, groups, !direct, workers)
			if err != nil {
				return err
			}
			return showLookups(rt, output, profile, groups, lookups)
		},
	}
	names.Add(command)
	command.Flags().StringArrayVar(&groupRefs, "group", nil, "Also check each device is in this Entra group: its object id or display name. Repeatable.")
	command.Flags().BoolVar(&direct, "direct", false, directHelp)
	command.Flags().IntVar(&workers, "workers", 8, "Devices looked up at once.")
	common.AddOutput(command, true)
	return command
}

func showLookups(rt *Runtime, output render.Output, profile microsoft.Profile, groups []entra.Group, lookups []entra.DeviceLookup) error {
	headers := []string{"DEVICE", "NAME", "OS", "ENABLED", "TRUST", "LAST SIGN-IN", "DEVICE ID"}
	for _, group := range groups {
		headers = append(headers, "IN "+group.DisplayName)
	}
	var rows [][]render.Cell
	records := []any{}
	for _, lookup := range lookups {
		rows = append(rows, lookupRows(rt, lookup)...)
		records = append(records, lookupRecord(lookup))
	}
	if err := rt.Console.Emit(output, headers, rows, records); err != nil {
		return err
	}
	found, inEvery := 0, 0
	for _, lookup := range lookups {
		if lookup.Found() {
			found++
			if lookup.InEveryGroup() {
				inEvery++
			}
		}
	}
	note := fmt.Sprintf("%d of %d in Entra", found, len(lookups))
	if len(groups) > 0 {
		note += fmt.Sprintf(", %d in every group", inEvery)
	}
	rt.Console.Note("%s (profile %s)", note, profile.Name)
	if found < len(lookups) || inEvery < found {
		return Attention
	}
	return nil
}

// lookupRows is a row for each device with the name, the most recently signed in first
// (the rest are marked as older records), or one row saying there is none.
func lookupRows(rt *Runtime, lookup entra.DeviceLookup) [][]render.Cell {
	if !lookup.Found() {
		row := []render.Cell{render.Plain(lookup.Query), render.Coloured("not in Entra", "red")}
		for range 5 + len(lookup.Groups) {
			row = append(row, render.Plain(""))
		}
		return [][]render.Cell{row}
	}
	var rows [][]render.Cell
	for index, device := range lookup.Devices {
		first := render.Plain(lookup.Query)
		if index > 0 {
			first = render.Coloured("  older record", "bright_black")
		}
		row := []render.Cell{first, render.Plain(device.DisplayName),
			render.Plain(strings.TrimSpace(device.OperatingSystem + " " + device.OSVersion)),
			render.Plain(render.YesNo(device.Enabled)), render.Plain(device.TrustType),
			render.Plain(rt.Console.When(device.LastSignIn)), render.Plain(device.DeviceID)}
		for _, group := range lookup.Groups {
			if lookup.InGroup(group, &device) {
				row = append(row, render.Coloured("yes", "green"))
			} else {
				row = append(row, render.Coloured("no", "yellow"))
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func lookupRecord(lookup entra.DeviceLookup) map[string]any {
	groups := []any{}
	for _, group := range lookup.Groups {
		groups = append(groups, map[string]any{"id": group.ID, "name": group.DisplayName, "member": lookup.InGroup(group, nil)})
	}
	devices := []any{}
	for _, device := range lookup.Devices {
		devices = append(devices, device.Raw)
	}
	return map[string]any{"query": lookup.Query, "found": lookup.Found(), "groups": groups, "devices": devices}
}

func entraDeviceGroups(rt *Runtime) *cobra.Command {
	common := &Common{}
	var direct bool
	command := &cobra.Command{
		Use:   "device-groups DEVICE",
		Short: "List the Entra groups a device belongs to.",
		Long:  "List the Entra groups a device belongs to. DEVICE is its FQDN or short hostname.",
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
			devices, err := client.FindDevices(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if len(devices) == 0 {
				var tried []string
				for _, name := range util.CandidateNames(args[0]) {
					tried = append(tried, "'"+name+"'")
				}
				return errs.NotFoundf("no Entra device is named %s", strings.Join(tried, " or "))
			}
			results := make([][]entra.Group, len(devices))
			for index, device := range devices {
				if results[index], err = client.DeviceGroups(rt.Ctx(), device.ID, !direct); err != nil {
					return err
				}
			}
			return showDeviceGroups(rt, output, devices, results)
		},
	}
	command.Flags().BoolVar(&direct, "direct", false, directHelp)
	common.AddOutput(command, false)
	return command
}

func showDeviceGroups(rt *Runtime, output render.Output, devices []entra.Device, results [][]entra.Group) error {
	if output != render.Table {
		var rows [][]render.Cell
		records := []any{}
		for index, device := range devices {
			for _, row := range groupRows(results[index]) {
				rows = append(rows, append([]render.Cell{render.Plain(device.DisplayName), render.Plain(device.ID)}, row...))
			}
			records = append(records, map[string]any{"device": device.Raw, "groups": groupRecords(results[index])})
		}
		return rt.Console.Emit(output, []string{"DEVICE", "DEVICE OBJECT ID", "GROUP", "GROUP OBJECT ID", "MEMBERSHIP", "SECURITY"},
			rows, records)
	}
	if len(devices) > 1 {
		rt.Console.Warn("%d Entra devices are named '%s' (stale registrations keep the name)", len(devices), devices[0].DisplayName)
	}
	for index, device := range devices {
		rt.Console.Println(title(rt, fmt.Sprintf("%s  object %s  %s %s  last sign-in %s", device.DisplayName, device.ID,
			device.OperatingSystem, device.OSVersion, rt.Console.When(device.LastSignIn))))
		if len(results[index]) > 0 {
			rt.Console.Println(rt.Console.Table([]string{"GROUP", "OBJECT ID", "MEMBERSHIP", "SECURITY"}, groupRows(results[index])))
		} else {
			rt.Console.Println("(no group memberships)")
		}
		rt.Console.Println("")
	}
	return nil
}

// groupRows are table rows for groups (GROUP, OBJECT ID, MEMBERSHIP, SECURITY), shared by
// the commands that list a device's or a user's groups.
func groupRows(groups []entra.Group) [][]render.Cell {
	rows := make([][]render.Cell, len(groups))
	for index, group := range groups {
		membership := "assigned"
		if group.Dynamic {
			membership = "dynamic"
		}
		rows[index] = render.Cells(group.DisplayName, group.ID, membership, render.YesNo(group.SecurityEnabled))
	}
	return rows
}

func groupRecords(groups []entra.Group) []fields.Object {
	records := []fields.Object{}
	for _, group := range groups {
		records = append(records, group.Raw)
	}
	return records
}
