// Package xdr reads Defender for Endpoint (Defender XDR): machines, alerts,
// vulnerabilities, indicators, hunting, and device timelines built from Advanced Hunting.
//
// Device lookups are small, server-side filtered GETs per candidate name, never a
// download of the tenant-wide machine inventory. The FQDN is tried first, then the short
// hostname, because Defender does not report Linux names consistently.
package xdr

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what each Defender for Endpoint feature needs from a token: the
// application permissions and granular delegated scopes. The Azure CLI's Defender token
// carries only user_impersonation, in which case the checks say access rests on the
// user's Defender role instead.
var Requirements = []microsoft.Requirement{
	{Feature: "xdr machines", Resource: "mde", AllOf: [][]string{{"Machine.Read.All", "Machine.ReadWrite.All", "Machine.Read", "Machine.ReadWrite"}}},
	{Feature: "xdr alerts", Resource: "mde", AllOf: [][]string{{"Alert.Read.All", "Alert.ReadWrite.All", "Alert.Read", "Alert.ReadWrite"}}},
	{Feature: "xdr vulnerabilities", Resource: "mde", AllOf: [][]string{{"Vulnerability.Read.All", "Vulnerability.Read"}}},
	{Feature: "xdr indicators", Resource: "mde", AllOf: [][]string{{"Ti.Read.All", "Ti.ReadWrite", "Ti.ReadWrite.All"}}},
	{Feature: "xdr hunting (hunt, timeline, av-signature with --endpoint)", Resource: "mde", AllOf: [][]string{{"AdvancedQuery.Read.All", "AdvancedQuery.Read"}}},
}

var machineID = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// Severities are the severities, lowest first.
var Severities = []string{"informational", "low", "medium", "high", "critical"}

// Client reads Defender for Endpoint for one tenant.
type Client struct {
	API *httpx.Client
}

// New is a Defender client for api's tenant, at the profile's regional endpoint when it
// has one; tokens are for the cloud's Defender resource, which regional endpoints accept.
func New(api microsoft.API, mdeURL string) (*Client, error) {
	resource, err := api.Cloud.RequireMDE()
	if err != nil {
		return nil, err
	}
	address := mdeURL
	if address == "" {
		address = resource
	}
	client, err := api.Client("Defender for Endpoint", address, resource, nil)
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

func (c *Client) machines(ctx context.Context, filter string) ([]Machine, error) {
	items, err := httpx.Collect(c.API.All(ctx, "/api/machines", httpxValues("$filter", filter), ""))
	if err != nil {
		return nil, err
	}
	machines := make([]Machine, len(items))
	for index, item := range items {
		machines[index] = MachineFrom(item)
	}
	return machines, nil
}

// MachinesNamed is every machine record whose computerDnsName equals name.
func (c *Client) MachinesNamed(ctx context.Context, name string) ([]Machine, error) {
	return c.machines(ctx, "computerDnsName eq "+util.ODataString(name))
}

// FindMachine looks one device up by FQDN, falling back to its short hostname.
//
// A short name finds nothing when Defender knows the device by its FQDN, so it is then
// looked for as the first label of one: web01 finds web01.corp.example.com, never
// web010.corp.example.com.
func (c *Client) FindMachine(ctx context.Context, name string) (MachineLookup, error) {
	for _, candidate := range util.CandidateNames(name) {
		records, err := c.MachinesNamed(ctx, candidate)
		if err != nil {
			return MachineLookup{}, err
		}
		if len(records) > 0 {
			return MachineLookup{Query: name, MatchedName: candidate, Records: newestFirst(records)}, nil
		}
	}
	short := strings.TrimRight(strings.TrimSpace(name), ".")
	if short != "" && !strings.Contains(short, ".") {
		records, err := c.machines(ctx, "startswith(computerDnsName,"+util.ODataString(short+".")+")")
		if err != nil {
			return MachineLookup{}, err
		}
		var kept []Machine
		for _, machine := range records {
			first, _, _ := strings.Cut(machine.ComputerDNSName, ".")
			if strings.EqualFold(first, short) {
				kept = append(kept, machine)
			}
		}
		if len(kept) > 0 {
			ordered := newestFirst(kept)
			return MachineLookup{Query: name, MatchedName: ordered[0].ComputerDNSName, Records: ordered}, nil
		}
	}
	return MachineLookup{Query: name}, nil
}

// FindMachines is FindMachine for each name, in order.
func (c *Client) FindMachines(ctx context.Context, names []string) ([]MachineLookup, error) {
	var lookups []MachineLookup
	for _, name := range names {
		lookup, err := c.FindMachine(ctx, name)
		if err != nil {
			return nil, err
		}
		lookups = append(lookups, lookup)
	}
	return lookups, nil
}

// GetMachine is one machine by its Defender id (40 hex characters).
func (c *Client) GetMachine(ctx context.Context, id string) (Machine, error) {
	checked, err := MachineID(id)
	if err != nil {
		return Machine{}, err
	}
	data, err := c.API.Get(ctx, "/api/machines/"+checked, nil)
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return Machine{}, errs.NotFoundf("no MDE machine has id %s", checked)
	}
	if err != nil {
		return Machine{}, err
	}
	return MachineFrom(data), nil
}

// StaleMachines is the machines not seen for olderThan, longest silent first. The filter
// runs on the server, so only the stale records are downloaded.
func (c *Client) StaleMachines(ctx context.Context, olderThan time.Duration, now time.Time) ([]Machine, error) {
	machines, err := c.machines(ctx, "lastSeen lt "+util.ODataDatetime(now.Add(-olderThan)))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(machines, func(a, b int) bool { return machines[a].LastSeen.Before(machines[b].LastSeen) })
	return machines, nil
}

// AlertQuery narrows Alerts.
type AlertQuery struct {
	MachineID       string
	Since           time.Time
	MinSeverity     string
	IncludeResolved bool
	Limit           int
}

// Alerts is the alerts for the tenant, or for one machine, newest first.
//
// The creation-time filter runs on the server; severity and status are filtered here,
// because the API's support for filtering those varies by endpoint.
func (c *Client) Alerts(ctx context.Context, query AlertQuery) ([]Alert, error) {
	floor := -1
	if query.MinSeverity != "" {
		rank, err := ParseSeverity(query.MinSeverity)
		if err != nil {
			return nil, err
		}
		floor = rank
	}
	limit := max(query.Limit, 1)
	var items []fields.Object
	var err error
	if query.MachineID != "" {
		id, idErr := MachineID(query.MachineID)
		if idErr != nil {
			return nil, idErr
		}
		items, err = httpx.Collect(c.API.All(ctx, "/api/machines/"+id+"/alerts", nil, ""))
	} else {
		params := httpxValues("$top", strconv.Itoa(min(limit, 10000)))
		if !query.Since.IsZero() {
			params.Set("$filter", "alertCreationTime ge "+util.ODataDatetime(query.Since))
		}
		items, err = httpx.Collect(c.API.All(ctx, "/api/alerts", params, ""))
	}
	if err != nil {
		return nil, err
	}
	var alerts []Alert
	for _, item := range items {
		alert := AlertFrom(item)
		if (query.IncludeResolved || !alert.Resolved()) && SeverityRank(alert.Severity) >= floor &&
			(query.Since.IsZero() || alert.Created.IsZero() || !alert.Created.Before(query.Since)) {
			alerts = append(alerts, alert)
		}
	}
	sort.SliceStable(alerts, func(a, b int) bool { return alerts[a].Created.After(alerts[b].Created) })
	if len(alerts) > limit {
		alerts = alerts[:limit]
	}
	return alerts, nil
}

// Vulnerabilities is the vulnerabilities on one machine, most severe first.
func (c *Client) Vulnerabilities(ctx context.Context, machine string) ([]Vulnerability, error) {
	id, err := MachineID(machine)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, "/api/machines/"+id+"/vulnerabilities", nil, ""))
	if err != nil {
		return nil, err
	}
	found := make([]Vulnerability, len(items))
	for index, item := range items {
		found[index] = VulnerabilityFrom(item)
	}
	score := func(item Vulnerability) float64 {
		if item.CVSS == nil {
			return 0
		}
		return *item.CVSS
	}
	sort.SliceStable(found, func(a, b int) bool {
		rankA, rankB := SeverityRank(found[a].Severity), SeverityRank(found[b].Severity)
		if rankA != rankB {
			return rankA > rankB
		}
		return score(found[a]) > score(found[b])
	})
	return found, nil
}

// Indicators is every custom indicator in the tenant.
func (c *Client) Indicators(ctx context.Context) ([]Indicator, error) {
	items, err := httpx.Collect(c.API.All(ctx, "/api/indicators", nil, ""))
	if err != nil {
		return nil, err
	}
	found := make([]Indicator, len(items))
	for index, item := range items {
		found[index] = IndicatorFrom(item)
	}
	return found, nil
}

// Hunt runs an Advanced Hunting (KQL) query. The API caps results at 100,000 rows.
func (c *Client) Hunt(ctx context.Context, query string) (render.QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return render.QueryResult{}, errs.Inputf("the hunting query is empty")
	}
	data, err := c.API.Post(ctx, "/api/advancedqueries/run", map[string]any{"Query": query}, nil)
	if err != nil {
		return render.QueryResult{}, err
	}
	var columns []string
	for _, column := range fields.Objects(data["Schema"]) {
		columns = append(columns, fields.String(column["Name"]))
	}
	rows := fields.Objects(data["Results"])
	records := make([]map[string]any, len(rows))
	copy(records, rows)
	if len(columns) == 0 {
		return render.FromRecords(records, false), nil
	}
	return render.QueryResult{Columns: columns, Rows: records}, nil
}

func newestFirst(records []Machine) []Machine {
	ordered := slices.Clone(records)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].LastSeen.After(ordered[b].LastSeen) })
	return ordered
}

// ParseSeverity is the rank of a severity someone typed: an Input error for anything
// unknown.
func ParseSeverity(severity string) (int, error) {
	rank := slices.Index(Severities, strings.ToLower(strings.TrimSpace(severity)))
	if rank < 0 {
		return 0, errs.Inputf("unknown severity '%s'", severity).WithHint("use one of %s", strings.Join(Severities, ", "))
	}
	return rank, nil
}

// SeverityRank is the rank of a severity from the API, leniently: an unknown one ranks
// below the rest.
func SeverityRank(severity string) int {
	return slices.Index(Severities, strings.ToLower(severity))
}

// MachineID is value as a Defender machine id, or an Input error.
func MachineID(value string) (string, error) {
	id := strings.TrimSpace(value)
	if !machineID.MatchString(id) {
		return "", errs.Inputf("not an MDE machine id: '%s'", id)
	}
	return id, nil
}

func httpxValues(pairs ...string) url.Values {
	found := url.Values{}
	for index := 0; index+1 < len(pairs); index += 2 {
		found.Set(pairs[index], pairs[index+1])
	}
	return found
}
