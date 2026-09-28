package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/timewindow"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/incidents"
)

// What people type for a status, and Graph's values.
var statusNames = []struct {
	name   string
	values []string
}{
	{"active", []string{"active"}}, {"in-progress", []string{"inProgress"}}, {"awaiting-action", []string{"awaitingAction"}},
	{"resolved", []string{"resolved"}}, {"redirected", []string{"redirected"}}, {"open", incidents.OpenStatuses}, {"all", nil},
}

// What people type for a source, and Graph's serviceSource.
var sourceNames = [][2]string{
	{"sentinel", "microsoftSentinel"}, {"endpoint", "microsoftDefenderForEndpoint"}, {"identity", "microsoftDefenderForIdentity"},
	{"office", "microsoftDefenderForOffice365"}, {"cloud-apps", "microsoftDefenderForCloudApps"}, {"cloud", "microsoftDefenderForCloud"},
	{"xdr", "microsoft365Defender"}, {"entra", "azureAdIdentityProtection"}, {"app-governance", "microsoftAppGovernance"},
	{"dlp", "dataLossPrevention"}, {"insider-risk", "microsoftInsiderRiskManagement"},
}

var incidentColours = map[string]string{"high": "red", "medium": "yellow", "low": "cyan"}

var statusLabels = map[string]string{"inProgress": "in progress", "awaitingAction": "awaiting action"}

func statusLabel(value string) string {
	if label, ok := statusLabels[value]; ok {
		return label
	}
	return value
}

func incidentSeverity(severity string) render.Cell {
	return render.Coloured(severity, incidentColours[strings.ToLower(severity)])
}

// incidentFlags are the filters every incident listing takes.
type incidentFlags struct {
	window   TimeFlags
	updated  bool
	status   []string
	severity string
	source   []string
	limit    int
}

func (f *incidentFlags) add(cmd *cobra.Command, limit int, withLimit bool) {
	f.window.Add(cmd)
	flags := cmd.Flags()
	flags.BoolVar(&f.updated, "updated", false, "Window on when incidents were last updated, not created.")
	flags.StringArrayVar(&f.status, "status", nil, "open, active, in-progress, awaiting-action, resolved, redirected or all. Repeatable.")
	flags.StringVar(&f.severity, "severity", "", "At least this severity: informational, low, medium, high.")
	var names []string
	for _, source := range sourceNames {
		names = append(names, source[0])
	}
	flags.StringArrayVar(&f.source, "source", nil, "Only incidents with alerts from here: "+strings.Join(names, ", ")+". Repeatable.")
	if withLimit {
		flags.IntVarP(&f.limit, "limit", "n", limit, "Most to show.")
	}
}

// query is the Graph query the flags name, in the window.
func (f *incidentFlags) query(window timewindow.Window, statuses []string) (incidents.Query, error) {
	if len(f.status) > 0 {
		statuses = f.status
	}
	query := incidents.Query{Start: window.Start, End: window.End, By: "createdDateTime"}
	if f.updated {
		query.By = "lastUpdateDateTime"
	}
	for _, name := range statuses {
		index := slices.IndexFunc(statusNames, func(item struct {
			name   string
			values []string
		}) bool {
			return item.name == strings.ToLower(strings.TrimSpace(name))
		})
		if index < 0 {
			return query, Usagef("--status", "--status must be one of active, in-progress, awaiting-action, resolved, redirected, open, all")
		}
		for _, value := range statusNames[index].values {
			if !slices.Contains(query.Statuses, value) {
				query.Statuses = append(query.Statuses, value)
			}
		}
	}
	for _, name := range f.source {
		index := slices.IndexFunc(sourceNames, func(item [2]string) bool { return item[0] == strings.ToLower(strings.TrimSpace(name)) })
		if index < 0 {
			var names []string
			for _, source := range sourceNames {
				names = append(names, source[0])
			}
			return query, Usagef("--source", "--source must be one of %s", strings.Join(names, ", "))
		}
		query.Sources = append(query.Sources, sourceNames[index][1])
	}
	if f.severity != "" {
		severities, err := incidents.SeveritiesFrom(f.severity)
		if err != nil {
			return query, err
		}
		query.Severities = severities
	}
	return query, nil
}

func incidentsClient(rt *Runtime, name string) (*incidents.Client, error) {
	profile, err := rt.Profile(name)
	if err != nil {
		return nil, err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	return incidents.New(api)
}

// incidentsCommand is xdr incidents: top, latest, list, summary, show.
func incidentsCommand(rt *Runtime) *cobra.Command {
	group := newGroup("incidents", "Defender XDR incidents, Sentinel's included: top, latest, list, summary, show.")
	last := func(span time.Duration) func(time.Time) timewindow.Window {
		return func(now time.Time) timewindow.Window { return timewindow.Last(span, now) }
	}
	group.AddCommand(
		incidentList(rt, "top", "The most severe open incidents, today unless told otherwise: 10 by default.", "",
			timewindow.Today, "open", 10, incidents.MostSevere),
		incidentList(rt, "latest", "The newest incidents of any status, from the last 30 days: 10 by default.", "",
			last(30*24*time.Hour), "all", 10, incidents.Newest),
		incidentList(rt, "list", "Every incident in a window, newest first: the last 24 hours by default.",
			"For between days, give --from and --to: ldo-go xdr incidents list --from 2026-09-01 --to 2026-09-24 takes both days whole.",
			last(24*time.Hour), "all", 0, incidents.Newest),
		incidentSummary(rt), incidentShow(rt))
	return group
}

func incidentList(rt *Runtime, use, short, long string, fallback func(time.Time) timewindow.Window, status string, limit int,
	order func([]incidents.Incident) []incidents.Incident) *cobra.Command {
	common := &Common{}
	flags := &incidentFlags{}
	command := &cobra.Command{
		Use: use, Short: short, Long: strings.TrimSpace(short + "\n\n" + long), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := positive(cmd, "limit", flags.limit); err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			window, err := flags.window.Window(rt, fallback)
			if err != nil {
				return err
			}
			query, err := flags.query(window, []string{status})
			if err != nil {
				return err
			}
			client, err := incidentsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Incidents(rt.Ctx(), query)
			if err != nil {
				return err
			}
			return showIncidents(rt, output, found, order(found.Incidents), flags, window)
		},
	}
	flags.add(command, limit, true)
	common.AddOutput(command, true)
	return command
}

func showIncidents(rt *Runtime, output render.Output, found incidents.List, ordered []incidents.Incident, flags *incidentFlags,
	window timewindow.Window) error {
	shown := ordered
	if flags.limit > 0 && len(shown) > flags.limit {
		shown = shown[:flags.limit]
	}
	rows := make([][]render.Cell, len(shown))
	records := []fields.Object{}
	for index, incident := range shown {
		rows[index] = []render.Cell{render.Plain(rt.Console.When(incident.Created)), incidentSeverity(incident.Severity),
			render.Plain(statusLabel(incident.Status)), render.Plain(incident.ID), render.Plain(incident.Title),
			render.Plain(strings.Join(incident.SourceNames(), ", ")), render.Plain(fmt.Sprint(len(incident.Alerts))),
			render.Plain(incident.AssignedTo)}
		records = append(records, incident.Raw)
	}
	if err := rt.Console.Emit(output, []string{"CREATED", "SEVERITY", "STATUS", "ID", "TITLE", "SOURCES", "ALERTS", "ASSIGNED"},
		rows, records); err != nil {
		return err
	}
	what := "created"
	if flags.updated {
		what = "updated"
	}
	rt.Console.Note("%d of %d incident(s) %s %s", len(shown), len(ordered), what, window.Label)
	warnTruncated(rt, found.Truncated)
	return nil
}

func warnTruncated(rt *Runtime, truncated bool) {
	if truncated {
		rt.Console.Warn("stopped at the incident limit; narrow the window to see them all")
	}
}

func incidentSummary(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &incidentFlags{}
	command := &cobra.Command{
		Use:   "summary",
		Short: "How many incidents, by severity, status and source: today by default.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			window, err := flags.window.Window(rt, timewindow.Today)
			if err != nil {
				return err
			}
			query, err := flags.query(window, []string{"all"})
			if err != nil {
				return err
			}
			client, err := incidentsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Incidents(rt.Ctx(), query)
			if err != nil {
				return err
			}
			return showSummary(rt, output, incidents.Summarise(found.Incidents), window, found.Truncated)
		},
	}
	flags.add(command, 0, false)
	common.AddOutput(command, false)
	return command
}

func countMap(counts []incidents.Count) map[string]int {
	found := map[string]int{}
	for _, count := range counts {
		found[count.Name] = count.Count
	}
	return found
}

func showSummary(rt *Runtime, output render.Output, counts incidents.Summary, window timewindow.Window, truncated bool) error {
	if output != render.Table {
		var rows [][]render.Cell
		for _, part := range []struct {
			by     string
			counts []incidents.Count
		}{{"severity", counts.BySeverity}, {"status", counts.ByStatus}, {"source", counts.BySource}} {
			for _, count := range part.counts {
				rows = append(rows, render.Cells(part.by, count.Name, fmt.Sprint(count.Count)))
			}
		}
		return rt.Console.Emit(output, []string{"BY", "VALUE", "INCIDENTS"}, rows, map[string]any{"window": window.Label,
			"total": counts.Total, "by_severity": countMap(counts.BySeverity), "by_status": countMap(counts.ByStatus),
			"by_source": countMap(counts.BySource)})
	}
	rt.Console.Println(title(rt, fmt.Sprintf("%d incident(s) %s", counts.Total, window.Label)))
	for _, part := range []struct {
		heading string
		counts  []incidents.Count
		colours map[string]string
		label   func(string) string
	}{{"SEVERITY", counts.BySeverity, incidentColours, func(name string) string { return name }},
		{"STATUS", counts.ByStatus, nil, statusLabel}, {"SOURCE", counts.BySource, nil, func(name string) string { return name }}} {
		if len(part.counts) == 0 {
			continue
		}
		rows := make([][]render.Cell, len(part.counts))
		for index, count := range part.counts {
			rows[index] = []render.Cell{render.Coloured(part.label(count.Name), part.colours[count.Name]), render.Plain(fmt.Sprint(count.Count))}
		}
		rt.Console.Println("")
		rt.Console.Println(rt.Console.Table([]string{part.heading, "INCIDENTS"}, rows))
	}
	warnTruncated(rt, truncated)
	return nil
}

func incidentShow(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "show ID",
		Short: "One incident: its alerts, where they came from, the devices and users involved.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, err := incidentsClient(rt, common.Profile)
			if err != nil {
				return err
			}
			incident, err := client.Incident(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if output == render.Table {
				printIncident(rt, incident)
				return nil
			}
			rows := make([][]render.Cell, len(incident.Alerts))
			for index, alert := range incident.Alerts {
				rows[index] = render.Cells(rt.Console.When(alert.Created), alert.Severity, alert.SourceName(), alert.Title, alert.ID)
			}
			return rt.Console.Emit(output, []string{"CREATED", "SEVERITY", "SOURCE", "TITLE", "ALERT ID"}, rows, incident.Raw)
		},
	}
	common.AddOutput(command, false)
	return command
}

func or(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}

// printIncident is the incident's facts, then a table of its alerts.
func printIncident(rt *Runtime, incident incidents.Incident) {
	rt.Console.Println(title(rt, "Incident "+incident.ID+": "+incident.Title))
	rt.Console.Println(pairs(rt, [][2]string{
		{"Severity", incident.Severity}, {"Status", statusLabel(incident.Status)},
		{"Created", rt.Console.When(incident.Created)}, {"Updated", rt.Console.When(incident.Updated)},
		{"Assigned to", or(incident.AssignedTo, "(nobody)")}, {"Classification", or(incident.Classification, "-")},
		{"Determination", or(incident.Determination, "-")}, {"Sources", or(strings.Join(incident.SourceNames(), ", "), "-")},
		{"Devices", or(strings.Join(incident.Devices(), ", "), "-")}, {"Users", or(strings.Join(incident.Users(), ", "), "-")},
		{"Tags", or(strings.Join(incident.Tags, ", "), "-")}, {"Link", or(incident.WebURL, "-")},
	}))
	if len(incident.Alerts) == 0 {
		return
	}
	rows := make([][]render.Cell, len(incident.Alerts))
	for index, alert := range incident.Alerts {
		rows[index] = []render.Cell{render.Plain(rt.Console.When(alert.Created)), incidentSeverity(alert.Severity),
			render.Plain(alert.Status), render.Plain(alert.SourceName()), render.Plain(alert.Title)}
	}
	rt.Console.Println("")
	rt.Console.Println(rt.Console.Table([]string{"CREATED", "SEVERITY", "STATUS", "SOURCE", "TITLE"}, rows))
}
