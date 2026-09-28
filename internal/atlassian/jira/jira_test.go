package jira

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/atlassianfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

var ctx = context.Background()

func client(t *testing.T, site *atlassianfake.Fake) (*Client, *httpfake.Transport) {
	t.Helper()
	http, transport := httpfake.Client(site.Handler)
	profile := atlassian.Profile{Name: "env", Site: atlassianfake.Site, Email: atlassianfake.Email, TokenEnv: atlassian.TokenEnv}
	found, err := New(profile, func(name string) string { return atlassianfake.Env[name] }, http)
	if err != nil {
		t.Fatal(err)
	}
	return found, transport
}

func TestIssuesArePagedByTokenUpToTheLimit(t *testing.T) {
	jira, transport := client(t, atlassianfake.New())
	found, err := jira.Issues(ctx, "project = OPS", 4)
	if err != nil || len(found) != 4 {
		t.Fatal(found, err)
	}
	if len(transport.Seen()) != 1 || transport.Seen()[0].URL.Query().Get("maxResults") != "4" {
		t.Error(transport.Paths())
	}
	every, _ := jira.Issues(ctx, DefaultJQL, 50)
	second := every[1]
	if len(every) != 5 || second.Key != "OPS-2" || second.Status != "In Progress" || second.StatusCategory != "In Progress" ||
		second.Assignee != "" || second.Reporter != "Ben" || second.Project != "OPS" || strings.Join(second.Labels, ",") != "linux" ||
		second.URL != "https://contoso.atlassian.net/browse/OPS-2" || second.Updated.Format(time.RFC3339) != "2026-09-25T09:00:00Z" {
		t.Errorf("%+v", second)
	}
	if _, err := jira.Issues(ctx, "", 0); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestPagesAreFollowedUntilTheLast(t *testing.T) {
	site := atlassianfake.New()
	for number := 6; number <= 130; number++ {
		site.Issues = append(site.Issues, atlassianfake.IssueRecord(fmt.Sprintf("OPS-%d", number), "more", "To Do", "To Do", true))
	}
	jira, transport := client(t, site)
	found, err := jira.Issues(ctx, DefaultJQL, 120)
	if err != nil || len(found) != 120 || len(transport.Seen()) != 2 || transport.Seen()[1].URL.Query().Get("nextPageToken") != "100" {
		t.Error(len(found), err, transport.Paths())
	}
}

func TestAnUnboundedQueryIsRefusedByJira(t *testing.T) {
	jira, _ := client(t, atlassianfake.New())
	if _, err := jira.Issues(ctx, "ORDER BY created", 5); err == nil || !strings.Contains(err.Error(), "Unbounded JQL") {
		t.Error(err)
	}
}

func TestAnIssueCarriesItsDescriptionAsMarkdown(t *testing.T) {
	jira, _ := client(t, atlassianfake.New())
	issue, err := jira.Issue(ctx, " ops-1 ")
	if err != nil || issue.Key != "OPS-1" || issue.Description != "Patch **web01** tonight.\n\n- drain" || issue.Raw["key"] != "OPS-1" {
		t.Fatalf("%+v %v", issue, err)
	}
	for _, bad := range []string{"OPS", "OPS-0", "OPS-1/../x"} {
		if _, err := jira.Issue(ctx, bad); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := jira.Issue(ctx, "OPS-99"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Error(err)
	}
}

func TestProjectsAreEveryPage(t *testing.T) {
	jira, _ := client(t, atlassianfake.New())
	found, err := jira.Projects(ctx)
	if err != nil || len(found) != 2 || found[0].Key != "OPS" || found[1].Type != "business" || found[1].URL != "https://contoso.atlassian.net/browse/SEC" {
		t.Error(found, err)
	}
	me, _ := jira.Whoami(ctx)
	if me["displayName"] != "Ana" || jira.Site() != atlassianfake.Site {
		t.Error(me)
	}
}
