// Package detections reads Defender XDR custom detection rules through Microsoft Graph,
// and exports them as YAML for the Terraform module
// terraform-msgraph-xdr-custom-detection-rules.
//
// The API is security/rules/detectionRules, which is still beta only, so that is the
// version used. It needs CustomDetection.Read.All: the Azure CLI's Graph token never
// carries it unless an admin consents it for the Azure CLI's own app.
package detections

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/sorting"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what detections need from a Graph token. The Azure CLI's token carries
// it only when an admin has consented it for the Azure CLI's app.
var Requirements = []microsoft.Requirement{
	{Feature: "xdr detections", Resource: "graph", AllOf: [][]string{{"CustomDetection.Read.All", "CustomDetection.ReadWrite.All"}}},
}

const (
	rulesPath      = "/beta/security/rules/detectionRules"
	azureCLIAppID  = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"
	apiDisplayName = "Defender XDR detections (Microsoft Graph)"
)

// ScopeHint is what a refused listing needs.
var ScopeHint = "custom detection rules need CustomDetection.Read.All on the Graph token. The Azure CLI cannot ask " +
	"for it: use an interactive or device-code profile whose app has it, or have an admin consent it for the " +
	"Azure CLI's app (" + azureCLIAppID + ")"

// Client reads custom detection rules.
type Client struct {
	API *httpx.Client
}

// New is a detections client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph(apiDisplayName)
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// Rules is every rule, by name (web2 before web10, whatever the case).
func (c *Client) Rules(ctx context.Context) ([]Rule, error) {
	raw, err := c.RawRules(ctx)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, len(raw))
	for index, item := range raw {
		rules[index] = RuleFrom(item)
	}
	sort.SliceStable(rules, func(a, b int) bool { return sorting.Compare(rules[a].DisplayName, rules[b].DisplayName) < 0 })
	return rules, nil
}

// RawRules is every rule as Graph returns it, following its pages, for export.
func (c *Client) RawRules(ctx context.Context) ([]fields.Object, error) {
	found, err := httpx.Collect(c.API.All(ctx, rulesPath, nil, ""))
	if err != nil {
		return nil, explained(err)
	}
	return found, nil
}

// Rule is one rule, by its id (a number) or its exact display name, whatever the case.
func (c *Client) Rule(ctx context.Context, ref string) (Rule, error) {
	raw, err := c.RawRule(ctx, ref)
	if err != nil {
		return Rule{}, err
	}
	return RuleFrom(raw), nil
}

// RawRule is one rule as Graph returns it, by id or display name.
func (c *Client) RawRule(ctx context.Context, ref string) (fields.Object, error) {
	wanted := strings.TrimSpace(ref)
	if wanted == "" {
		return nil, errs.Inputf("no rule named").WithHint("pass a rule's display name or its id")
	}
	if _, err := strconv.ParseUint(wanted, 10, 64); err == nil {
		data, err := c.API.Get(ctx, rulesPath+"/"+url.PathEscape(wanted), nil)
		if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
			return nil, errs.NotFoundf("no custom detection rule has id %s", wanted)
		}
		if err != nil {
			return nil, explained(err)
		}
		return data, nil
	}
	all, err := c.RawRules(ctx)
	if err != nil {
		return nil, err
	}
	var named []fields.Object
	for _, item := range all {
		if strings.EqualFold(fields.Text(item, "displayName"), wanted) {
			named = append(named, item)
		}
	}
	switch len(named) {
	case 0:
		return nil, errs.NotFoundf("no custom detection rule is named '%s'", wanted).
			WithHint("list them with 'xdr detections list', or pass the rule's id")
	case 1:
		return named[0], nil
	}
	var ids []string
	for _, item := range named {
		ids = append(ids, fields.Text(item, "id"))
	}
	return nil, errs.Ambiguousf("%d rules are named '%s': %s", len(named), wanted, strings.Join(ids, ", ")).WithHint("pass the id instead")
}

// explained gives a refused call the hint for the permission it needs; a suspended
// service answers 403 too, and its own hint says why better.
func explained(err error) error {
	found := errs.As(err)
	if found == nil || (found.Status != 401 && found.Status != 403) || strings.Contains(strings.ToLower(found.Message), "suspended") {
		return err
	}
	copied := *found
	copied.Hint = ScopeHint
	return &copied
}
