package automation

import (
	"context"
	"net/http"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/armfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var ops = armfake.AutomationAccount("rg-ops", "aa-ops")

func client(t *testing.T) (*Client, *httpfake.Transport) {
	t.Helper()
	account := ops["id"].(string)
	httpClient, transport := httpfake.Client(httpfake.Routes(
		httpfake.Route{Match: "GET /subscriptions/" + armfake.Subscription + "/providers/Microsoft.Automation", Reply: httpfake.JSON(armfake.Page("",
			ops, armfake.AutomationAccount("rg-dev", "aa-dev"), armfake.AutomationAccount("rg-b", "twice"), armfake.AutomationAccount("rg-a", "twice")))},
		httpfake.Route{Match: "GET " + account + "/jobs/j2/streams/s2", Reply: httpfake.JSON(map[string]any{"properties": map[string]any{
			"jobStreamId": "s2", "streamType": "Error", "summary": "boom", "streamText": "boom, in full"}})},
		httpfake.Route{Match: "GET " + account + "/jobs/j2/streams", Reply: httpfake.JSON(armfake.Page("",
			map[string]any{"properties": map[string]any{"jobStreamId": "s2", "time": "2026-09-24T10:05:00Z", "streamType": "Error", "summary": "boom"}},
			map[string]any{"properties": map[string]any{"jobStreamId": "s1", "time": "2026-09-24T10:01:00Z", "streamType": "Output", "summary": "hello"}}))},
		httpfake.Route{Match: "GET " + account + "/jobs/j2/output", Func: func(r *http.Request) httpfake.Reply {
			if r.Header.Get("Accept") != "text/plain" {
				t.Error(r.Header)
			}
			return httpfake.Reply{Status: 200, Body: httpfake.Text{Content: "hello\n"}}
		}},
		httpfake.Route{Match: "GET " + account + "/jobs/j9", Reply: httpfake.Status(404, nil)},
		httpfake.Route{Match: "GET " + account + "/jobs/j2", Reply: httpfake.JSON(armfake.Job("j2", "Patch", "Failed", "2026-09-24T10:00:00Z", ""))},
		httpfake.Route{Match: "GET " + account + "/jobs", Reply: httpfake.JSON(armfake.Page("",
			armfake.Job("j1", "Patch", "Completed", "2026-09-23T10:00:00Z", "2026-09-23T10:02:00Z"),
			armfake.Job("j2", "Patch", "Failed", "2026-09-24T10:00:00Z", "")))},
		httpfake.Route{Match: "GET " + account, Reply: httpfake.JSON(ops)},
	))
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: armfake.Tenant, Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport
}

func TestAccountsByNameOrID(t *testing.T) {
	c, _ := client(t)
	scope := []string{armfake.Subscription}
	accounts, err := c.Accounts(context.Background(), scope)
	if err != nil || accounts[0].Name != "aa-dev" || accounts[2].ResourceGroup != "rg-a" {
		t.Errorf("%+v %v", accounts, err)
	}
	if found, err := c.FindAccount(context.Background(), "AA-OPS", scope, ""); err != nil || found.ResourceGroup != "rg-ops" {
		t.Errorf("%+v %v", found, err)
	}
	if found, err := c.FindAccount(context.Background(), ops["id"].(string), nil, ""); err != nil || found.Name != "aa-ops" {
		t.Errorf("%+v %v", found, err)
	}
	if found, err := c.FindAccount(context.Background(), "twice", scope, "rg-b"); err != nil || found.ResourceGroup != "rg-b" {
		t.Errorf("%+v %v", found, err)
	}
	for ref, kind := range map[string]errs.Kind{"twice": errs.Ambiguous, "none": errs.NotFound, "bad name!": errs.Input,
		armfake.Group("rg") + "/providers/Microsoft.KeyVault/vaults/kv": errs.Input} {
		if _, err := c.FindAccount(context.Background(), ref, scope, ""); !errs.Is(err, kind) {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

func TestJobsStreamsAndOutput(t *testing.T) {
	c, _ := client(t)
	account := AccountFrom(ops)
	jobs, err := c.Jobs(context.Background(), account)
	if err != nil || jobs[0].ID != "j2" || !jobs[0].IsFailed() || jobs[1].IsFailed() {
		t.Errorf("%+v %v", jobs, err)
	}
	streams, _ := c.Streams(context.Background(), account, "j2")
	if streams[0].ID != "s1" || streams[1].Message() != "boom" {
		t.Errorf("%+v", streams)
	}
	full, _ := c.Stream(context.Background(), account, "j2", "s2")
	if full.Message() != "boom, in full" {
		t.Error(full)
	}
	if text, err := c.Output(context.Background(), account, "j2"); err != nil || text != "hello\n" {
		t.Errorf("%q %v", text, err)
	}
	if _, err := c.Job(context.Background(), account, "../etc"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Job(context.Background(), account, "j9"); !errs.Is(err, errs.NotFound) {
		t.Errorf("%v", err)
	}
	if _, err := c.Stream(context.Background(), account, "j2", "a/b"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}
