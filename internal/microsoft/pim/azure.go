package pim

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// PIM for Azure resources, through Azure Resource Manager.
//
// Access rests on Azure RBAC, so the Azure CLI's token works; the tenant needs Microsoft
// Entra ID P2 or ID Governance. "Mine" views are asked at the root scope with ARM's own
// filters (asTarget(), asRequestor(), asApprover()); a named principal is asked per
// subscription with assignedTo(), which includes roles held through a group and those
// inherited from above.

// The API versions PIM for Azure resources is read at.
const (
	PIMAPI   = "2020-10-01"
	RolesAPI = "2022-04-01"
)

const authorization = "providers/Microsoft.Authorization"

// AzureClient is PIM for Azure resources.
type AzureClient struct {
	API *httpx.Client
}

// NewAzure is an Azure PIM client for api's tenant.
func NewAzure(api microsoft.API) (*AzureClient, error) {
	client, err := api.ARM("Azure PIM")
	if err != nil {
		return nil, err
	}
	return &AzureClient{API: client}, nil
}

// Eligible is the eligible role assignments: the signed-in user's, or principalID's in
// the scopes.
func (c *AzureClient) Eligible(ctx context.Context, principalID string, scopes []string) ([]Assignment, error) {
	return c.instances(ctx, "roleEligibilityScheduleInstances", "eligible", principalID, scopes)
}

// Active is the active role assignments, activated through PIM or standing.
func (c *AzureClient) Active(ctx context.Context, principalID string, scopes []string) ([]Assignment, error) {
	return c.instances(ctx, "roleAssignmentScheduleInstances", "active", principalID, scopes)
}

type scoped struct{ scope, filter string }

// Requests is the requests the signed-in user made, or with approver the ones waiting on
// them; or principalID's in the scopes. Newest first.
func (c *AzureClient) Requests(ctx context.Context, approver bool, principalID string, scopes []string) ([]Request, error) {
	var pairs []scoped
	if principalID != "" {
		id, err := util.RequireGUID(principalID, "an object id")
		if err != nil {
			return nil, err
		}
		checked, err := checkScopes(scopes)
		if err != nil {
			return nil, err
		}
		for _, scope := range checked {
			pairs = append(pairs, scoped{scope, "principalId eq " + util.ODataString(id)})
		}
	} else if approver {
		pairs = []scoped{{"", "asApprover()"}}
	} else {
		pairs = []scoped{{"", "asRequestor()"}}
	}
	seen := map[string]bool{}
	var found []Request
	for _, pair := range pairs {
		items, err := c.list(ctx, pair.scope, "roleAssignmentScheduleRequests", pair.filter)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			request := azureRequest(item)
			if !seen[request.ID] {
				seen[request.ID] = true
				found = append(found, request)
			}
		}
	}
	return newest(found), nil
}

// Settings is a role's PIM settings at scope (a subscription, resource group or
// resource).
func (c *AzureClient) Settings(ctx context.Context, role, scope string) (Settings, error) {
	checked, err := checkScope(scope)
	if err != nil {
		return Settings{}, err
	}
	definitions, err := httpx.Collect(c.API.All(ctx, checked+"/"+authorization+"/roleDefinitions",
		url.Values{"api-version": {RolesAPI}, "$filter": {"roleName eq " + util.ODataString(role)}}, "nextLink"))
	if err != nil {
		return Settings{}, err
	}
	if len(definitions) == 0 {
		return Settings{}, errs.NotFoundf("no Azure role is named '%s' at %s", role, checked)
	}
	items, err := httpx.Collect(c.API.All(ctx, checked+"/"+authorization+"/roleManagementPolicyAssignments",
		url.Values{"api-version": {PIMAPI}, "$filter": {"roleDefinitionId eq " + util.ODataString(fields.Text(definitions[0], "id"))}}, "nextLink"))
	if err != nil {
		return Settings{}, err
	}
	if len(items) == 0 {
		return Settings{}, errs.NotFoundf("no PIM settings for '%s' at %s", role, checked)
	}
	rules := fields.Objects(fields.Map(items[0]["properties"])["effectiveRules"])
	return SettingsFromRules("azure", role, checked, rules), nil
}

func (c *AzureClient) instances(ctx context.Context, collection, state, principalID string, scopes []string) ([]Assignment, error) {
	pairs := []scoped{{"", "asTarget()"}}
	if principalID != "" {
		id, err := util.RequireGUID(principalID, "an object id")
		if err != nil {
			return nil, err
		}
		checked, err := checkScopes(scopes)
		if err != nil {
			return nil, err
		}
		pairs = nil
		for _, scope := range checked {
			pairs = append(pairs, scoped{scope, "assignedTo(" + util.ODataString(id) + ")"})
		}
	}
	seen := map[string]bool{}
	var found []Assignment
	for _, pair := range pairs {
		items, err := c.list(ctx, pair.scope, collection, pair.filter)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			id := fields.Text(item, "id")
			if id == "" || !seen[id] {
				seen[id] = id != ""
				found = append(found, azureAssignment(item, state))
			}
		}
	}
	sort.SliceStable(found, func(a, b int) bool {
		roleA, roleB := strings.ToLower(found[a].Role), strings.ToLower(found[b].Role)
		if roleA != roleB {
			return roleA < roleB
		}
		return found[a].Scope < found[b].Scope
	})
	return found, nil
}

func (c *AzureClient) list(ctx context.Context, scope, collection, filter string) ([]fields.Object, error) {
	return httpx.Collect(c.API.All(ctx, scope+"/"+authorization+"/"+collection,
		url.Values{"api-version": {PIMAPI}, "$filter": {filter}}, "nextLink"))
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func azureAssignment(item fields.Object, state string) Assignment {
	properties := fields.Map(item["properties"])
	expanded := fields.Map(properties["expandedProperties"])
	principal := fields.Map(expanded["principal"])
	return Assignment{
		Area: "azure", State: state,
		Role:          first(fields.Text(fields.Map(expanded["roleDefinition"]), "displayName"), fields.Text(properties, "roleDefinitionId")),
		Scope:         first(fields.Text(fields.Map(expanded["scope"]), "displayName"), fields.Text(properties, "scope")),
		PrincipalID:   fields.Text(properties, "principalId"),
		PrincipalName: first(fields.Text(principal, "displayName"), fields.Text(principal, "email")),
		MemberType:    fields.Text(properties, "memberType"), AssignmentType: fields.Text(properties, "assignmentType"),
		Starts: fields.When(properties, "startDateTime"), Ends: fields.When(properties, "endDateTime"), Raw: item,
	}
}

func azureRequest(item fields.Object) Request {
	properties := fields.Map(item["properties"])
	expanded := fields.Map(properties["expandedProperties"])
	schedule := fields.Map(properties["scheduleInfo"])
	expiration := fields.Map(schedule["expiration"])
	return Request{
		Area: "azure", ID: fields.Text(item, "id"), Action: fields.Text(properties, "requestType"), Status: fields.Text(properties, "status"),
		Role:          fields.Text(fields.Map(expanded["roleDefinition"]), "displayName"),
		Scope:         first(fields.Text(fields.Map(expanded["scope"]), "displayName"), fields.Text(properties, "scope")),
		PrincipalID:   fields.Text(properties, "principalId"),
		PrincipalName: fields.Text(fields.Map(expanded["principal"]), "displayName"), Justification: fields.Text(properties, "justification"),
		Created: fields.When(properties, "createdOn"), Starts: fields.When(schedule, "startDateTime"), Ends: fields.When(expiration, "endDateTime"),
		Duration: fields.Text(expiration, "duration"), Ticket: fields.Text(fields.Map(properties["ticketInfo"]), "ticketNumber"), Raw: item,
	}
}

func newest(requests []Request) []Request {
	sort.SliceStable(requests, func(a, b int) bool { return requests[a].Created.After(requests[b].Created) })
	return requests
}

// checkScope is an ARM scope, checked part by part: a subscription, anything in one, or a
// management group.
func checkScope(value string) (string, error) {
	hint := "use /subscriptions/ID[/resourceGroups/NAME...] or a management group's id"
	found, err := microsoft.ParseResourceID(value)
	if err != nil {
		return "", errs.Inputf("not an Azure scope: '%s' (%v)", value, err).WithHint("%s", hint)
	}
	if found.Subscription == "" && found.ManagementGroup == "" {
		return "", errs.Inputf("not an Azure scope: '%s'", value).WithHint("%s", hint)
	}
	return found.ID, nil
}

func checkScopes(values []string) ([]string, error) {
	var scopes []string
	for _, value := range values {
		scope, err := checkScope(value)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	if len(scopes) == 0 {
		return nil, errs.Inputf("a named principal needs at least one scope to search")
	}
	return scopes, nil
}
