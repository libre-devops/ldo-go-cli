package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

func xdrMachines(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	var allRecords bool
	command := &cobra.Command{
		Use:   "machines [NAMES]...",
		Short: "Look devices up in Defender: onboarding, health, last seen, tags and device group.",
		Long: "Look devices up in Defender: onboarding, health, last seen, tags and device group.\n\n" +
			"Each device is looked up by FQDN, then by short hostname. Exits 3 when any device has no Defender record.",
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			client, profile, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			lookups, err := client.FindMachines(rt.Ctx(), wanted)
			if err != nil {
				return err
			}
			records := []any{}
			missing := 0
			for _, lookup := range lookups {
				raw := []fields.Object{}
				for _, record := range lookup.Records {
					raw = append(raw, record.Raw)
				}
				var matched any
				if lookup.MatchedName != "" {
					matched = lookup.MatchedName
				}
				records = append(records, map[string]any{"query": lookup.Query, "matched_name": matched, "found": lookup.Found(), "records": raw})
				if !lookup.Found() {
					missing++
				}
			}
			if err := rt.Console.Emit(output, []string{"DEVICE", "MATCHED", "ONBOARDING", "HEALTH", "LAST SEEN", "OS", "TAGS",
				"DEVICE GROUP", "RECORDS", "MACHINE ID"}, machineRows(rt, lookups, allRecords), records); err != nil {
				return err
			}
			rt.Console.Note("%d of %d found in Defender (profile %s)", len(lookups)-missing, len(lookups), profile.Name)
			if missing > 0 {
				return Attention
			}
			return nil
		},
	}
	names.Add(command)
	command.Flags().BoolVar(&allRecords, "all-records", false, "Also list older duplicate Defender records.")
	common.AddOutput(command, true)
	return command
}

func machineRows(rt *Runtime, lookups []xdr.MachineLookup, allRecords bool) [][]render.Cell {
	var rows [][]render.Cell
	for _, lookup := range lookups {
		newest := lookup.Machine()
		if newest == nil {
			rows = append(rows, []render.Cell{render.Plain(lookup.Query), render.Coloured("not found", "red"), render.Plain(""),
				render.Plain(""), render.Plain(""), render.Plain(""), render.Plain(""), render.Plain(""), render.Plain("0"), render.Plain("")})
			continue
		}
		query := strings.TrimRight(strings.TrimSpace(lookup.Query), ".")
		matched := "prefix" // a short name, found as the first label of Defender's FQDN
		switch lookup.MatchedName {
		case query:
			matched = "fqdn"
		case util.ShortName(query):
			matched = "short"
		}
		row := append([]render.Cell{render.Plain(lookup.Query), render.Plain(matched)}, machineCells(rt, *newest)...)
		rows = append(rows, append(row, render.Plain(fmt.Sprint(len(lookup.Records))), render.Plain(newest.ID)))
		if allRecords {
			for _, older := range lookup.Records[1:] {
				row := append([]render.Cell{render.Coloured("  older record", "bright_black"), render.Plain("")}, machineCells(rt, older)...)
				rows = append(rows, append(row, render.Plain(""), render.Plain(older.ID)))
			}
		}
	}
	return rows
}

func machineCells(rt *Runtime, machine xdr.Machine) []render.Cell {
	onboarded := "yellow"
	if machine.OnboardingStatus == "Onboarded" {
		onboarded = "green"
	}
	health := map[string]string{"Active": "green", "Inactive": "yellow"}[machine.HealthStatus]
	if health == "" {
		health = "red"
	}
	return []render.Cell{render.Coloured(machine.OnboardingStatus, onboarded), render.Coloured(machine.HealthStatus, health),
		render.Plain(rt.Console.When(machine.LastSeen)), render.Plain(strings.TrimSpace(machine.OSPlatform + " " + machine.OSVersion)),
		render.Plain(strings.Join(machine.MachineTags, ",")), render.Plain(machine.DeviceGroup)}
}

func xdrStale(rt *Runtime) *cobra.Command {
	common := &Common{}
	var olderThan string
	command := &cobra.Command{
		Use:   "stale",
		Short: "List machines Defender has not seen recently, longest silent first.",
		Long:  "List machines Defender has not seen recently, longest silent first.\n\nExits 3 when any are found.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			window, err := Duration("--older-than", olderThan)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.StaleMachines(rt.Ctx(), window, rt.Clock())
			if err != nil {
				return err
			}
			return showStale(rt, output, found, window)
		},
	}
	command.Flags().StringVar(&olderThan, "older-than", "30d", "Not seen for at least this long, e.g. 30d.")
	common.AddOutput(command, true)
	return command
}

func showStale(rt *Runtime, output render.Output, found []xdr.Machine, window time.Duration) error {
	rows := make([][]render.Cell, len(found))
	records := []fields.Object{}
	for index, machine := range found {
		rows[index] = render.Cells(machine.ComputerDNSName, rt.Console.When(machine.LastSeen), machine.HealthStatus, machine.OnboardingStatus,
			strings.TrimSpace(machine.OSPlatform+" "+machine.OSVersion), strings.Join(machine.MachineTags, ","), machine.ID)
		records = append(records, machine.Raw)
	}
	if err := rt.Console.Emit(output, []string{"DEVICE", "LAST SEEN", "HEALTH", "ONBOARDING", "OS", "TAGS", "MACHINE ID"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%d machine(s) not seen for %s", len(found), util.FormatSpan(window))
	if len(found) > 0 {
		return Attention
	}
	return nil
}
