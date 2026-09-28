// Package incidents reads the Graph security API's incidents: one queue for Defender XDR
// and Sentinel.
//
// Graph filters incidents on time, status and severity; which services raised an
// incident's alerts (Sentinel, say) is only known from the alerts, so that filter, and
// the sorting, happen here. Incidents come with their alerts ($expand=alerts).
package incidents

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what incidents need from a Graph token. The Azure CLI's Graph token
// never carries these, so incidents need an interactive or device-code profile whose app
// registration has been granted them.
var Requirements = []microsoft.Requirement{
	{Feature: "xdr incidents", Resource: "graph", AllOf: [][]string{{"SecurityIncident.Read.All", "SecurityIncident.ReadWrite.All"}}},
}

const path = "/v1.0/security/incidents"

// PageSize is Graph's most per page; MaxIncidents a runaway guard (narrow the window for
// more).
const (
	PageSize     = 50
	MaxIncidents = 2000
)

// ScopeHint is what a refused listing needs.
const ScopeHint = "incidents need SecurityIncident.Read.All on the Graph token, which the Azure CLI's token never " +
	"carries: use an interactive or device-code profile whose app has it. The account also needs a Defender XDR " +
	"role, such as Security Reader"

// List is the incidents found, and whether the guard stopped the listing short.
type List struct {
	Incidents []Incident
	Truncated bool
}

// Summary is how many incidents there are, by severity, status and the product that
// raised them, each most first (severity most severe first).
type Summary struct {
	Total      int
	BySeverity []Count
	ByStatus   []Count
	BySource   []Count
}

// Count is how many incidents have one value.
type Count struct {
	Name  string
	Count int
}

// Client reads incidents through Microsoft Graph.
type Client struct {
	API *httpx.Client
}

// New is an incidents client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// Query is which incidents to list: from Start to End (on By), with the given statuses
// and severities (any when empty), whose alerts came from any of Sources.
type Query struct {
	Start      time.Time
	End        time.Time
	By         string
	Statuses   []string
	Severities []string
	Sources    []string
}

// Incidents is the incidents Query names, as Graph lists them.
func (c *Client) Incidents(ctx context.Context, query Query) (List, error) {
	by := query.By
	if by == "" {
		by = "createdDateTime"
	}
	if by != "createdDateTime" && by != "lastUpdateDateTime" {
		return List{}, errs.Inputf("incidents can be windowed on created or updated, not '%s'", by)
	}
	for _, status := range query.Statuses {
		if !slices.Contains(Statuses, status) {
			return List{}, errs.Inputf("unknown incident status '%s'", status)
		}
	}
	params := url.Values{"$expand": {"alerts"}, "$top": {strconv.Itoa(PageSize)}}
	if expression := filter(query, by); expression != "" {
		params.Set("$filter", expression)
	}
	var list List
	for record, err := range c.API.All(ctx, path, params, "") {
		if err != nil {
			return List{}, explained(err)
		}
		if len(list.Incidents) >= MaxIncidents {
			list.Truncated = true
			break
		}
		list.Incidents = append(list.Incidents, IncidentFrom(record))
	}
	if len(query.Sources) > 0 {
		var kept []Incident
		for _, incident := range list.Incidents {
			if slices.ContainsFunc(incident.Sources(), func(source string) bool { return slices.Contains(query.Sources, source) }) {
				kept = append(kept, incident)
			}
		}
		list.Incidents = kept
	}
	return list, nil
}

// Incident is one incident, with its alerts and their evidence.
func (c *Client) Incident(ctx context.Context, id string) (Incident, error) {
	id = strings.TrimSpace(id)
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return Incident{}, errs.Inputf("'%s' is not an incident id (they are numbers)", id)
	}
	record, err := c.API.Get(ctx, path+"/"+id, url.Values{"$expand": {"alerts"}})
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return Incident{}, errs.NotFoundf("no incident %s", id)
	}
	if err != nil {
		return Incident{}, explained(err)
	}
	return IncidentFrom(record), nil
}

// MostSevere is most severe first, and newest first within a severity.
func MostSevere(incidents []Incident) []Incident {
	ordered := Newest(incidents)
	sort.SliceStable(ordered, func(a, b int) bool { return SeverityRank(ordered[a].Severity) < SeverityRank(ordered[b].Severity) })
	return ordered
}

// Newest is newest first, by when each was created.
func Newest(incidents []Incident) []Incident {
	ordered := slices.Clone(incidents)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].Created.After(ordered[b].Created) })
	return ordered
}

// Summarise is how many, by severity (most severe first), status and source.
func Summarise(incidents []Incident) Summary {
	severities, statuses, sources := counter{}, counter{}, counter{}
	for _, incident := range incidents {
		severity := strings.ToLower(incident.Severity)
		if severity == "" {
			severity = "unknown"
		}
		severities.add(severity)
		statuses.add(incident.Status)
		for _, name := range incident.SourceNames() {
			sources.add(name)
		}
	}
	var bySeverity []Count
	order := slices.Clone(SeverityOrder)
	var others []string
	for _, name := range severities.order {
		if !slices.Contains(SeverityOrder, name) {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	for _, name := range append(order, others...) {
		if severities.counts[name] > 0 {
			bySeverity = append(bySeverity, Count{name, severities.counts[name]})
		}
	}
	return Summary{Total: len(incidents), BySeverity: bySeverity, ByStatus: statuses.mostCommon(), BySource: sources.mostCommon()}
}

// counter counts values, remembering the order each was first seen, as Python's Counter.
type counter struct {
	counts map[string]int
	order  []string
}

func (c *counter) add(name string) {
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	if c.counts[name] == 0 {
		c.order = append(c.order, name)
	}
	c.counts[name]++
}

// mostCommon is the counts, most first; equal counts in the order first seen.
func (c *counter) mostCommon() []Count {
	found := make([]Count, len(c.order))
	for index, name := range c.order {
		found[index] = Count{name, c.counts[name]}
	}
	sort.SliceStable(found, func(a, b int) bool { return found[a].Count > found[b].Count })
	return found
}

// SeveritiesFrom is the severities at or above minimum: medium is high and medium.
func SeveritiesFrom(minimum string) ([]string, error) {
	index := slices.Index(SeverityOrder, strings.ToLower(strings.TrimSpace(minimum)))
	if index < 0 {
		return nil, errs.Inputf("unknown severity '%s'", minimum).WithHint("use one of %s", strings.Join(SeverityOrder, ", "))
	}
	return slices.Clone(SeverityOrder[:index+1]), nil
}

func filter(query Query, by string) string {
	var terms []string
	if !query.Start.IsZero() {
		terms = append(terms, by+" ge "+util.ODataDatetime(query.Start))
	}
	if !query.End.IsZero() {
		terms = append(terms, by+" lt "+util.ODataDatetime(query.End))
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"status", query.Statuses}, {"severity", query.Severities}} {
		var either []string
		for _, value := range field.values {
			either = append(either, field.name+" eq "+util.ODataString(value))
		}
		switch len(either) {
		case 0:
		case 1:
			terms = append(terms, either[0])
		default:
			terms = append(terms, "("+strings.Join(either, " or ")+")")
		}
	}
	return strings.Join(terms, " and ")
}

// explained gives a refused call the hint for the permission it needs; a suspended
// service answers 403 too, and its own hint says why better.
func explained(err error) error {
	found := errs.As(err)
	if found == nil || (found.Status != 401 && found.Status != 403) || strings.Contains(strings.ToLower(found.Message), "suspended") {
		return err
	}
	copied := *found
	copied.Hint = ScopeHint
	return &copied
}
