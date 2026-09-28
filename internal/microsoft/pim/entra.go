package pim

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// PIM for Entra roles and for groups, through Microsoft Graph v1.0.
//
// "Mine" views use Graph's filterByCurrentUser, so they need a delegated token (the Azure
// CLI's does not carry the scopes; a profile with auth = "interactive" or "device-code"
// does). A named principal is asked with a principalId filter, which an app
// registration's application permissions can serve. Graph asks for a ReadWrite scope
// even to list requests; this still only ever reads.

const (
	rolesPath  = "/v1.0/roleManagement/directory"
	groupsPath = "/v1.0/identityGovernance/privilegedAccess/group"
)

// GraphClient is PIM for Entra roles and PIM for Groups.
type GraphClient struct {
	API        *httpx.Client
	mu         sync.Mutex
	roleNames  map[string]string
	groupNames map[string]string
}

// NewGraph is a Graph PIM client for api's tenant.
func NewGraph(api microsoft.API) (*GraphClient, error) {
	client, err := api.Graph("Graph PIM")
	if err != nil {
		return nil, err
	}
	return &GraphClient{API: client, groupNames: map[string]string{}}, nil
}

// RoleEligible is the Entra roles the signed-in user (or principalID) may activate.
func (c *GraphClient) RoleEligible(ctx context.Context, principalID string) ([]Assignment, error) {
	items, err := c.mineOrTheirs(ctx, rolesPath+"/roleEligibilityScheduleInstances", principalID, "principal")
	if err != nil {
		return nil, err
	}
	return c.roleAssignments(ctx, items, "eligible")
}

// RoleActive is the Entra roles the signed-in user (or principalID) holds now.
func (c *GraphClient) RoleActive(ctx context.Context, principalID string) ([]Assignment, error) {
	items, err := c.mineOrTheirs(ctx, rolesPath+"/roleAssignmentScheduleInstances", principalID, "principal")
	if err != nil {
		return nil, err
	}
	return c.roleAssignments(ctx, items, "active")
}

// RoleRequests is the role requests the signed-in user (or principalID) made, or with
// approver those waiting on them; the newest first.
func (c *GraphClient) RoleRequests(ctx context.Context, approver bool, principalID string) ([]Request, error) {
	items, err := c.mineOrTheirs(ctx, rolesPath+"/roleAssignmentScheduleRequests", principalID, on(approver))
	if err != nil {
		return nil, err
	}
	names, err := c.RoleNames(ctx)
	if err != nil {
		return nil, err
	}
	var found []Request
	for _, item := range items {
		found = append(found, graphRequest("entra", item, names))
	}
	return newest(found), nil
}

func on(approver bool) string {
	if approver {
		return "approver"
	}
	return "principal"
}

// RoleSettings is an Entra role's PIM settings, for the whole directory.
func (c *GraphClient) RoleSettings(ctx context.Context, role string) (Settings, error) {
	roleID, err := c.roleID(ctx, role)
	if err != nil {
		return Settings{}, err
	}
	assignments, err := httpx.Collect(c.API.All(ctx, "/v1.0/policies/roleManagementPolicyAssignments", url.Values{
		"$filter": {"scopeId eq '/' and scopeType eq 'DirectoryRole' and roleDefinitionId eq " + util.ODataString(roleID)},
		"$expand": {"policy($expand=rules)"}}, ""))
	if err != nil {
		return Settings{}, err
	}
	if len(assignments) == 0 {
		return Settings{}, errs.NotFoundf("no PIM settings for the Entra role '%s'", role)
	}
	names, _ := c.RoleNames(ctx)
	name := names[roleID]
	if name == "" {
		name = role
	}
	return SettingsFromRules("entra", name, "/", fields.Objects(fields.Map(assignments[0]["policy"])["rules"])), nil
}

// RoleNames is every directory role definition's name by id, fetched once.
func (c *GraphClient) RoleNames(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roleNames != nil {
		return c.roleNames, nil
	}
	items, err := httpx.Collect(c.API.All(ctx, rolesPath+"/roleDefinitions", url.Values{"$select": {"id,displayName"}}, ""))
	if err != nil {
		return nil, err
	}
	c.roleNames = map[string]string{}
	for _, item := range items {
		c.roleNames[fields.Text(item, "id")] = fields.Text(item, "displayName")
	}
	return c.roleNames, nil
}

// GroupEligible is the group memberships and ownerships the signed-in user (or
// principalID) could activate.
func (c *GraphClient) GroupEligible(ctx context.Context, principalID string) ([]Assignment, error) {
	items, err := c.mineOrTheirs(ctx, groupsPath+"/eligibilityScheduleInstances", principalID, "principal")
	if err != nil {
		return nil, err
	}
	return c.groupAssignments(ctx, items, "eligible"), nil
}

// GroupActive is the group memberships and ownerships the signed-in user (or principalID)
// holds now.
func (c *GraphClient) GroupActive(ctx context.Context, principalID string) ([]Assignment, error) {
	items, err := c.mineOrTheirs(ctx, groupsPath+"/assignmentScheduleInstances", principalID, "principal")
	if err != nil {
		return nil, err
	}
	return c.groupAssignments(ctx, items, "active"), nil
}

// GroupRequests is the group requests the signed-in user (or principalID) made, or with
// approver those waiting on them; the newest first.
func (c *GraphClient) GroupRequests(ctx context.Context, approver bool, principalID string) ([]Request, error) {
	items, err := c.mineOrTheirs(ctx, groupsPath+"/assignmentScheduleRequests", principalID, on(approver))
	if err != nil {
		return nil, err
	}
	var found []Request
	for _, item := range items {
		request := graphRequest("groups", item, nil)
		request.Role = fields.Text(item, "accessId")
		request.Scope = c.GroupName(ctx, fields.Text(item, "groupId"))
		found = append(found, request)
	}
	return newest(found), nil
}

// GroupSettings is a group's PIM settings for its members (member) or owners (owner).
func (c *GraphClient) GroupSettings(ctx context.Context, groupID, access string) (Settings, error) {
	id, err := util.RequireGUID(groupID, "an object id")
	if err != nil {
		return Settings{}, err
	}
	assignments, err := httpx.Collect(c.API.All(ctx, "/v1.0/policies/roleManagementPolicyAssignments", url.Values{
		"$filter": {"scopeId eq " + util.ODataString(id) + " and scopeType eq 'Group' and roleDefinitionId eq " + util.ODataString(access)},
		"$expand": {"policy($expand=rules)"}}, ""))
	if err != nil {
		return Settings{}, err
	}
	if len(assignments) == 0 {
		return Settings{}, errs.NotFoundf("no PIM settings for group %s (%s)", id, access)
	}
	return SettingsFromRules("groups", access, c.GroupName(ctx, id), fields.Objects(fields.Map(assignments[0]["policy"])["rules"])), nil
}

// GroupName is a group's display name, cached; its id when the name cannot be read.
func (c *GraphClient) GroupName(ctx context.Context, groupID string) string {
	if !util.IsGUID(groupID) {
		// Never build a path from something that is not an object id.
		return groupID
	}
	c.mu.Lock()
	name, known := c.groupNames[groupID]
	c.mu.Unlock()
	if known {
		return name
	}
	name = groupID
	if data, err := c.API.Get(ctx, "/v1.0/groups/"+groupID, url.Values{"$select": {"displayName"}}); err == nil && fields.Text(data, "displayName") != "" {
		name = fields.Text(data, "displayName")
	}
	c.mu.Lock()
	c.groupNames[groupID] = name
	c.mu.Unlock()
	return name
}

func (c *GraphClient) mineOrTheirs(ctx context.Context, path, principalID, side string) ([]fields.Object, error) {
	if principalID != "" {
		id, err := util.RequireGUID(principalID, "an object id")
		if err != nil {
			return nil, err
		}
		return httpx.Collect(c.API.All(ctx, path, url.Values{"$filter": {"principalId eq " + util.ODataString(id)}}, ""))
	}
	return httpx.Collect(c.API.All(ctx, path+"/filterByCurrentUser(on='"+side+"')", nil, ""))
}

func (c *GraphClient) roleID(ctx context.Context, role string) (string, error) {
	if util.IsGUID(role) {
		return strings.ToLower(strings.TrimSpace(role)), nil
	}
	names, err := c.RoleNames(ctx)
	if err != nil {
		return "", err
	}
	for id, name := range names {
		if strings.EqualFold(name, strings.TrimSpace(role)) {
			return id, nil
		}
	}
	return "", errs.NotFoundf("no Entra role is named '%s'", role)
}

func (c *GraphClient) roleAssignments(ctx context.Context, items []fields.Object, state string) ([]Assignment, error) {
	names, err := c.RoleNames(ctx)
	if err != nil {
		return nil, err
	}
	var found []Assignment
	for _, item := range items {
		roleID := fields.Text(item, "roleDefinitionId")
		found = append(found, Assignment{Area: "entra", State: state, Role: first(names[roleID], roleID),
			Scope: first(fields.Text(item, "directoryScopeId"), "/"), PrincipalID: fields.Text(item, "principalId"),
			MemberType: fields.Text(item, "memberType"), AssignmentType: fields.Text(item, "assignmentType"),
			Starts: fields.When(item, "startDateTime"), Ends: fields.When(item, "endDateTime"), Raw: item})
	}
	sort.SliceStable(found, func(a, b int) bool { return strings.ToLower(found[a].Role) < strings.ToLower(found[b].Role) })
	return found, nil
}

func (c *GraphClient) groupAssignments(ctx context.Context, items []fields.Object, state string) []Assignment {
	var found []Assignment
	for _, item := range items {
		found = append(found, Assignment{Area: "groups", State: state, Role: fields.Text(item, "accessId"),
			Scope: c.GroupName(ctx, fields.Text(item, "groupId")), PrincipalID: fields.Text(item, "principalId"),
			MemberType: fields.Text(item, "memberType"), AssignmentType: fields.Text(item, "assignmentType"),
			Starts: fields.When(item, "startDateTime"), Ends: fields.When(item, "endDateTime"), Raw: item})
	}
	sort.SliceStable(found, func(a, b int) bool {
		scopeA, scopeB := strings.ToLower(found[a].Scope), strings.ToLower(found[b].Scope)
		if scopeA != scopeB {
			return scopeA < scopeB
		}
		return found[a].Role < found[b].Role
	})
	return found
}

func graphRequest(area string, item fields.Object, roleNames map[string]string) Request {
	schedule := fields.Map(item["scheduleInfo"])
	expiration := fields.Map(schedule["expiration"])
	roleID := fields.Text(item, "roleDefinitionId")
	return Request{
		Area: area, ID: fields.Text(item, "id"), Action: fields.Text(item, "action"), Status: fields.Text(item, "status"),
		Role: first(roleNames[roleID], roleID), Scope: first(fields.Text(item, "directoryScopeId"), "/"),
		PrincipalID: fields.Text(item, "principalId"), Justification: fields.Text(item, "justification"),
		Created: fields.When(item, "createdDateTime"), Starts: fields.When(schedule, "startDateTime"),
		Ends: fields.When(expiration, "endDateTime"), Duration: fields.Text(expiration, "duration"),
		Ticket: fields.Text(fields.Map(item["ticketInfo"]), "ticketNumber"), Raw: item,
	}
}
