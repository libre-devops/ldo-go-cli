package xdr

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/timewindow"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// A device's timeline, from Advanced Hunting: the events the Defender portal's timeline
// shows.
//
// Defender has no API for the device timeline. The portal's timeline page reads an
// internal service of its own, which Microsoft neither documents nor supports, and which
// accepts no token an app registration or the Azure CLI can get. The events behind that
// page are in Advanced Hunting's device tables, though, and those the supported hunting
// APIs can query: Graph's runHuntingQuery (ThreatHunting.Read.All) or Defender for
// Endpoint's advancedqueries/run (AdvancedQuery.Read). So a timeline here is one query
// over those tables, for one device and one window of time, newest first.
//
// Advanced Hunting keeps 30 days of device events; a query returns at most 100,000 rows
// and counts against the tenant's hunting quota, so only the newest limit are asked for.
// The query is fixed text around values checked first: the device name
// (util.RequireHost), the kinds of event (from Kinds), the window (written here) and the
// limit (a whole number in range). Nothing else a person types reaches it.

// Retention is how far back Advanced Hunting keeps device events.
const Retention = 30 * 24 * time.Hour

// MaxEvents is the most rows one hunting query returns; DefaultLimit the events asked for.
const (
	MaxEvents    = 100000
	DefaultLimit = 1000
)

// kind is one kind of event: the table that holds it, and KQL for its detail and account.
type kind struct {
	name    string
	table   string
	detail  string
	account string
}

// kinds is each kind's table and what it shows: the detail a person reads first (a
// command line, a connection, a path), and the account behind it. coalesce() takes the
// first that is not empty. The alert kind is built apart, from AlertEvidence and
// AlertInfo, so it has no table here.
var kinds = []kind{
	{"process", "DeviceProcessEvents", "coalesce(ProcessCommandLine, FolderPath, FileName)",
		"coalesce(AccountName, InitiatingProcessAccountName)"},
	{"network", "DeviceNetworkEvents", `iff(isempty(RemoteIP), strcat("listening on ", LocalIP, ":", LocalPort), ` +
		`strcat(RemoteIP, ":", RemotePort, iff(isempty(RemoteUrl), "", strcat(" ", RemoteUrl))))`, "InitiatingProcessAccountName"},
	{"file", "DeviceFileEvents", "coalesce(FolderPath, FileName)", "coalesce(RequestAccountName, InitiatingProcessAccountName)"},
	{"registry", "DeviceRegistryEvents", `strcat(RegistryKey, iff(isempty(RegistryValueName), "", ` +
		`strcat(" ", RegistryValueName, " = ", RegistryValueData)))`, "InitiatingProcessAccountName"},
	{"logon", "DeviceLogonEvents", `strcat(LogonType, iff(isempty(RemoteIP), "", strcat(" from ", RemoteIP)))`, "AccountName"},
	{"image-load", "DeviceImageLoadEvents", "coalesce(FolderPath, FileName)", "InitiatingProcessAccountName"},
	{"other", "DeviceEvents", "coalesce(ProcessCommandLine, FolderPath, FileName, RemoteUrl, RemoteIP)",
		"coalesce(AccountName, InitiatingProcessAccountName)"},
	{"alert", "", "", ""},
}

// Kinds are the kinds of event, in order.
func Kinds() []string {
	names := make([]string, len(kinds))
	for index, item := range kinds {
		names[index] = item.name
	}
	return names
}

// TimelineEvent is one event on a device's timeline. Kind is one of Kinds; Action its
// ActionType (for an alert, its severity); ID the event's ReportId, or the AlertId.
type TimelineEvent struct {
	Time       time.Time
	Kind       string
	Action     string
	Detail     string
	Account    string
	Process    string
	DeviceName string
	DeviceID   string
	ID         string
	Raw        fields.Object
}

// Timeline is a device's events, newest first, as one query found them.
type Timeline struct {
	Device string
	Events []TimelineEvent
	Limit  int
}

// Truncated reports whether the query stopped at its limit, so older events in the
// window are left out.
func (t Timeline) Truncated() bool { return len(t.Events) >= t.Limit }

// Devices is each device the events came from, by name and id: more than one when
// devices share the name (a rebuilt server keeps its name and gets a new id).
func (t Timeline) Devices() []string {
	var found []string
	for _, event := range t.Events {
		name := event.DeviceName + " (" + event.DeviceID + ")"
		if !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	return found
}

// ParseKinds is the kinds of event named, in Kinds order ("process,network", or
// repeated); all of them when none are named. deviceTablesOnly is for Defender for
// Endpoint's hunting API, which has no alert tables: every kind but alerts, and an Input
// error when alerts are named.
func ParseKinds(values []string, deviceTablesOnly bool) ([]string, error) {
	named := map[string]bool{}
	var unknown []string
	for _, value := range util.SplitNames(values) {
		lowered := strings.ToLower(value)
		named[lowered] = true
		if !slices.Contains(Kinds(), lowered) {
			unknown = append(unknown, lowered)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, errs.Inputf("unknown kind of event: %s", strings.Join(unknown, ", ")).WithHint("use %s", strings.Join(Kinds(), ", "))
	}
	if deviceTablesOnly && named["alert"] {
		return nil, errs.Inputf("alerts are not in the Defender for Endpoint API's tables, only device events are").
			WithHint("ask Graph's hunting API for alerts (leave out --endpoint), or leave alert out")
	}
	var chosen []string
	for _, item := range kinds {
		if (deviceTablesOnly && item.name == "alert") || (len(named) > 0 && !named[item.name]) {
			continue
		}
		chosen = append(chosen, item.name)
	}
	return chosen, nil
}

// TimelineQuery is the KQL for a device's events in the window: the newest limit of the
// kinds asked for (every kind when none are), each shaped to the same columns.
func TimelineQuery(device string, window timewindow.Window, chosen []string, limit int) (string, error) {
	if limit < 1 || limit > MaxEvents {
		return "", errs.Inputf("the limit must be from 1 to 100,000, not %d", limit)
	}
	chosen, err := ParseKinds(chosen, false)
	if err != nil {
		return "", err
	}
	names, err := deviceNames(device)
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(names)
	lines := []string{"let device = dynamic(" + strings.ReplaceAll(string(encoded), ",", ", ") + ");"}
	var lets []string
	for _, name := range chosen {
		part, err := queryPart(name, window, device)
		if err != nil {
			return "", err
		}
		lines = append(lines, "let "+letName(name)+" = "+part+";")
		lets = append(lets, letName(name))
	}
	lines = append(lines, "union "+strings.Join(lets, ", "), fmt.Sprintf("| top %d by Timestamp desc", limit))
	return strings.Join(lines, "\n"), nil
}

// ReadTimeline is a result of TimelineQuery as a Timeline, newest first.
func ReadTimeline(device string, result render.QueryResult, limit int) Timeline {
	events := make([]TimelineEvent, len(result.Rows))
	for index, row := range result.Rows {
		events[index] = TimelineEvent{
			Time: fields.When(row, "Timestamp"), Kind: fields.Text(row, "Type"), Action: fields.Text(row, "ActionType"),
			Detail: fields.Text(row, "Detail"), Account: fields.Text(row, "Account"), Process: fields.Text(row, "Process"),
			DeviceName: fields.Text(row, "DeviceName"), DeviceID: fields.Text(row, "DeviceId"), ID: fields.Text(row, "Id"), Raw: row,
		}
	}
	sort.SliceStable(events, func(a, b int) bool { return events[a].Time.After(events[b].Time) })
	return Timeline{Device: device, Events: events, Limit: limit}
}

// OutsideRetention reports whether the window reaches back past what Advanced Hunting
// keeps (or has no start).
func OutsideRetention(window timewindow.Window, now time.Time) bool {
	return window.Start.IsZero() || window.Start.Before(now.Add(-Retention))
}

// deviceNames are the names to match, lower case: an FQDN and its host name, or a host
// name alone.
func deviceNames(device string) ([]string, error) {
	name, err := util.RequireHost(device)
	if err != nil {
		return nil, err
	}
	name = strings.ToLower(name)
	names := []string{name}
	if short := util.ShortName(name); short != name {
		names = append(names, short)
	}
	return names, nil
}

// deviceFilter is rows from the device, by the names the query's device list holds. A
// host name on its own also finds the device by its first label (web01 finds
// web01.corp.example), never a longer name (web010).
func deviceFilter(device string) (string, error) {
	name, err := util.RequireHost(device)
	if err != nil {
		return "", err
	}
	name = strings.ToLower(name)
	if strings.Contains(name, ".") {
		return "DeviceName in~ (device)", nil
	}
	encoded, _ := json.Marshal(name + ".")
	return "DeviceName in~ (device) or DeviceName startswith " + string(encoded), nil
}

func timeFilter(window timewindow.Window) string {
	var bounds []string
	if !window.Start.IsZero() {
		bounds = append(bounds, "Timestamp >= "+kqlTime(window.Start))
	}
	if !window.End.IsZero() {
		bounds = append(bounds, "Timestamp < "+kqlTime(window.End))
	}
	if len(bounds) == 0 {
		return "true"
	}
	return strings.Join(bounds, " and ")
}

// queryPart is one kind's rows, filtered to the window and the device, in the shared
// columns.
func queryPart(name string, window timewindow.Window, device string) (string, error) {
	filter, err := deviceFilter(device)
	if err != nil {
		return "", err
	}
	where := "| where " + timeFilter(window) + "\n    | where " + filter
	index := slices.IndexFunc(kinds, func(item kind) bool { return item.name == name })
	shape := kinds[index]
	if shape.table == "" {
		// An alert's evidence is a row per entity: the first one on this device dates it.
		return "AlertEvidence\n    " + where + "\n" +
			"    | summarize Timestamp = min(Timestamp) by AlertId, DeviceName, DeviceId\n" +
			"    | join kind=leftouter (AlertInfo | summarize arg_max(Timestamp, Title, Severity) by AlertId) on AlertId\n" +
			`    | project Timestamp, Type = "alert", ActionType = Severity, Detail = Title, Account = "", Process = "", DeviceName, DeviceId, Id = AlertId`, nil
	}
	return shape.table + "\n    " + where + "\n" +
		`    | project Timestamp, Type = "` + name + `", ActionType, Detail = ` + shape.detail + ",\n" +
		"        Account = " + shape.account + ", Process = InitiatingProcessFileName,\n" +
		"        DeviceName, DeviceId, Id = tostring(ReportId)", nil
}

func letName(name string) string { return strings.ReplaceAll(name, "-", "_") + "_events" }

func kqlTime(moment time.Time) string {
	return "datetime(" + moment.UTC().Format("2006-01-02T15:04:05Z") + ")"
}
