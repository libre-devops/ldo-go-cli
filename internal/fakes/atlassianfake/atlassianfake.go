// Package atlassianfake is a fake Atlassian Cloud site: Jira's and Confluence's REST APIs,
// paged as the real ones page them, refusing any request without the right email and API
// token.
package atlassianfake

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// The site and the account the fake knows.
const (
	Site  = "https://contoso.atlassian.net"
	Email = "ana@corp.example"
	Token = "atl-token"
)

// Env is the environment that makes a profile for the site.
var Env = map[string]string{"JIRA_INSTANCE": Site, "JIRA_EMAIL": Email, "JIRA_TOKEN": Token}

// Object is a JSON object.
type Object = map[string]any

// IssueRecord is an issue as Jira returns it.
func IssueRecord(key, summary, status, category string, assigned bool) Object {
	number, _ := strconv.Atoi(strings.SplitN(key, "-", 2)[1])
	found := Object{
		"summary": summary, "status": Object{"name": status, "statusCategory": Object{"name": category}},
		"issuetype": Object{"name": "Task"}, "priority": Object{"name": "Medium"}, "assignee": nil,
		"reporter": Object{"displayName": "Ben"}, "project": Object{"key": strings.SplitN(key, "-", 2)[0]},
		"labels": []any{"linux"}, "created": "2026-09-20T09:00:00.000+0000", "updated": "2026-09-25T09:00:00.000+0000",
	}
	if assigned {
		found["assignee"] = Object{"displayName": "Ana"}
	}
	return Object{"id": strconv.Itoa(10000 + number), "key": key, "fields": found}
}

// PageRecord is a page as Confluence returns it.
func PageRecord(id, title, spaceID string) Object {
	return Object{"id": id, "title": title, "spaceId": spaceID, "status": "current",
		"version": Object{"number": 3, "createdAt": "2026-09-24T10:00:00.000Z"},
		"_links":  Object{"webui": "/spaces/OPS/pages/" + id + "/" + strings.ReplaceAll(title, " ", "+")}}
}

// Fake is Jira and Confluence for one site, a few records of each.
type Fake struct {
	mu          sync.Mutex
	Issues      []Object
	Description string
	Spaces      []Object
	Pages       []Object
	Body        string
}

// New is a site with five issues, two spaces and three pages.
func New() *Fake {
	fake := &Fake{
		Description: "<p>Patch <strong>web01</strong> tonight.</p><ul><li>drain</li></ul>",
		Spaces: []Object{
			{"id": "1", "key": "OPS", "name": "Operations", "type": "global", "status": "current", "_links": Object{"webui": "/spaces/OPS"}},
			{"id": "2", "key": "~acc1", "name": "Ana", "type": "personal", "status": "current", "_links": Object{"webui": "/spaces/~acc1"}},
		},
		Pages: []Object{PageRecord("101", "Runbook", "1"), PageRecord("102", "On-call", "1"), PageRecord("201", "Notes", "2")},
		Body:  "<h2>Restart</h2><p>Run <code>systemctl restart app</code>.</p>",
	}
	for number := 1; number <= 5; number++ {
		fake.Issues = append(fake.Issues, IssueRecord(fmt.Sprintf("OPS-%d", number), fmt.Sprintf("Patch web0%d", number), "To Do", "To Do", true))
	}
	fake.Issues[1] = IssueRecord("OPS-2", "Rotate keys", "In Progress", "In Progress", false)
	return fake
}

// Handler answers one request.
func (f *Fake) Handler(request *http.Request) httpfake.Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(Email+":"+Token))
	if request.Header.Get("Authorization") != expected {
		return httpfake.Status(401, Object{"errorMessages": []any{"Client must be authenticated to access this resource."}})
	}
	query := map[string]string{}
	for key, values := range request.URL.Query() {
		query[key] = values[0]
	}
	path := request.URL.Path
	if rest, ok := strings.CutPrefix(path, "/rest/api/3"); ok {
		return f.jira(rest, query)
	}
	if rest, ok := strings.CutPrefix(path, "/wiki"); ok {
		return f.confluence(rest, query)
	}
	return httpfake.Status(404, Object{"errorMessages": []any{"unexpected Atlassian request " + path}})
}

func (f *Fake) jira(path string, query map[string]string) httpfake.Reply {
	switch {
	case path == "/myself":
		return httpfake.JSON(Object{"accountId": "acc1", "displayName": "Ana", "emailAddress": Email, "active": true, "timeZone": "Europe/London"})
	case path == "/search/jql":
		return f.search(query)
	case strings.HasPrefix(path, "/issue/"):
		key := strings.TrimPrefix(path, "/issue/")
		for _, issue := range f.Issues {
			if issue["key"] == key {
				found := Object{"renderedFields": Object{"description": f.Description}}
				for name, value := range issue {
					found[name] = value
				}
				return httpfake.JSON(found)
			}
		}
		return httpfake.Status(404, Object{"errorMessages": []any{"Issue does not exist or you do not have permission to see it."}})
	case path == "/project/search":
		start, _ := strconv.Atoi(query["startAt"])
		projects := []Object{
			{"id": "1", "key": "OPS", "name": "Operations", "projectTypeKey": "software"},
			{"id": "2", "key": "SEC", "name": "Security", "projectTypeKey": "business"},
		}
		// One a page, to be paged through.
		return httpfake.JSON(Object{"values": projects[start : start+1], "isLast": start+1 >= len(projects), "startAt": start})
	}
	return httpfake.Status(404, Object{"errorMessages": []any{"unexpected Jira path " + path}})
}

func (f *Fake) search(query map[string]string) httpfake.Reply {
	if strings.HasPrefix(strings.ToLower(query["jql"]), "order by") {
		return httpfake.Status(400, Object{"errorMessages": []any{"Unbounded JQL queries are not allowed here."}})
	}
	start, _ := strconv.Atoi(query["nextPageToken"])
	size, err := strconv.Atoi(query["maxResults"])
	if err != nil {
		size = 50
	}
	end := min(start+size, len(f.Issues))
	body := Object{"issues": f.Issues[start:end], "isLast": start+size >= len(f.Issues)}
	if start+size < len(f.Issues) {
		body["nextPageToken"] = strconv.Itoa(start + size)
	}
	return httpfake.JSON(body)
}

func (f *Fake) confluence(path string, query map[string]string) httpfake.Reply {
	switch {
	case path == "/api/v2/spaces":
		if key, ok := query["keys"]; ok {
			found := []Object{}
			for _, space := range f.Spaces {
				if space["key"] == key {
					found = append(found, space)
				}
			}
			return httpfake.JSON(Object{"results": found})
		}
		if query["cursor"] == "2" {
			return httpfake.JSON(Object{"results": f.Spaces[1:], "_links": Object{}})
		}
		return httpfake.JSON(Object{"results": f.Spaces[:1], "_links": Object{"next": "/wiki/api/v2/spaces?cursor=2"}})
	case strings.HasPrefix(path, "/api/v2/spaces/") && strings.HasSuffix(path, "/pages"):
		space := strings.Split(path, "/")[4]
		var inSpace []Object
		for _, page := range f.Pages {
			if page["spaceId"] == space {
				inSpace = append(inSpace, page)
			}
		}
		return httpfake.JSON(Object{"results": titled(inSpace, query)})
	case path == "/api/v2/pages":
		return httpfake.JSON(Object{"results": titled(f.Pages, query)})
	case strings.HasPrefix(path, "/api/v2/pages/"):
		return f.page(strings.TrimPrefix(path, "/api/v2/pages/"))
	case path == "/rest/api/search":
		hit := Object{"content": Object{"id": "101", "type": "page", "title": "Runbook"}, "title": "Runbook",
			"url": "/spaces/OPS/pages/101/Runbook", "resultGlobalContainer": Object{"title": "Operations"},
			"lastModified": "2026-09-24T10:00:00.000Z", "excerpt": "restart the\n app"}
		return httpfake.JSON(Object{"results": []Object{hit}, "_links": Object{}})
	}
	return httpfake.Status(404, Object{"errors": []any{Object{"title": "Not Found", "detail": "unexpected Confluence path " + path}}})
}

func (f *Fake) page(id string) httpfake.Reply {
	for _, page := range f.Pages {
		if page["id"] == id {
			found := Object{"body": Object{"storage": Object{"representation": "storage", "value": f.Body}}}
			for name, value := range page {
				found[name] = value
			}
			return httpfake.JSON(found)
		}
	}
	return httpfake.Status(404, Object{"errors": []any{Object{"status": 404, "title": "Not Found", "detail": "no such page"}}})
}

func titled(pages []Object, query map[string]string) []Object {
	title, wanted := query["title"]
	found := []Object{}
	for _, page := range pages {
		if !wanted || page["title"] == title {
			found = append(found, page)
		}
	}
	return found
}
