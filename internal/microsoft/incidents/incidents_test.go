package incidents

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func client(t *testing.T, handler httpfake.Handler) (*Client, *httpfake.Transport) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport
}

func queue() httpfake.Handler {
	return httpfake.Routes(httpfake.Route{Match: "GET /v1.0/security/incidents", Reply: httpfake.JSON(graphfake.Page(
		graphfake.Incident("1", "medium", "active", now.Add(-time.Hour), graphfake.IncidentAlert("a1", "microsoftSentinel")),
		graphfake.Incident("2", "high", "inProgress", now.Add(-3*time.Hour), graphfake.IncidentAlert("a2", "microsoftDefenderForEndpoint")),
		graphfake.Incident("3", "high", "resolved", now.Add(-2*time.Hour)),
	))})
}

func TestTheFilterIsSentAndSourcesAreFilteredHere(t *testing.T) {
	c, transport := client(t, queue())
	found, err := c.Incidents(context.Background(), Query{Start: now.Add(-24 * time.Hour), End: now, Statuses: OpenStatuses,
		Severities: []string{"high"}, Sources: []string{"microsoftSentinel"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Incidents) != 1 || found.Incidents[0].ID != "1" {
		t.Errorf("%+v", found)
	}
	query := transport.Seen()[0].URL.Query()
	want := "createdDateTime ge 2026-09-23T12:00:00Z and createdDateTime lt 2026-09-24T12:00:00Z and " +
		"(status eq 'active' or status eq 'inProgress' or status eq 'awaitingAction') and severity eq 'high'"
	if query.Get("$filter") != want || query.Get("$expand") != "alerts" || query.Get("$top") != "50" {
		t.Errorf("%v", query)
	}
}

func TestOrderAndSummary(t *testing.T) {
	c, _ := client(t, queue())
	found, err := c.Incidents(context.Background(), Query{By: "lastUpdateDateTime"})
	if err != nil {
		t.Fatal(err)
	}
	severe := MostSevere(found.Incidents)
	if severe[0].ID != "3" || severe[1].ID != "2" || severe[2].ID != "1" {
		t.Errorf("%v %v %v", severe[0].ID, severe[1].ID, severe[2].ID)
	}
	if newest := Newest(found.Incidents); newest[0].ID != "1" {
		t.Error(newest[0].ID)
	}
	summary := Summarise(found.Incidents)
	if summary.Total != 3 || summary.BySeverity[0] != (Count{"high", 2}) || summary.BySource[0].Name != "Sentinel" || len(summary.ByStatus) != 3 {
		t.Errorf("%+v", summary)
	}
	incident := found.Incidents[0]
	if !incident.Open() || incident.Devices()[0] != "web01.corp.example" || incident.Users()[0] != "ana" || incident.Tags[0] != "patching" {
		t.Errorf("%+v", incident)
	}
}

func TestBadQueriesAndIDs(t *testing.T) {
	c, _ := client(t, queue())
	if _, err := c.Incidents(context.Background(), Query{By: "closedDateTime"}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Incidents(context.Background(), Query{Statuses: []string{"done"}}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Incident(context.Background(), "12; drop"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := SeveritiesFrom("severe"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if found, _ := SeveritiesFrom("Medium"); strings.Join(found, ",") != "high,medium" {
		t.Error(found)
	}
}

func TestOneIncidentAndARefusal(t *testing.T) {
	c, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/security/incidents/7", Reply: httpfake.JSON(graphfake.Incident("7", "low", "active", now))},
		httpfake.Route{Match: "GET /v1.0/security/incidents/8", Reply: httpfake.GraphError(403, "Forbidden", "no")},
		httpfake.Route{Match: "GET /v1.0/security/incidents/9", Reply: httpfake.GraphError(403, "Forbidden", "tenant is suspended")},
	))
	if incident, err := c.Incident(context.Background(), " 7 "); err != nil || incident.ID != "7" {
		t.Errorf("%+v %v", incident, err)
	}
	if _, err := c.Incident(context.Background(), "6"); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if _, err := c.Incident(context.Background(), "8"); errs.HintOf(err) != ScopeHint {
		t.Errorf("%v", err)
	}
	if _, err := c.Incident(context.Background(), "9"); errs.HintOf(err) == ScopeHint {
		t.Errorf("%v", err)
	}
}

func TestTheGuardStopsARunawayListing(t *testing.T) {
	page := 0
	c, _ := client(t, func(r *http.Request) httpfake.Reply {
		page++
		var items []map[string]any
		for index := range PageSize {
			items = append(items, graphfake.Incident(string(rune('a'+index%26)), "low", "active", now))
		}
		body := graphfake.Page(items...)
		body["@odata.nextLink"] = "https://graph.microsoft.com/v1.0/security/incidents?$skiptoken=" + string(rune('a'+page%26))
		return httpfake.JSON(body)
	})
	found, err := c.Incidents(context.Background(), Query{})
	if err != nil || !found.Truncated || len(found.Incidents) != MaxIncidents {
		t.Errorf("%d %v %v", len(found.Incidents), found.Truncated, err)
	}
}
