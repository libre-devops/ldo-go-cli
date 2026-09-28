package xdr

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/timewindow"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

const id1 = "1111111111111111111111111111111111111111"

func machine(id, name string, seen time.Time) map[string]any {
	return map[string]any{"id": id, "computerDnsName": name, "onboardingStatus": "Onboarded", "healthStatus": "Active",
		"lastSeen": seen.Format(time.RFC3339), "machineTags": []any{"prod"}, "rbacGroupName": "Servers"}
}

func client(t *testing.T, handler httpfake.Handler, mdeURL string) (*Client, *httpfake.Transport, *tokenfake.Provider) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	tokens := &tokenfake.Provider{}
	built, err := New(microsoft.API{Tokens: tokens, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient}, mdeURL)
	if err != nil {
		t.Fatal(err)
	}
	return built, transport, tokens
}

func TestARegionalEndpointStillAsksForTheGlobalResource(t *testing.T) {
	c, transport, tokens := client(t, httpfake.Routes(httpfake.Route{Match: "GET /api/machines", Reply: httpfake.JSON(map[string]any{"value": []any{}})}),
		"https://api-eu.securitycenter.microsoft.com")
	if _, err := c.MachinesNamed(context.Background(), "web01"); err != nil {
		t.Fatal(err)
	}
	if host := transport.Seen()[0].URL.Host; host != "api-eu.securitycenter.microsoft.com" {
		t.Error(host)
	}
	if tokens.Asked()[0] != "https://api.securitycenter.microsoft.com t" {
		t.Error(tokens.Asked())
	}
	httpClient, _ := httpfake.Client(nil)
	if _, err := New(microsoft.API{Tokens: tokens, Cloud: microsoft.China, HTTPClient: httpClient}, ""); !errs.Is(err, errs.Config) {
		t.Errorf("%v", err)
	}
}

func TestFindMachineTriesTheFQDNThenTheShortNameThenAPrefix(t *testing.T) {
	c, _, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /api/machines", Func: func(r *http.Request) httpfake.Reply {
		filter := r.URL.Query().Get("$filter")
		switch {
		case filter == "computerDnsName eq 'web02'":
			return httpfake.JSON(map[string]any{"value": []any{machine(id1, "web02", now.Add(-48*time.Hour)), machine("b", "web02", now)}})
		case strings.HasPrefix(filter, "startswith(computerDnsName,'db01.')"):
			return httpfake.JSON(map[string]any{"value": []any{machine("c", "db01.corp.example", now), machine("d", "db010.corp.example", now)}})
		}
		return httpfake.JSON(map[string]any{"value": []any{}})
	}}), "")
	lookups, err := c.FindMachines(context.Background(), []string{"web02.corp.example", "db01", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if lookups[0].MatchedName != "web02" || lookups[0].Machine().ID != "b" || len(lookups[0].Records) != 2 {
		t.Errorf("%+v", lookups[0])
	}
	if lookups[1].MatchedName != "db01.corp.example" || len(lookups[1].Records) != 1 {
		t.Errorf("%+v", lookups[1])
	}
	if lookups[2].Found() || lookups[2].Machine() != nil {
		t.Errorf("%+v", lookups[2])
	}
}

func TestAlertsAreFilteredHereAndNewestFirst(t *testing.T) {
	c, transport, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /api/alerts", Reply: httpfake.JSON(map[string]any{"value": []any{
		map[string]any{"id": "old", "severity": "High", "status": "New", "alertCreationTime": "2026-09-20T00:00:00Z"},
		map[string]any{"id": "low", "severity": "Low", "status": "New", "alertCreationTime": "2026-09-24T00:00:00Z"},
		map[string]any{"id": "done", "severity": "High", "status": "Resolved", "alertCreationTime": "2026-09-24T00:00:00Z"},
		map[string]any{"id": "new", "severity": "Medium", "status": "InProgress", "alertCreationTime": "2026-09-24T06:00:00Z"},
		map[string]any{"id": "mid", "severity": "High", "status": "New", "alertCreationTime": "2026-09-23T06:00:00Z"},
	}})}), "")
	alerts, err := c.Alerts(context.Background(), AlertQuery{Since: now.Add(-72 * time.Hour), MinSeverity: "medium", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, alert := range alerts {
		ids = append(ids, alert.ID)
	}
	if strings.Join(ids, ",") != "new,mid" {
		t.Error(ids)
	}
	if query := transport.Seen()[0].URL.Query(); query.Get("$filter") != "alertCreationTime ge 2026-09-21T12:00:00Z" || query.Get("$top") != "5" {
		t.Error(query)
	}
	if _, err := c.Alerts(context.Background(), AlertQuery{MinSeverity: "severe"}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Alerts(context.Background(), AlertQuery{MachineID: "short"}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestVulnerabilitiesMostSevereFirst(t *testing.T) {
	c, _, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /api/machines/" + id1 + "/vulnerabilities", Reply: httpfake.JSON(map[string]any{"value": []any{
		map[string]any{"id": "CVE-1", "severity": "Medium", "cvssV3": 5.0},
		map[string]any{"id": "CVE-2", "severity": "Critical", "cvssV3": 9.1, "exploitVerified": true},
		map[string]any{"id": "CVE-3", "severity": "Critical", "cvssV3": 9.8},
		map[string]any{"id": "CVE-4", "severity": "Low"},
	}})}), "")
	found, err := c.Vulnerabilities(context.Background(), id1)
	if err != nil {
		t.Fatal(err)
	}
	if found[0].ID != "CVE-3" || found[1].ID != "CVE-2" || !found[1].ExploitVerified || found[3].CVSS != nil {
		t.Errorf("%+v", found)
	}
}

func TestGetMachineAndStaleMachines(t *testing.T) {
	c, _, _ := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /api/machines/" + id1, Reply: httpfake.JSON(machine(id1, "web01", now))},
		httpfake.Route{Match: "GET /api/machines/2", Reply: httpfake.Status(404, map[string]any{"error": map[string]any{"code": "NotFound"}})},
		httpfake.Route{Match: "GET /api/machines", Func: func(r *http.Request) httpfake.Reply {
			if r.URL.Query().Get("$filter") != "lastSeen lt 2026-08-25T12:00:00Z" {
				t.Error(r.URL.Query().Get("$filter"))
			}
			return httpfake.JSON(map[string]any{"value": []any{machine("a", "a", now.Add(-40*24*time.Hour)), machine("b", "b", now.Add(-90*24*time.Hour))}})
		}},
	), "")
	found, err := c.GetMachine(context.Background(), id1)
	if err != nil || found.DeviceGroup != "Servers" || found.MachineTags[0] != "prod" {
		t.Errorf("%+v %v", found, err)
	}
	if _, err := c.GetMachine(context.Background(), strings.Repeat("2", 40)); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	stale, err := c.StaleMachines(context.Background(), 30*24*time.Hour, now)
	if err != nil || stale[0].ID != "b" {
		t.Errorf("%+v %v", stale, err)
	}
}

func TestHuntKeepsTheSchemaOrder(t *testing.T) {
	c, _, _ := client(t, httpfake.Routes(httpfake.Route{Match: "POST /api/advancedqueries/run", Reply: httpfake.JSON(map[string]any{
		"Schema":  []any{map[string]any{"Name": "Timestamp"}, map[string]any{"Name": "DeviceName"}},
		"Results": []any{map[string]any{"DeviceName": "web01", "Timestamp": "x"}},
	})}), "")
	result, err := c.Hunt(context.Background(), "DeviceInfo")
	if err != nil || strings.Join(result.Columns, ",") != "Timestamp,DeviceName" {
		t.Errorf("%+v %v", result, err)
	}
	if _, err := c.Hunt(context.Background(), " "); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestIndicators(t *testing.T) {
	c, _, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /api/indicators", Reply: httpfake.JSON(map[string]any{"value": []any{
		map[string]any{"indicatorValue": "1.2.3.4", "indicatorType": "IpAddress", "action": "Block"}}})}), "")
	found, err := c.Indicators(context.Background())
	if err != nil || found[0].Value != "1.2.3.4" {
		t.Errorf("%+v %v", found, err)
	}
}

func TestTheTimelineQueryIsThePythonOnes(t *testing.T) {
	want, err := os.ReadFile("testdata/timeline-web01.kql")
	if err != nil {
		t.Fatal(err)
	}
	window := timewindow.Window{Start: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	got, err := TimelineQuery("web01", window, []string{"alert", "process"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimRight(string(want), "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTimelineQueryChecksWhatGoesIntoIt(t *testing.T) {
	window := timewindow.Window{}
	for _, device := range []string{"web01; drop", `web"01`, ""} {
		if _, err := TimelineQuery(device, window, nil, 10); !errs.Is(err, errs.Input) {
			t.Errorf("%q: %v", device, err)
		}
	}
	if _, err := TimelineQuery("web01", window, nil, 0); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := TimelineQuery("web01", window, []string{"keyboard"}, 10); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	query, err := TimelineQuery("web01.corp.example", window, []string{"file"}, 10)
	if err != nil || !strings.Contains(query, `dynamic(["web01.corp.example", "web01"])`) || !strings.Contains(query, "where true") ||
		strings.Contains(query, "startswith") {
		t.Errorf("%s %v", query, err)
	}
}

func TestKindsForTheEndpointHaveNoAlerts(t *testing.T) {
	all, _ := ParseKinds(nil, true)
	if len(all) != len(Kinds())-1 || all[len(all)-1] != "other" {
		t.Error(all)
	}
	if _, err := ParseKinds([]string{"alert"}, true); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	chosen, _ := ParseKinds([]string{"Network,process"}, false)
	if strings.Join(chosen, ",") != "process,network" {
		t.Error(chosen)
	}
}

func TestReadTimeline(t *testing.T) {
	result := render.QueryResult{Rows: []map[string]any{
		{"Timestamp": "2026-09-24T10:00:00Z", "Type": "process", "DeviceName": "web01", "DeviceId": "a"},
		{"Timestamp": "2026-09-24T11:00:00Z", "Type": "alert", "DeviceName": "web01", "DeviceId": "b"},
	}}
	timeline := ReadTimeline("web01", result, 2)
	if timeline.Events[0].Kind != "alert" || !timeline.Truncated() || len(timeline.Devices()) != 2 {
		t.Errorf("%+v", timeline)
	}
	if !OutsideRetention(timewindow.Window{}, now) || OutsideRetention(timewindow.Last(time.Hour, now), now) {
		t.Error("retention")
	}
}
