// Package confluence is Confluence Cloud: spaces, pages (their bodies as Markdown) and CQL
// search, as the account whose API token the profile names. Spaces and pages come from
// the REST API's v2, search from v1. It needs nothing past the token: it reads what its
// account may, in Confluence's own permissions.
package confluence

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

var (
	pageID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	// A space's key, or a personal space's: ~ and the account's id.
	spaceKey = regexp.MustCompile(`^(?:[A-Za-z0-9_]{1,255}|~[A-Za-z0-9_:-]{1,128})$`)
)

func link(site string, data httpx.Object) string {
	if webui := fields.Text(fields.Map(data["_links"]), "webui"); webui != "" {
		return site + "/wiki" + webui
	}
	return ""
}

// Space is one Confluence space.
type Space struct {
	ID     string
	Key    string
	Name   string
	Type   string
	Status string
	URL    string
}

// SpaceFrom is a space from Confluence's JSON (v2); site makes its link.
func SpaceFrom(data httpx.Object, site string) Space {
	return Space{ID: fields.Text(data, "id"), Key: fields.Text(data, "key"), Name: fields.Text(data, "name"),
		Type: fields.Text(data, "type"), Status: fields.Text(data, "status"), URL: link(site, data)}
}

// Page is one page: its title, where it is, its version, and its body as Markdown when
// read.
type Page struct {
	ID      string
	Title   string
	SpaceID string
	Status  string
	Version int
	Updated time.Time
	URL     string
	Body    string
}

// PageFrom is a page from Confluence's JSON (v2); site makes its link.
func PageFrom(data httpx.Object, site, body string) Page {
	version := fields.Map(data["version"])
	return Page{ID: fields.Text(data, "id"), Title: fields.Text(data, "title"), SpaceID: fields.Text(data, "spaceId"),
		Status: fields.Text(data, "status"), Version: fields.Int(version["number"]), Updated: fields.When(version, "createdAt"),
		URL: link(site, data), Body: body}
}

// SearchHit is one thing a CQL search found: a page, a blog post, an attachment, a space.
type SearchHit struct {
	ID      string
	Type    string
	Title   string
	Space   string
	Updated time.Time
	URL     string
	Excerpt string
}

// SearchHitFrom is a result from Confluence's search (v1); site makes its link.
func SearchHitFrom(data httpx.Object, site string) SearchHit {
	content := fields.Map(data["content"])
	hit := SearchHit{ID: fields.Text(content, "id"), Type: fields.Text(content, "type"), Title: fields.Text(data, "title"),
		Space: fields.Text(fields.Map(data["resultGlobalContainer"]), "title"), Updated: fields.When(data, "lastModified"),
		Excerpt: strings.Join(strings.Fields(fields.Text(data, "excerpt")), " ")}
	if hit.Type == "" {
		hit.Type = fields.Text(data, "entityType")
	}
	if hit.Title == "" {
		hit.Title = fields.Text(content, "title")
	}
	if path := fields.Text(data, "url"); path != "" {
		hit.URL = site + "/wiki" + path
	}
	return hit
}

// Client reads one site's Confluence as one account.
type Client struct {
	API *httpx.Client
}

// New is a Confluence client for the profile's site.
func New(profile atlassian.Profile, getenv func(string) string, client *http.Client) (*Client, error) {
	api, err := atlassian.NewClient("Confluence", profile, getenv, client)
	if err != nil {
		return nil, err
	}
	return &Client{API: api}, nil
}

// Site is the site's address, for links.
func (c *Client) Site() string { return c.API.BaseURL() }

// Spaces are every space the account can see.
func (c *Client) Spaces(ctx context.Context) ([]Space, error) {
	found := []Space{}
	err := c.each(ctx, "/wiki/api/v2/spaces", url.Values{"limit": {"250"}}, func(item httpx.Object) bool {
		found = append(found, SpaceFrom(item, c.Site()))
		return true
	})
	return found, err
}

// Space is the space called key, or a NotFound error.
func (c *Client) Space(ctx context.Context, key string) (Space, error) {
	wanted := strings.TrimSpace(key)
	if !spaceKey.MatchString(wanted) {
		return Space{}, errs.Inputf("'%s' is not a space key", key).WithHint("e.g. OPS, or ~ and an id")
	}
	page, err := c.API.Get(ctx, "/wiki/api/v2/spaces", url.Values{"keys": {wanted}})
	if err != nil {
		return Space{}, err
	}
	results := fields.Objects(page["results"])
	if len(results) == 0 {
		return Space{}, errs.NotFoundf("no space '%s' that this account can see", wanted)
	}
	return SpaceFrom(results[0], c.Site()), nil
}

// Pages are pages, newest change first: in space (its key) and called title, when given.
func (c *Client) Pages(ctx context.Context, space, title string, limit int) ([]Page, error) {
	if limit < 1 {
		return nil, errs.Inputf("limit must be at least 1")
	}
	params := url.Values{"limit": {strconv.Itoa(min(limit, 250))}, "sort": {"-modified-date"}}
	if title != "" {
		params.Set("title", title)
	}
	path := "/wiki/api/v2/pages"
	if space != "" {
		found, err := c.Space(ctx, space)
		if err != nil {
			return nil, err
		}
		path = "/wiki/api/v2/spaces/" + url.PathEscape(found.ID) + "/pages"
	}
	found := []Page{}
	err := c.each(ctx, path, params, func(item httpx.Object) bool {
		found = append(found, PageFrom(item, c.Site(), ""))
		return len(found) < limit
	})
	return found, err
}

// Page is one page, with its body as Markdown.
func (c *Client) Page(ctx context.Context, id string) (Page, error) {
	wanted := strings.TrimSpace(id)
	if !pageID.MatchString(wanted) {
		return Page{}, errs.Inputf("'%s' is not a page id", id).WithHint("the number in its link")
	}
	data, err := c.API.Get(ctx, "/wiki/api/v2/pages/"+wanted, url.Values{"body-format": {"storage"}})
	if err != nil {
		return Page{}, err
	}
	body := ""
	if html := fields.Text(fields.Map(fields.Map(data["body"])["storage"]), "value"); html != "" {
		body = strings.TrimSpace(markdown.FromHTML(html))
	}
	return PageFrom(data, c.Site(), body), nil
}

// Search is what the CQL query cql finds, up to limit.
func (c *Client) Search(ctx context.Context, cql string, limit int) ([]SearchHit, error) {
	if limit < 1 {
		return nil, errs.Inputf("limit must be at least 1")
	}
	found := []SearchHit{}
	err := c.each(ctx, "/wiki/rest/api/search", url.Values{"cql": {cql}, "limit": {strconv.Itoa(min(limit, 100))}}, func(item httpx.Object) bool {
		found = append(found, SearchHitFrom(item, c.Site()))
		return len(found) < limit
	})
	return found, err
}

// each calls visit with every item of a paged list, until it says to stop: Confluence's
// _links.next is a path on the site.
func (c *Client) each(ctx context.Context, path string, params url.Values, visit func(httpx.Object) bool) error {
	page, err := c.API.Get(ctx, path, params)
	for err == nil {
		for _, item := range fields.Objects(page["results"]) {
			if !visit(item) {
				return nil
			}
		}
		following := fields.Text(fields.Map(page["_links"]), "next")
		if !strings.HasPrefix(following, "/") || strings.HasPrefix(following, "//") {
			return nil
		}
		page, err = c.API.Get(ctx, following, nil)
	}
	return err
}
