package devices

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/poll"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/clockfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/xdrfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// services answers Entra, Defender and Intune for web01 (everywhere, tagged), web02
// (in Entra only) and web03 (Defender fails for it).
func services(onboarded *atomic.Bool) httpfake.Handler {
	servers := graphfake.Group(50, "Servers", false)
	return httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/devices", Func: func(r *http.Request) httpfake.Reply {
			filter := r.URL.Query().Get("$filter")
			for _, name := range []string{"web01", "web02", "web03"} {
				if strings.Contains(filter, "'"+name+"'") {
					device := graphfake.Device(int(name[4]-'0'), name, now)
					device["deviceId"] = "aad-" + name
					return httpfake.JSON(graphfake.Page(device))
				}
			}
			return httpfake.JSON(graphfake.Page())
		}},
		httpfake.Route{Match: "GET /v1.0/groups", Reply: httpfake.JSON(graphfake.Page(servers))},
		httpfake.Route{Match: "GET /v1.0/groups/" + graphfake.ID(50) + "/transitiveMembers/microsoft.graph.device",
			Reply: httpfake.JSON(graphfake.Page(graphfake.Device(1, "web01", now)))},
		httpfake.Route{Match: "GET /v1.0/devices/", Reply: httpfake.JSON(graphfake.Page(servers))},
		httpfake.Route{Match: "GET /api/machines", Func: func(r *http.Request) httpfake.Reply {
			filter := r.URL.Query().Get("$filter")
			switch {
			case strings.Contains(filter, "'web03'"):
				return httpfake.Status(500, map[string]any{"error": map[string]any{"message": "flaky"}})
			case strings.Contains(filter, "'web01'") || (onboarded != nil && onboarded.Load() && strings.Contains(filter, "'web02'")):
				machine := xdrfake.Machine('1', "web01", now.Add(-10*24*time.Hour))
				machine["aadDeviceId"] = "aad-web01"
				return httpfake.JSON(xdrfake.Page(machine))
			}
			return httpfake.JSON(xdrfake.Page())
		}},
		httpfake.Route{Match: "GET /v1.0/deviceManagement/managedDevices", Func: func(r *http.Request) httpfake.Reply {
			if strings.Contains(r.URL.Query().Get("$filter"), "'web01'") {
				return httpfake.JSON(graphfake.Page(map[string]any{"deviceName": "web01", "complianceState": "noncompliant",
					"managementAgent": "mdm", "azureADDeviceId": "aad-other"}))
			}
			return httpfake.JSON(graphfake.Page())
		}},
	)
}

func checker(t *testing.T, handler httpfake.Handler) *Checker {
	t.Helper()
	httpClient, _ := httpfake.Client(handler)
	api := microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient}
	entraClient, _ := entra.New(api)
	xdrClient, _ := xdr.New(api, "")
	intuneClient, _ := intune.New(api)
	// Retries would slow the flaky service down; one attempt is enough here.
	xdrClient.API = retriesOff(t, api)
	return &Checker{Entra: entraClient, XDR: xdrClient, Intune: intuneClient, Workers: 4, Now: func() time.Time { return now }}
}

func TestEveryCheckIsJudgedAndOneFlakyServiceIsAnErrorForItsDevice(t *testing.T) {
	c := checker(t, services(nil))
	expectations := Expectations{InEntra: true, Onboarded: true, Active: true, Tags: []string{"PROD"}, DeviceGroups: []string{"servers"},
		Groups: []string{"Servers"}, InIntune: true, Compliant: true}
	run, err := c.Check(context.Background(), []string{"web01", "web02", "web03"}, expectations)
	if err != nil {
		t.Fatal(err)
	}
	web01, web02, web03 := run.Reports[0], run.Reports[1], run.Reports[2]
	for _, check := range []string{"entra", "defender", "active", "tag PROD", "device group servers", "group Servers", "intune"} {
		if outcome, _ := web01.Outcome(check); outcome.Status != "met" {
			t.Errorf("web01 %s: %+v", check, outcome)
		}
	}
	if outcome, _ := web01.Outcome("compliant"); outcome.Status != "unmet" || outcome.Detail != "noncompliant" {
		t.Errorf("%+v", outcome)
	}
	if outcome, _ := web02.Outcome("defender"); outcome.Detail != "no Defender record" {
		t.Errorf("%+v", outcome)
	}
	if outcome, _ := web02.Outcome("group Servers"); outcome.Detail != "not a member" {
		t.Errorf("%+v", outcome)
	}
	if outcome, _ := web03.Outcome("defender"); outcome.Status != "error" {
		t.Errorf("%+v", outcome)
	}
	if run.Complete() || run.Counts(expectations.Checks())[0].Met != 3 {
		t.Errorf("%+v", run.Counts(expectations.Checks()))
	}
}

func TestAForbiddenServiceEndsTheCheck(t *testing.T) {
	c := checker(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/devices", Reply: httpfake.GraphError(403, "Forbidden", "no")}))
	if _, err := c.Check(context.Background(), []string{"web01"}, Expectations{InEntra: true}); !errs.Is(err, errs.API) {
		t.Errorf("%v", err)
	}
	if _, err := (&Checker{}).Check(context.Background(), []string{"web01"}, Expectations{InEntra: true}); err == nil {
		t.Error("a check with no client ran")
	}
	if _, err := c.Check(context.Background(), []string{"web01"}, Expectations{}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestAWatchRechecksOnlyWhatIsOutstanding(t *testing.T) {
	var onboarded atomic.Bool
	requests := 0
	handler := services(&onboarded)
	counting := func(r *http.Request) httpfake.Reply {
		if strings.HasPrefix(r.URL.Path, "/api/machines") {
			requests++
			if requests >= 3 {
				onboarded.Store(true)
			}
		}
		return handler(r)
	}
	c := checker(t, counting)
	clock := clockfake.New(now)
	var passes []int
	outcome, err := Watch(context.Background(), c, []string{"web01", "web02"}, Expectations{Onboarded: true},
		poll.Limits{Interval: 5 * time.Minute, Timeout: time.Hour}, false, poll.Clock{Now: clock.Now, Sleep: clock.Sleep},
		poll.Hooks[Run]{OnPass: func(number int, _ Run) { passes = append(passes, number) }})
	if err != nil || !outcome.Complete() || outcome.Passes != 2 || len(outcome.Result.Reports) != 2 {
		t.Fatalf("%+v %v", outcome, err)
	}
	// Pass 1 looks both up (web02 twice: by name, then as the first label of an FQDN);
	// pass 2 only web02, the one left outstanding.
	if requests != 4 {
		t.Errorf("%d Defender requests", requests)
	}
}

func TestInspectFindsWhatLooksWrong(t *testing.T) {
	c := checker(t, services(nil))
	view, err := Inspect(context.Background(), "web01", c.Entra, c.XDR, c.Intune, 7*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, finding := range view.Findings {
		messages = append(messages, finding.Level+": "+finding.Message)
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"warn: Defender last saw it 10d 00h ago", "warn: Intune compliance is noncompliant",
		"warn: Intune links it to Entra device aad-other"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if len(view.Groups[graphfake.ID(1)]) != 1 {
		t.Errorf("%v", view.Groups)
	}
	quiet, err := Inspect(context.Background(), "web03", c.Entra, c.XDR, nil, 7*24*time.Hour, now)
	if err != nil || quiet.Defender != nil || !strings.Contains(quiet.Findings[0].Message, "Defender could not be read") {
		t.Errorf("%+v %v", quiet, err)
	}
	missing, _ := Inspect(context.Background(), "nope", c.Entra, nil, nil, time.Hour, now)
	if missing.Findings[0].Message != "not in Entra" {
		t.Errorf("%+v", missing.Findings)
	}
}

func TestTheAVQueryIsThePythonOnes(t *testing.T) {
	want, _ := os.ReadFile("testdata/av.kql")
	got, err := AVQuery([]string{"web01.corp.example", "DB01", "web01"})
	if err != nil || got != string(want) {
		t.Errorf("%v\ngot:\n%s\nwant:\n%s", err, got, want)
	}
	if _, err := AVQuery([]string{"web01; drop"}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := AVQuery(nil); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestAVStatusesByNameNewestFirst(t *testing.T) {
	result := render.QueryResult{Rows: []map[string]any{
		{"DeviceName": "web01.corp.example", "AvSignatureVersion": "1.419.10.0", "AvMode": "0", "SignatureUpToDate": true, "Reported": "2026-09-23T00:00:00Z"},
		{"DeviceName": "WEB01", "AvSignatureVersion": "1.400.0.0", "AvMode": "9", "Reported": "2026-09-24T00:00:00Z"},
	}}
	statuses := AVStatuses([]string{"web01", "db01"}, result)
	if len(statuses) != 3 || statuses[0].Signature != "1.400.0.0" || statuses[0].Mode != "9" || statuses[1].Mode != "active" ||
		*statuses[1].UpToDate != true || statuses[2].Found {
		t.Errorf("%+v", statuses)
	}
	if !statuses[0].OlderThan("1.419.0.0") || statuses[1].OlderThan("1.419.0.0") || !(AVStatus{}).OlderThan("1") {
		t.Error("versions")
	}
	if _, err := VersionKey("latest"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if ModeLabel(nil) != "" || ModeLabel(float64(4)) != "EDR block" {
		t.Error(ModeLabel(float64(4)))
	}
}

func retriesOff(t *testing.T, api microsoft.API) *httpx.Client {
	t.Helper()
	resource, _ := api.Cloud.RequireMDE()
	bearer := auth.Bearer{Provider: api.Tokens, Resource: resource, TenantID: api.TenantID}
	client, err := httpx.New(httpx.Options{BaseURL: resource, Name: "Defender for Endpoint", HTTPClient: api.HTTPClient, MaxAttempts: 1,
		Token: &httpx.TokenSource{Token: bearer.Token, Refresh: bearer.Refresh}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
