// Package jira is Jira Cloud: search issues with JQL, read one (its description as
// Markdown), and list projects, as the account whose API token the profile names. It
// reads through the REST API (v3), and needs nothing past the token: it reads what its
// account may, in Jira's own permissions.
package jira

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/markdown"
)

// DefaultJQL is the issues not done: Jira refuses a query with no restriction at all, so
// none is ever sent without one.
const DefaultJQL = "statusCategory != Done ORDER BY updated DESC"

// Fields are the fields a search asks for.
const Fields = "summary,status,issuetype,priority,assignee,reporter,created,updated,labels,project"

// pageSize is the most Jira gives in one page of a search.
const pageSize = 100

var issueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[1-9][0-9]*$`)

// Issue is one Jira issue: its key, what it is, where it stands, and who has it.
type Issue struct {
	Key     string
	ID      string
	Summary string
	Type    string
	Status  string
	// StatusCategory is To Do, In Progress or Done, as Jira groups every status.
	StatusCategory string
	Priority       string
	Assignee       string
	Reporter       string
	Project        string
	Labels         []string
	Created        time.Time
	Updated        time.Time
	URL            string
	// Description is Markdown, when the issue was read on its own.
	Description string
	Raw         httpx.Object
}

// IssueFrom is an issue from Jira's JSON; site makes its link.
func IssueFrom(data httpx.Object, site, description string) Issue {
	found := fields.Map(data["fields"])
	status := fields.Map(found["status"])
	key := fields.Text(data, "key")
	return Issue{
		Key: key, ID: fields.Text(data, "id"), Summary: fields.Text(found, "summary"),
		Type: fields.Text(fields.Map(found["issuetype"]), "name"), Status: fields.Text(status, "name"),
		StatusCategory: fields.Text(fields.Map(status["statusCategory"]), "name"),
		Priority:       fields.Text(fields.Map(found["priority"]), "name"),
		Assignee:       fields.Text(fields.Map(found["assignee"]), "displayName"),
		Reporter:       fields.Text(fields.Map(found["reporter"]), "displayName"),
		Project:        fields.Text(fields.Map(found["project"]), "key"),
		Labels:         fields.Strings(found["labels"]), Created: fields.When(found, "created"), Updated: fields.When(found, "updated"),
		URL: site + "/browse/" + key, Description: description, Raw: data,
	}
}

// Project is one Jira project.
type Project struct {
	Key  string
	ID   string
	Name string
	Type string
	URL  string
}

// ProjectFrom is a project from Jira's JSON; site makes its link.
func ProjectFrom(data httpx.Object, site string) Project {
	key := fields.Text(data, "key")
	return Project{Key: key, ID: fields.Text(data, "id"), Name: fields.Text(data, "name"), Type: fields.Text(data, "projectTypeKey"),
		URL: site + "/browse/" + key}
}

// Client reads one site's Jira as one account.
type Client struct {
	API *httpx.Client
}

// New is a Jira client for the profile's site.
func New(profile atlassian.Profile, getenv func(string) string, client *http.Client) (*Client, error) {
	api, err := atlassian.NewClient("Jira", profile, getenv, client)
	if err != nil {
		return nil, err
	}
	return &Client{API: api}, nil
}

// Site is the site's address, for links.
func (c *Client) Site() string { return c.API.BaseURL() }

// Whoami is the account the token belongs to, as Jira has it.
func (c *Client) Whoami(ctx context.Context) (httpx.Object, error) {
	return c.API.Get(ctx, "/rest/api/3/myself", nil)
}

// Issues are the issues jql finds, in its order, up to limit.
func (c *Client) Issues(ctx context.Context, jql string, limit int) ([]Issue, error) {
	if limit < 1 {
		return nil, errs.Inputf("limit must be at least 1")
	}
	found := []Issue{}
	token := ""
	for len(found) < limit {
		params := url.Values{"jql": {jql}, "fields": {Fields}, "maxResults": {strconv.Itoa(min(pageSize, limit-len(found)))}}
		if token != "" {
			params.Set("nextPageToken", token)
		}
		page, err := c.API.Get(ctx, "/rest/api/3/search/jql", params)
		if err != nil {
			return nil, err
		}
		for _, item := range fields.Objects(page["issues"]) {
			found = append(found, IssueFrom(item, c.Site(), ""))
		}
		token = fields.String(page["nextPageToken"])
		if last, ok := page["isLast"].(bool); !ok || last || token == "" {
			break
		}
	}
	return found[:min(limit, len(found))], nil
}

// Issue is one issue, with its description as Markdown.
func (c *Client) Issue(ctx context.Context, key string) (Issue, error) {
	wanted := strings.ToUpper(strings.TrimSpace(key))
	if !issueKey.MatchString(wanted) {
		return Issue{}, errs.Inputf("'%s' is not an issue key", key).WithHint("e.g. OPS-123")
	}
	data, err := c.API.Get(ctx, "/rest/api/3/issue/"+wanted, url.Values{"fields": {Fields + ",description"}, "expand": {"renderedFields"}})
	if err != nil {
		return Issue{}, err
	}
	description := ""
	if html := fields.Text(fields.Map(data["renderedFields"]), "description"); html != "" {
		description = strings.TrimSpace(markdown.FromHTML(html))
	}
	return IssueFrom(data, c.Site(), description), nil
}

// Projects are every project the account can see.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	found := []Project{}
	start := 0
	for {
		page, err := c.API.Get(ctx, "/rest/api/3/project/search", url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"50"}})
		if err != nil {
			return nil, err
		}
		values := fields.Objects(page["values"])
		for _, item := range values {
			found = append(found, ProjectFrom(item, c.Site()))
		}
		start += len(values)
		if last, ok := page["isLast"].(bool); !ok || last || len(values) == 0 {
			return found, nil
		}
	}
}
