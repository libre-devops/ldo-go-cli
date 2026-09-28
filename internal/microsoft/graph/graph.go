// Package graph reads Microsoft Graph directly: any GET, paged; objects by name or id;
// and Advanced Hunting.
//
// The feature packages (entra, intune, incidents, pim) each read the part of Graph they
// need. This one is the general tool: read any path, as az rest would, but knowing
// Graph's paging, its query options and the ConsistencyLevel its advanced queries want.
// It only reads: the one POST, runHuntingQuery, runs a query and changes nothing.
package graph

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Versions are the Graph API versions a path may start with.
var Versions = []string{"v1.0", "beta"}

// HuntHint is what a refused hunting query needs.
var HuntHint = "Advanced Hunting through Graph needs ThreatHunting.Read.All on the token, which the " +
	"Azure CLI's token never carries: use an interactive or device-code profile whose app " +
	"has it (or " + brand.Suggest("xdr hunt --endpoint") + ", for the device tables with the " +
	"Azure CLI's sign-in)"

// Requirements are what this package's calls need from a token.
var Requirements = []microsoft.Requirement{
	{Feature: "hunting (xdr hunt, xdr timeline, devices av-signature)", Resource: "graph",
		AllOf: [][]string{{"ThreatHunting.Read.All"}}},
}

var unsafe = regexp.MustCompile(`[\s\\]|\.\.`)

// Kind is a kind of object, its collection, and what it is looked up by besides its id.
type Kind struct {
	Name       string
	Collection string
	Fields     []string
	// OtherID is the other id people use for it (a device's deviceId, an app's appId).
	OtherID string
}

// Kinds are the kinds of object Lookup finds, in order.
var Kinds = []Kind{
	{"user", "users", []string{"userPrincipalName", "displayName", "mail"}, ""},
	{"device", "devices", []string{"displayName"}, "deviceId"},
	{"group", "groups", []string{"displayName", "mailNickname"}, ""},
	{"app", "applications", []string{"displayName"}, "appId"},
	{"sp", "servicePrincipals", []string{"displayName"}, "appId"},
}

// KindNames are the kinds' names.
func KindNames() []string {
	names := make([]string, len(Kinds))
	for index, kind := range Kinds {
		names[index] = kind.Name
	}
	return names
}

// Page is what a collection GET returned: its items, @odata.count when asked for, and
// More when a next page exists that was not fetched.
type Page struct {
	Items []fields.Object
	Count *int
	More  bool
}

// Client reads Microsoft Graph for one tenant.
type Client struct {
	API *httpx.Client
}

// New is a Graph client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// Params are a query's options; empty ones are left out.
func Params(pairs ...string) url.Values {
	values := url.Values{}
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			values.Set(pairs[index], pairs[index+1])
		}
	}
	return values
}

func headers(eventual bool) map[string]string {
	if eventual {
		return map[string]string{"ConsistencyLevel": "eventual"}
	}
	return nil
}

// Get is one GET of path (users, /beta/me, or a full Graph URL).
func (c *Client) Get(ctx context.Context, path string, params url.Values, beta, eventual bool) (fields.Object, error) {
	address, err := Path(path, beta)
	if err != nil {
		return nil, err
	}
	return c.API.Do(ctx, http.MethodGet, address, httpx.Call{Params: params, Headers: headers(eventual)})
}

// PageOptions are how much of a collection Page reads: the first page, up to Limit
// items (0 for no limit), or every page.
type PageOptions struct {
	Beta     bool
	Eventual bool
	Limit    int
	All      bool
}

// Page is a collection's items: the first page, up to a limit, or every page.
func (c *Client) Page(ctx context.Context, path string, params url.Values, opts PageOptions) (Page, error) {
	data, err := c.Get(ctx, path, params, opts.Beta, opts.Eventual)
	if err != nil {
		return Page{}, err
	}
	if _, ok := data["value"].([]any); !ok {
		return Page{}, errs.Inputf("%s is a single object, not a collection", path)
	}
	var page Page
	if count, ok := fields.Number(data["@odata.count"]); ok {
		whole := int(count)
		page.Count = &whole
	}
	for {
		page.Items = append(page.Items, fields.Objects(data["value"])...)
		link, _ := data["@odata.nextLink"].(string)
		if opts.Limit > 0 && len(page.Items) >= opts.Limit {
			page.More = link != "" || len(page.Items) > opts.Limit
			page.Items = page.Items[:opts.Limit]
			return page, nil
		}
		if link == "" {
			return page, nil
		}
		if !opts.All && opts.Limit == 0 {
			page.More = true
			return page, nil
		}
		data, err = c.API.Do(ctx, http.MethodGet, link, httpx.Call{Headers: headers(opts.Eventual)})
		if err != nil {
			return Page{}, err
		}
	}
}

// KindOf is the kind named, or an Input error listing the kinds.
func KindOf(name string) (Kind, error) {
	for _, kind := range Kinds {
		if kind.Name == name {
			return kind, nil
		}
	}
	return Kind{}, errs.Inputf("unknown kind %q: use one of %s", name, strings.Join(KindNames(), ", "))
}

// Lookup is every object of a kind that ref names: an id, or a name it goes by.
//
// Devices are tried by FQDN, then short host name, as elsewhere in this tool; a name can
// match several objects (stale device registrations keep the name).
func (c *Client) Lookup(ctx context.Context, kindName, ref, selectFields string) ([]fields.Object, error) {
	kind, err := KindOf(kindName)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errs.Inputf("no %s named", kind.Name)
	}
	if util.IsGUID(ref) {
		return c.byID(ctx, kind, ref, selectFields)
	}
	if kind.Name == "user" && strings.Contains(ref, "@") {
		found, err := c.Get(ctx, "users/"+url.PathEscape(ref), Params("$select", selectFields), false, false)
		if err == nil {
			return []fields.Object{found}, nil
		}
		if !notFound(err) {
			return nil, err
		}
	}
	names := []string{ref}
	if kind.Name == "device" {
		names = util.CandidateNames(ref)
	}
	for _, name := range names {
		var either []string
		for _, field := range kind.Fields {
			either = append(either, field+" eq "+util.ODataString(name))
		}
		page, err := c.Page(ctx, kind.Collection, Params("$select", selectFields, "$filter", strings.Join(either, " or ")),
			PageOptions{All: true})
		if err != nil {
			return nil, err
		}
		if len(page.Items) > 0 {
			return page.Items, nil
		}
	}
	return nil, nil
}

func notFound(err error) bool {
	found := errs.As(err)
	return found != nil && found.Status == http.StatusNotFound
}

func (c *Client) byID(ctx context.Context, kind Kind, ref, selectFields string) ([]fields.Object, error) {
	found, err := c.Get(ctx, kind.Collection+"/"+ref, Params("$select", selectFields), false, false)
	if err == nil {
		return []fields.Object{found}, nil
	}
	if !notFound(err) {
		return nil, err
	}
	// Not an object id: for these kinds, the other id people use.
	if kind.OtherID == "" {
		return nil, nil
	}
	page, err := c.Page(ctx, kind.Collection,
		Params("$select", selectFields, "$filter", kind.OtherID+" eq "+util.ODataString(ref)), PageOptions{})
	return page.Items, err
}

// Me is the signed-in user (delegated tokens only).
func (c *Client) Me(ctx context.Context) (fields.Object, error) {
	return c.Get(ctx, "me", Params("$select", "id,displayName,userPrincipalName,mail,jobTitle,department"), false, false)
}

// ServicePrincipal is the service principal an app-only token belongs to, or nil.
func (c *Client) ServicePrincipal(ctx context.Context, appID string) (fields.Object, error) {
	if !util.IsGUID(appID) {
		return nil, nil
	}
	page, err := c.Page(ctx, "servicePrincipals",
		Params("$filter", "appId eq "+util.ODataString(appID), "$select", "id,displayName,appId"), PageOptions{})
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	return page.Items[0], nil
}

// Hunt runs an Advanced Hunting (KQL) query over the whole Defender XDR schema.
func (c *Client) Hunt(ctx context.Context, query string, timespan time.Duration) (render.QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return render.QueryResult{}, errs.Inputf("the hunting query is empty")
	}
	body := map[string]any{"Query": query}
	if timespan != 0 {
		duration, err := ISODuration(timespan)
		if err != nil {
			return render.QueryResult{}, err
		}
		body["Timespan"] = duration
	}
	data, err := c.API.Post(ctx, "/v1.0/security/runHuntingQuery", body, nil)
	if err != nil {
		return render.QueryResult{}, huntError(err)
	}
	var columns []string
	for _, column := range fields.Objects(data["schema"]) {
		columns = append(columns, fields.Text(column, "name"))
	}
	rows := fields.Objects(data["results"])
	records := make([]map[string]any, len(rows))
	copy(records, rows)
	if len(columns) == 0 {
		return render.FromRecords(records, false), nil
	}
	return render.QueryResult{Columns: columns, Rows: records}, nil
}

// huntError gives a refused query the hint for the permission it needs; a suspended
// service answers 403 too, and its own hint says why better.
func huntError(err error) error {
	found := errs.As(err)
	if found == nil || (found.Status != 401 && found.Status != 403) ||
		strings.Contains(strings.ToLower(found.Message), "suspended") {
		return err
	}
	copied := *found
	copied.Hint = HuntHint
	return &copied
}

// Path is path as Graph wants it: users is /v1.0/users, beta/me is kept as it is.
//
// A full Graph URL (a nextLink, or one pasted from Graph Explorer) passes through; the
// API client refuses any other host, so the token cannot be sent elsewhere.
func Path(path string, beta bool) (string, error) {
	text := strings.TrimSpace(path)
	if text == "" {
		return "", errs.Inputf("no Graph path given").WithHint("for example: users, me, devices")
	}
	if strings.Contains(text, "://") {
		return text, nil
	}
	base, _, _ := strings.Cut(text, "?")
	base, _, _ = strings.Cut(base, "#")
	base = strings.TrimLeft(base, "/")
	if unsafe.MatchString(base) {
		return "", errs.Inputf("%q is not a Graph path", path)
	}
	first, _, _ := strings.Cut(base, "/")
	for _, version := range Versions {
		if first == version {
			if beta && first != "beta" {
				return "", errs.Inputf("--beta and a v1.0 path disagree")
			}
			return "/" + strings.TrimLeft(text, "/"), nil
		}
	}
	version := "v1.0"
	if beta {
		version = "beta"
	}
	return "/" + version + "/" + strings.TrimLeft(text, "/"), nil
}

// ISODuration is an ISO 8601 duration: P7D, PT6H, PT30M, PT45S.
func ISODuration(span time.Duration) (string, error) {
	seconds := int64(span / time.Second)
	switch {
	case seconds <= 0:
		return "", errs.Inputf("a timespan must be positive")
	case seconds%86400 == 0:
		return "P" + itoa(seconds/86400) + "D", nil
	case seconds%3600 == 0:
		return "PT" + itoa(seconds/3600) + "H", nil
	case seconds%60 == 0:
		return "PT" + itoa(seconds/60) + "M", nil
	}
	return "PT" + itoa(seconds) + "S", nil
}

// NotFound is a NotFound error naming what was tried; for a device, both its FQDN and
// short name.
func NotFound(kind, ref string) error {
	tried := quote(ref)
	if kind == "device" {
		var names []string
		for _, name := range util.CandidateNames(ref) {
			names = append(names, quote(name))
		}
		tried = strings.Join(names, " or ")
	}
	return errs.NotFoundf("no %s is named %s", kind, tried)
}

func quote(text string) string { return "'" + text + "'" }

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
