package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/atlassianfake"
)

// atlassianHarness is a harness whose HTTP goes to a fake site, with its profile in the
// environment.
func atlassianHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, atlassianfake.New().Handler)
	for key, value := range atlassianfake.Env {
		h.env[key] = value
	}
	return h
}

func TestJiraWhoamiSaysTheAccountAndSite(t *testing.T) {
	h := atlassianHarness(t)
	contains(t, h.ok("jira", "whoami"), "Profile     env", "Name        Ana", "Account id  acc1", "Active      yes")
	var record map[string]any
	_ = json.Unmarshal([]byte(h.ok("jira", "whoami", "-o", "json")), &record)
	if record["profile"] != "env" || record["site"] != atlassianfake.Site || record["active"] != true || record["time_zone"] != "Europe/London" {
		t.Error(record)
	}
	contains(t, h.ok("jira", "whoami", "-o", "csv"), "PROFILE,SITE,NAME,EMAIL,ACCOUNT ID,ACTIVE\nenv,")
}

func TestJiraIssuesComeFromAQueryOrAProject(t *testing.T) {
	h := atlassianHarness(t)
	lines := strings.Split(h.ok("jira", "issues", "--project", "ops", "-n", "2", "-o", "csv"), "\n")
	if lines[0] != "KEY,TYPE,STATUS,PRIORITY,ASSIGNEE,UPDATED,SUMMARY" || !strings.HasPrefix(lines[2], "OPS-2,Task,In Progress,Medium,-,") {
		t.Error(lines)
	}
	contains(t, h.err.String(), "2 issue(s) (profile env)")
	contains(t, h.transport.Seen()[0].URL.Query().Get("jql"), "project = OPS AND statusCategory != Done")
	h.ok("jira", "issues", "assignee = currentUser()")
	contains(t, h.transport.Seen()[len(h.transport.Seen())-1].URL.Query().Get("jql"), "assignee = currentUser()")
	contains(t, usageError(h.fails(2, "jira", "issues", "project = OPS", "--project", "OPS")), "put the project in the query instead")
	contains(t, usageError(h.fails(2, "jira", "issues", "--project", "OPS'--")), "is not a project key")
	contains(t, usageError(h.fails(2, "jira", "issues", "-n", "0")), "--limit")
	contains(t, h.fails(1, "jira", "issues", "ORDER BY created"), "Unbounded JQL")
}

func TestJiraIssueShowsItsDetailsThenItsDescription(t *testing.T) {
	h := atlassianHarness(t)
	out := h.ok("jira", "issue", "OPS-2")
	contains(t, out, "Link      https://contoso.atlassian.net/browse/OPS-2", "Assignee  unassigned", "Status    In Progress (In Progress)",
		"Created   2026-09-20 09:00:00")
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "Patch **web01** tonight.\n\n- drain") {
		t.Error(out)
	}
	if got := h.ok("jira", "issue", "OPS-1", "--markdown"); got != "Patch **web01** tonight.\n\n- drain\n" {
		t.Errorf("%q", got)
	}
	var record map[string]any
	_ = json.Unmarshal([]byte(h.ok("jira", "issue", "OPS-1", "-o", "json")), &record)
	if record["key"] != "OPS-1" || record["description"] != "Patch **web01** tonight.\n\n- drain" || record["created"] != "2026-09-20T09:00:00Z" {
		t.Error(record)
	}
	contains(t, h.fails(1, "jira", "issue", "not-a-key"), "is not an issue key", "e.g. OPS-123")
}

func TestJiraProjectsAndNoProfile(t *testing.T) {
	h := atlassianHarness(t)
	if lines := strings.Split(h.ok("jira", "projects", "-o", "csv"), "\n"); !strings.HasPrefix(lines[1], "OPS,Operations,") {
		t.Error(lines)
	}
	bare := newHarness(t, atlassianfake.New().Handler)
	contains(t, bare.fails(1, "jira", "projects"), "no Atlassian profile selected", "JIRA_INSTANCE")
	contains(t, bare.fails(1, "jira", "projects", "-p", "work"), `unknown Atlassian profile "work"`)
	bare.env["JIRA_INSTANCE"] = "contoso"
	contains(t, bare.fails(1, "jira", "projects"), "JIRA_EMAIL is not")
}

func TestAtlassianProfilesAreChosenByNameDefaultOrTheOnlyOne(t *testing.T) {
	h := atlassianHarness(t)
	delete(h.env, "JIRA_INSTANCE")
	h.config(testConfig + "\n[atlassian.profiles.work]\nsite = \"contoso\"\nemail = \"" + atlassianfake.Email + "\"\n")
	contains(t, h.ok("jira", "whoami"), "Profile     work")
	h.env["JIRA_INSTANCE"] = atlassianfake.Site
	contains(t, h.ok("jira", "whoami"), "Profile     env")
	contains(t, h.ok("jira", "whoami", "-p", "work"), "Profile     work")
	h.env["LDO_ATLASSIAN_PROFILE"] = "work"
	contains(t, h.ok("jira", "whoami"), "Profile     work")
	contains(t, h.fails(1, "jira", "whoami", "-p", "lab"), "configured: work")
	h.config(testConfig + "\n[atlassian]\ndefault_profile = \"work\"\n\n[atlassian.profiles.work]\nsite = \"https://your-site.atlassian.net\"\nemail = \"a@corp.example\"\n")
	contains(t, h.fails(1, "jira", "whoami"), "placeholder site")
	h.config(testConfig + "\n[atlassian]\ncolour = \"blue\"\n")
	contains(t, h.fails(1, "jira", "whoami"), "unknown key")
	delete(h.env, "JIRA_TOKEN")
	h.config(testConfig)
	delete(h.env, "LDO_ATLASSIAN_PROFILE")
	contains(t, h.fails(1, "confluence", "spaces"), "no Atlassian API token in JIRA_TOKEN")
}

func TestConfluenceSpacesAndTheirPages(t *testing.T) {
	h := atlassianHarness(t)
	if spaces := strings.Split(h.ok("confluence", "spaces", "-o", "csv"), "\n"); !strings.HasPrefix(spaces[1], "OPS,Operations,global,current,") || !strings.HasPrefix(spaces[2], "~acc1,") {
		t.Error(spaces)
	}
	pages := strings.Split(h.ok("confluence", "pages", "--space", "OPS", "-o", "csv"), "\n")
	if pages[0] != "ID,TITLE,SPACE,VERSION,UPDATED,LINK" || !strings.HasPrefix(pages[1], "101,Runbook,OPS,3,") {
		t.Error(pages)
	}
	var everywhere []map[string]any
	_ = json.Unmarshal([]byte(h.ok("confluence", "pages", "-o", "json")), &everywhere)
	spaces := map[any]bool{}
	for _, page := range everywhere {
		spaces[page["space"]] = true
	}
	if len(everywhere) != 3 || !spaces["OPS"] || !spaces["~acc1"] {
		t.Error(everywhere)
	}
	contains(t, h.ok("confluence", "pages", "--title", "Nothing"), "ID  TITLE")
	contains(t, usageError(h.fails(2, "confluence", "pages", "-n", "0")), "--limit")
	contains(t, h.fails(1, "confluence", "pages", "--space", "DEV"), "no space 'DEV'")
}

func TestConfluencePageIsItsDetailsThenItsBodyOrOnlyItsMarkdown(t *testing.T) {
	h := atlassianHarness(t)
	out := h.ok("confluence", "page", "101")
	contains(t, out, "Title    Runbook", "Version  3", "Updated  2026-09-24 10:00:00")
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "## Restart\n\nRun `systemctl restart app`.") {
		t.Error(out)
	}
	if got := h.ok("confluence", "page", "101", "--markdown"); !strings.HasPrefix(got, "## Restart") {
		t.Error(got)
	}
	var record map[string]any
	_ = json.Unmarshal([]byte(h.ok("confluence", "page", "101", "-o", "json")), &record)
	if record["title"] != "Runbook" || record["body"] != "## Restart\n\nRun `systemctl restart app`." || record["space"] != nil {
		t.Error(record)
	}
	contains(t, h.fails(1, "confluence", "page", "abc"), "is not a page id")
}

func TestConfluenceSearchFindsPagesByCQL(t *testing.T) {
	h := atlassianHarness(t)
	if lines := strings.Split(h.ok("confluence", "search", `type = page AND text ~ "restart"`, "-o", "csv"), "\n"); !strings.HasPrefix(lines[1], "page,Runbook,Operations,") {
		t.Error(lines)
	}
	contains(t, usageError(h.fails(2, "confluence", "search", "x", "-n", "0")), "--limit")
}
