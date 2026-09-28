package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/timewindow"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/graph"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

func xdrHunt(rt *Runtime) *cobra.Command {
	common := &Common{}
	var file, timespan string
	var endpoint bool
	command := &cobra.Command{
		Use:   "hunt [QUERY]",
		Short: "Run an Advanced Hunting (KQL) query over Defender XDR.",
		Long: "Run an Advanced Hunting (KQL) query over Defender XDR.\n\n" +
			"Through Microsoft Graph, which covers every Defender XDR table: devices, email, identity, cloud apps " +
			"and alerts. It needs ThreatHunting.Read.All, which the Azure CLI's token never has: use an interactive " +
			"or device-code profile whose app has it, or --endpoint for the device tables with the Azure CLI's " +
			"sign-in. Omit the query, or pass -, to read it from stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			text, err := readQuery(rt, query, file)
			if err != nil {
				return err
			}
			if !endpoint {
				return runGraphHunt(rt, common, text, timespan)
			}
			if timespan != "" {
				return Usagef("--timespan", "--timespan is not available with --endpoint; put it in the query")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			result, err := client.Hunt(rt.Ctx(), text)
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
	command.Flags().BoolVar(&endpoint, "endpoint", false, endpointHelp)
	command.Flags().StringVar(&timespan, "timespan", "", "How far back the data goes, e.g. 7d. Default: 30 days.")
	common.AddOutput(command, true)
	return command
}

func xdrTimeline(rt *Runtime) *cobra.Command {
	common := &Common{}
	window := &TimeFlags{}
	var kinds []string
	var limit int
	var endpoint, showQuery bool
	command := &cobra.Command{
		Use:   "timeline DEVICE",
		Short: "A device's timeline: its events, newest first, from Advanced Hunting.",
		Long: "A device's timeline: its events, newest first, from Advanced Hunting.\n\n" +
			"Defender has no API for the portal's device timeline, so this asks Advanced Hunting's device tables for " +
			"the same events: processes, network connections, files, registry, logons, image loads, other device " +
			"events and alerts. Advanced Hunting keeps 30 days; the portal reaches further back. Through Graph like " +
			"'xdr hunt', or --endpoint, which has the device tables but not the alert ones, so no alerts. The " +
			"default is the last 24 hours and the newest 1000 events. Times show in local time; -o json has them in UTC.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("limit") && (limit < 1 || limit > xdr.MaxEvents) {
				return Usagef("--limit", "%d is not in the range 1<=x<=100000", limit)
			}
			span, err := window.Window(rt, func(now time.Time) timewindow.Window { return timewindow.Last(24*time.Hour, now) })
			if err != nil {
				return err
			}
			chosen, err := xdr.ParseKinds(kinds, endpoint)
			if err != nil {
				return err
			}
			query, err := xdr.TimelineQuery(args[0], span, chosen, limit)
			if err != nil {
				return err
			}
			if showQuery {
				rt.Console.Println(query)
				return nil
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			return runTimeline(rt, common, args[0], query, span, limit, endpoint, len(kinds) == 0, output)
		},
	}
	command.Flags().StringArrayVar(&kinds, "type", nil, "Only these kinds of event: process, network, file, registry, logon, "+
		"image-load, other or alert. Comma-separated or repeated. Default: all.")
	window.Add(command)
	command.Flags().IntVar(&limit, "limit", xdr.DefaultLimit, "At most this many, the newest.")
	command.Flags().BoolVar(&endpoint, "endpoint", false, endpointHelp)
	command.Flags().BoolVar(&showQuery, "show-query", false, "Print the KQL instead of running it.")
	common.AddOutput(command, true)
	return command
}

func runTimeline(rt *Runtime, common *Common, device, query string, span timewindow.Window, limit int, endpoint, allKinds bool,
	output render.Output) error {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return err
	}
	var result render.QueryResult
	if endpoint {
		client, _, err := xdrClientFor(rt, profile)
		if err != nil {
			return err
		}
		result, err = client.Hunt(rt.Ctx(), query)
		if err != nil {
			return err
		}
	} else {
		api, err := rt.API(profile)
		if err != nil {
			return err
		}
		client, err := graph.New(api)
		if err != nil {
			return err
		}
		if result, err = client.Hunt(rt.Ctx(), query, 0); err != nil {
			return err
		}
	}
	found := xdr.ReadTimeline(device, result, limit)
	rows := make([][]render.Cell, len(found.Events))
	records := []any{}
	for index, event := range found.Events {
		kind := render.Plain(event.Kind)
		if event.Kind == "alert" {
			kind = render.Coloured(event.Kind, "red")
		}
		rows[index] = []render.Cell{render.Plain(render.Moment(event.Time)), kind, render.Plain(event.Action), render.Plain(event.Account),
			render.Plain(event.Process), render.Plain(event.Detail)}
		records = append(records, map[string]any{"time": render.ISO(event.Time), "type": event.Kind, "action": event.Action,
			"detail": event.Detail, "account": event.Account, "process": event.Process, "device_name": event.DeviceName,
			"device_id": event.DeviceID, "id": event.ID})
	}
	if err := rt.Console.Emit(output, []string{"TIME", "TYPE", "ACTION", "ACCOUNT", "PROCESS", "DETAIL"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%d event(s) on %s, %s (profile %s)", len(found.Events), device, span.Label, profile.Name)
	if endpoint && allKinds {
		rt.Console.Note("alerts are left out: the Defender for Endpoint API has no alert tables")
	}
	timelineWarnings(rt, found, span)
	return nil
}

// timelineWarnings are what the person should know about what came back: a cut-off, a
// shared name, and time outside what Advanced Hunting keeps.
func timelineWarnings(rt *Runtime, found xdr.Timeline, span timewindow.Window) {
	if found.Truncated() {
		rt.Console.Warn("stopped at the newest %d: narrow the window or --type, or raise --limit", found.Limit)
	}
	if devices := found.Devices(); len(devices) > 1 {
		rt.Console.Warn("%d devices have that name: %s; -o json tells their events apart", len(devices), strings.Join(devices, ", "))
	}
	if xdr.OutsideRetention(span, rt.Clock()) {
		rt.Console.Warn("Advanced Hunting keeps 30 days of device events, so nothing older is here (the portal's timeline reaches further back)")
	}
}
