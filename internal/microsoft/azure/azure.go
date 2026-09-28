// Package azure reads Azure Resource Manager: subscriptions, Azure Resource Graph, RBAC
// role assignments, Defender for Cloud (secure score, controls, recommendations, plans),
// and finding a Log Analytics workspace by any of its names. Access rests on Azure RBAC
// (Reader is enough for all of it), not on token scopes.
package azure

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// The API versions each part of Resource Manager is read at.
const (
	SubscriptionsAPI = "2022-12-01"
	ResourceGraphAPI = "2022-10-01"
	AuthorizationAPI = "2022-04-01"
	SecureScoreAPI   = "2020-01-01"
	AssessmentsAPI   = "2021-06-01"
	PricingsAPI      = "2024-01-01"
	WorkspacesAPI    = "2023-09-01"
)

// graphPage is the most rows Resource Graph returns a page.
const graphPage = 1000

// Client reads Azure Resource Manager for one tenant.
type Client struct {
	API       *httpx.Client
	mu        sync.Mutex
	roleNames map[string]string
}

// New is an Azure client for api's tenant, in its cloud.
func New(api microsoft.API) (*Client, error) {
	client, err := api.ARM("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client, roleNames: map[string]string{}}, nil
}

func version(api string, pairs ...string) url.Values {
	values := url.Values{"api-version": {api}}
	for index := 0; index+1 < len(pairs); index += 2 {
		values.Set(pairs[index], pairs[index+1])
	}
	return values
}

func subscriptionID(value string) (string, error) {
	return util.RequireGUID(value, "a subscription id")
}

// Subscriptions -------------------------------------------------------------------------

// Subscriptions is the subscriptions the credential can see, in tenantID when given, by
// name.
func (c *Client) Subscriptions(ctx context.Context, tenantID string) ([]Subscription, error) {
	items, err := httpx.Collect(c.API.All(ctx, "/subscriptions", version(SubscriptionsAPI), "nextLink"))
	if err != nil {
		return nil, err
	}
	var found []Subscription
	for _, item := range items {
		subscription := SubscriptionFrom(item)
		if tenantID == "" || subscription.TenantID == strings.ToLower(tenantID) {
			found = append(found, subscription)
		}
	}
	sort.SliceStable(found, func(a, b int) bool { return strings.ToLower(found[a].Name) < strings.ToLower(found[b].Name) })
	return found, nil
}

// Resource Graph ------------------------------------------------------------------------

// ResourceGraph runs an Azure Resource Graph (KQL) query, following $skipToken up to
// limit. Without subscriptions it covers every subscription the credential can read.
func (c *Client) ResourceGraph(ctx context.Context, query string, subscriptions []string, limit int) (render.QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return render.QueryResult{}, errs.Inputf("the Resource Graph query is empty")
	}
	var scope []string
	for _, item := range subscriptions {
		id, err := subscriptionID(item)
		if err != nil {
			return render.QueryResult{}, err
		}
		scope = append(scope, id)
	}
	var rows []map[string]any
	skipToken, truncated := "", false
	for {
		options := map[string]any{"resultFormat": "objectArray", "$top": min(graphPage, limit-len(rows))}
		if skipToken != "" {
			options["$skipToken"] = skipToken
		}
		body := map[string]any{"query": query, "options": options}
		if len(scope) > 0 {
			body["subscriptions"] = scope
		}
		page, err := c.API.Post(ctx, "/providers/Microsoft.ResourceGraph/resources", body, version(ResourceGraphAPI))
		if err != nil {
			return render.QueryResult{}, err
		}
		rows = append(rows, fields.Objects(page["data"])...)
		skipToken, _ = page["$skipToken"].(string)
		if skipToken == "" {
			break
		}
		if len(rows) >= limit {
			truncated = true
			break
		}
	}
	return render.FromRecords(rows, truncated), nil
}

// Log Analytics workspaces --------------------------------------------------------------

// FindWorkspace is the workspace ref names, by its resource id, its name or its Workspace
// ID. A resource id is read from Resource Manager directly; a name or a Workspace ID is
// looked for with Resource Graph, in subscriptions when given, else in every one the
// credential can read.
func (c *Client) FindWorkspace(ctx context.Context, ref microsoft.WorkspaceRef, subscriptions []string) (Workspace, error) {
	if ref.Kind == microsoft.WorkspaceResourceID {
		return c.workspaceByID(ctx, ref.Value)
	}
	column := "properties.customerId"
	if ref.Kind == microsoft.WorkspaceName {
		column = "name"
	}
	// The ref has been checked: a name has only letters, digits and hyphens, and a
	// Workspace ID is a GUID, so neither can end the string early.
	result, err := c.ResourceGraph(ctx, "resources | where type =~ '"+microsoft.WorkspaceType+"' and "+column+" =~ '"+ref.Value+"'"+
		" | project id, name, location, properties", subscriptions, 10)
	if err != nil {
		return Workspace{}, err
	}
	var found []Workspace
	for _, row := range result.Rows {
		found = append(found, WorkspaceFrom(row))
	}
	switch len(found) {
	case 0:
		return Workspace{}, errs.NotFoundf("no Log Analytics workspace with the %s '%s'", ref.Kind, ref.Value).
			WithHint("check the name and the profile's subscription, and that you have Reader on the workspace; or %s", microsoft.WorkspaceHint)
	case 1:
		return withWorkspaceID(found[0])
	}
	var places []string
	for _, item := range found {
		places = append(places, item.ID)
	}
	sort.Strings(places)
	return Workspace{}, errs.Ambiguousf("%d workspaces are named '%s': %s", len(found), ref.Value, strings.Join(places, ", ")).
		WithHint("give the one you mean by its resource id or Workspace ID")
}

func (c *Client) workspaceByID(ctx context.Context, resourceID string) (Workspace, error) {
	data, err := c.API.Get(ctx, resourceID, version(WorkspacesAPI))
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return Workspace{}, errs.NotFoundf("no Log Analytics workspace at '%s'", resourceID).
			WithHint("check the subscription, resource group and name in the id")
	}
	if err != nil {
		return Workspace{}, err
	}
	return withWorkspaceID(WorkspaceFrom(data))
}

// withWorkspaceID is the workspace, when Resource Manager gave its Workspace ID, as it
// always should.
func withWorkspaceID(workspace Workspace) (Workspace, error) {
	if workspace.WorkspaceID == "" {
		return Workspace{}, errs.NotFoundf("Resource Manager gave no Workspace ID (customerId) for '%s'", workspace.ID).
			WithHint("give the Workspace ID itself, from the workspace's Overview page")
	}
	return workspace, nil
}

// RBAC ----------------------------------------------------------------------------------

// RoleAssignments is every role assignment that applies to principalID in the
// subscriptions. assignedTo() includes assignments made to groups the principal belongs
// to, and those inherited from management groups above each subscription.
func (c *Client) RoleAssignments(ctx context.Context, principalID string, subscriptions []string) ([]RoleAssignment, error) {
	principal, err := util.RequireGUID(principalID, "a principal id")
	if err != nil {
		return nil, err
	}
	seen := map[string]RoleAssignment{}
	var order []string
	for _, subscription := range subscriptions {
		id, err := subscriptionID(subscription)
		if err != nil {
			return nil, err
		}
		items, err := httpx.Collect(c.API.All(ctx, "/subscriptions/"+id+"/providers/Microsoft.Authorization/roleAssignments",
			version(AuthorizationAPI, "$filter", "assignedTo('"+principal+"')"), "nextLink"))
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			assignment := fields.Text(item, "id")
			if _, done := seen[assignment]; assignment == "" || done {
				continue
			}
			definition := fields.Text(fields.Map(item["properties"]), "roleDefinitionId")
			seen[assignment] = RoleAssignmentFrom(item, c.roleName(ctx, definition))
			order = append(order, assignment)
		}
	}
	found := make([]RoleAssignment, len(order))
	for index, id := range order {
		found[index] = seen[id]
	}
	sort.SliceStable(found, func(a, b int) bool {
		scopeA, scopeB := strings.ToLower(found[a].Scope), strings.ToLower(found[b].Scope)
		if scopeA != scopeB {
			return scopeA < scopeB
		}
		return found[a].RoleName < found[b].RoleName
	})
	return found, nil
}

func (c *Client) roleName(ctx context.Context, definitionID string) string {
	if definitionID == "" {
		return ""
	}
	c.mu.Lock()
	name, known := c.roleNames[definitionID]
	c.mu.Unlock()
	if known {
		return name
	}
	// The assignment is still worth showing without a friendly name.
	if data, err := c.API.Get(ctx, definitionID, version(AuthorizationAPI)); err == nil {
		name = fields.Text(fields.Map(data["properties"]), "roleName")
	}
	if name == "" {
		name = definitionID[strings.LastIndex(definitionID, "/")+1:]
	}
	c.mu.Lock()
	c.roleNames[definitionID] = name
	c.mu.Unlock()
	return name
}

// Defender for Cloud --------------------------------------------------------------------

// SecureScore is the subscription's secure score, or nil where Defender for Cloud has
// none.
func (c *Client) SecureScore(ctx context.Context, subscription string) (*SecureScore, error) {
	id, err := subscriptionID(subscription)
	if err != nil {
		return nil, err
	}
	data, err := c.API.Get(ctx, "/subscriptions/"+id+"/providers/Microsoft.Security/secureScores/ascScore", version(SecureScoreAPI))
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	score := SecureScoreFrom(id, data)
	return &score, nil
}

// SecureScoreControls is the security controls, the ones costing the most points first.
func (c *Client) SecureScoreControls(ctx context.Context, subscription string) ([]SecureScoreControl, error) {
	id, err := subscriptionID(subscription)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, "/subscriptions/"+id+"/providers/Microsoft.Security/secureScoreControls",
		version(SecureScoreAPI, "$expand", "definition"), "nextLink"))
	if err != nil {
		return nil, err
	}
	controls := make([]SecureScoreControl, len(items))
	for index, item := range items {
		controls[index] = SecureScoreControlFrom(item)
	}
	sort.SliceStable(controls, func(a, b int) bool { return controls[a].PointsLost() > controls[b].PointsLost() })
	return controls, nil
}

var severityOrder = map[string]int{"high": 0, "medium": 1, "low": 2}

func severityRank(severity string) int {
	if rank, ok := severityOrder[strings.ToLower(severity)]; ok {
		return rank
	}
	return 3
}

// Assessments is Defender for Cloud recommendation results, most severe first.
func (c *Client) Assessments(ctx context.Context, subscription string, unhealthyOnly bool) ([]Assessment, error) {
	id, err := subscriptionID(subscription)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, "/subscriptions/"+id+"/providers/Microsoft.Security/assessments",
		version(AssessmentsAPI, "$expand", "metadata"), "nextLink"))
	if err != nil {
		return nil, err
	}
	var found []Assessment
	for _, item := range items {
		if assessment := AssessmentFrom(item); !unhealthyOnly || assessment.Unhealthy() {
			found = append(found, assessment)
		}
	}
	sort.SliceStable(found, func(a, b int) bool {
		rankA, rankB := severityRank(found[a].Severity), severityRank(found[b].Severity)
		if rankA != rankB {
			return rankA < rankB
		}
		return found[a].Name < found[b].Name
	})
	return found, nil
}

// DefenderPlans is every Defender for Cloud plan on the subscription and its tier.
func (c *Client) DefenderPlans(ctx context.Context, subscription string) ([]DefenderPlan, error) {
	id, err := subscriptionID(subscription)
	if err != nil {
		return nil, err
	}
	data, err := c.API.Get(ctx, "/subscriptions/"+id+"/providers/Microsoft.Security/pricings", version(PricingsAPI))
	if err != nil {
		return nil, err
	}
	var plans []DefenderPlan
	for _, item := range fields.Objects(data["value"]) {
		plans = append(plans, DefenderPlanFrom(item))
	}
	sort.SliceStable(plans, func(a, b int) bool { return strings.ToLower(plans[a].Name) < strings.ToLower(plans[b].Name) })
	return plans, nil
}
