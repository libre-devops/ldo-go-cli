// Package news reads Microsoft 365 Message Center: the posts announcing what changes in
// the tenant's services, through Graph's admin/serviceAnnouncement/messages. It needs
// ServiceMessage.Read.All (delegated, or an application role): the Azure CLI's token has
// neither, so use a profile with your own app registration.
package news

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what Message Center needs from a Graph token.
var Requirements = []microsoft.Requirement{{Feature: "Message Center posts", Resource: "graph", AllOf: [][]string{{"ServiceMessage.Read.All"}}}}

// AdminCentre is where a post is opened, by those who are admins.
const AdminCentre = "https://admin.microsoft.com/#/MessageCenter/:/messages/"

// SecurityServices are the services a security team follows, as security-news filters
// Message Center by them: a post for any of these, or for one whose name holds one of
// them, is a security post.
var SecurityServices = []string{"Microsoft Defender", "Microsoft 365 Defender", "Microsoft Sentinel", "Microsoft Purview",
	"Microsoft Entra", "Microsoft Intune"}

// MessageKey finds a post's id in a task's title, as the task was raised for the post: at
// the start, as this raises them (MC1183010: ...), or in square brackets, as Microsoft's
// own Message Center sync to Planner writes them ([Service] Title [MC1183010]). The id is
// its first group.
var MessageKey = regexp.MustCompile(`(?i)(?:^|\[)(MC[0-9]+)`)

// Categories are Graph's categories, and how they read.
var Categories = [][2]string{{"planForChange", "plan for change"}, {"stayInformed", "stay informed"}, {"preventOrFixIssue", "prevent or fix issue"}}

// ScopeHint is what a refused listing needs.
const ScopeHint = "Message Center needs ServiceMessage.Read.All, which the Azure CLI's token lacks: use a profile with your own " +
	"app registration (-p)"

const (
	path  = "/v1.0/admin/serviceAnnouncement/messages"
	sel   = "id,title,category,severity,services,tags,isMajorChange,startDateTime,endDateTime,lastModifiedDateTime,actionRequiredByDateTime,body"
	idPat = `^MC[0-9]{1,12}$`
)

var messageID = regexp.MustCompile(idPat)

// Message is one Message Center post: what changes, for which services, and by when.
type Message struct {
	ID       string
	Title    string
	Category string
	Severity string
	Services []string
	Tags     []string
	Major    bool
	Starts   time.Time
	Ends     time.Time
	Updated  time.Time
	ActionBy time.Time
	BodyHTML string
}

// URL is the post in the Microsoft 365 admin centre, which only admins can open.
func (m Message) URL() string { return AdminCentre + m.ID }

// ForAny reports whether a service of the post's holds any of services (ignoring case):
// xdr finds Microsoft Defender XDR.
func (m Message) ForAny(services []string) bool {
	for _, mine := range m.Services {
		for _, part := range services {
			if strings.Contains(strings.ToLower(mine), strings.ToLower(part)) {
				return true
			}
		}
	}
	return false
}

// CategoryLabel is the category as it reads: plan for change.
func (m Message) CategoryLabel() string {
	for _, known := range Categories {
		if known[0] == m.Category {
			return known[1]
		}
	}
	return m.Category
}

// MessageFrom is a post from Graph's JSON.
func MessageFrom(data fields.Object) Message {
	var services, tags []string
	for _, item := range fields.Items(data["services"]) {
		services = append(services, fields.String(item))
	}
	for _, item := range fields.Items(data["tags"]) {
		tags = append(tags, fields.String(item))
	}
	return Message{ID: fields.Text(data, "id"), Title: fields.Text(data, "title"), Category: fields.Text(data, "category"),
		Severity: fields.Text(data, "severity"), Services: services, Tags: tags, Major: data["isMajorChange"] == true,
		Starts: fields.When(data, "startDateTime"), Ends: fields.When(data, "endDateTime"), Updated: fields.When(data, "lastModifiedDateTime"),
		ActionBy: fields.When(data, "actionRequiredByDateTime"), BodyHTML: fields.Text(fields.Map(data["body"]), "content")}
}

// Client reads Message Center posts.
type Client struct {
	API *httpx.Client
}

// New is a Message Center client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("Message Center (Microsoft Graph)")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// Query is which posts: changed from Since and before Until (either end open when zero),
// for any of Services (part of a name, ignoring case), in one Category, only major changes
// with Major, and at most Limit (0 for all).
type Query struct {
	Since    time.Time
	Until    time.Time
	Services []string
	Category string
	Major    bool
	Limit    int
}

// Messages is the posts the query names, newest change first.
func (c *Client) Messages(ctx context.Context, query Query) ([]Message, error) {
	var clauses []string
	if !query.Since.IsZero() {
		clauses = append(clauses, "lastModifiedDateTime ge "+util.ODataDatetime(query.Since))
	}
	if !query.Until.IsZero() {
		clauses = append(clauses, "lastModifiedDateTime lt "+util.ODataDatetime(query.Until))
	}
	if query.Category != "" {
		category, err := CategoryOf(query.Category)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, "category eq "+util.ODataString(category))
	}
	if query.Major {
		clauses = append(clauses, "isMajorChange eq true")
	}
	params := url.Values{"$select": {sel}, "$orderby": {"lastModifiedDateTime desc"}}
	if len(clauses) > 0 {
		params.Set("$filter", strings.Join(clauses, " and "))
	}
	var found []Message
	// Graph matches a service by its whole name only, so part of one is matched here.
	for item, err := range c.API.All(ctx, path, params, "") {
		if err != nil {
			return nil, scoped(err)
		}
		message := MessageFrom(item)
		if len(query.Services) > 0 && !message.ForAny(query.Services) {
			continue
		}
		found = append(found, message)
		if query.Limit > 0 && len(found) >= query.Limit {
			break
		}
	}
	return found, nil
}

// Message is one post, by its id (MC1183010).
func (c *Client) Message(ctx context.Context, id string) (Message, error) {
	wanted := strings.ToUpper(strings.TrimSpace(id))
	if !messageID.MatchString(wanted) {
		return Message{}, errs.Inputf("'%s' is not a Message Center id", id).WithHint("e.g. MC1183010")
	}
	data, err := c.API.Get(ctx, path+"/"+wanted, nil)
	if err != nil {
		return Message{}, scoped(err)
	}
	return MessageFrom(data), nil
}

// scoped gives a refusal what Message Center needs, which is most often what is missing.
func scoped(err error) error {
	found := errs.As(err)
	if found == nil || (found.Status != http.StatusUnauthorized && found.Status != http.StatusForbidden) {
		return err
	}
	copied := *found
	copied.Hint = ScopeHint
	return &copied
}

// CategoryOf is Graph's name for a category, from it or from how it reads (plan for
// change).
func CategoryOf(value string) (string, error) {
	folded := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", ""), "-", "")
	var labels []string
	for _, known := range Categories {
		if strings.ToLower(known[0]) == folded {
			return known[0], nil
		}
		labels = append(labels, known[1])
	}
	return "", errs.Inputf("'%s' is not a Message Center category", value).WithHint("use one of: %s", strings.Join(labels, ", "))
}
