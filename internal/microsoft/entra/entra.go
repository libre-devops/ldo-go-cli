// Package entra reads Entra ID through Microsoft Graph v1.0: devices, users, groups and
// their memberships, directory roles, sign-ins, app registration credentials and
// Conditional Access policies.
package entra

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// directory is the scopes that read the directory. Directory.AccessAsUser.All is among
// them because the Azure CLI's delegated Graph token carries it, and it lets the call do
// whatever the signed-in user can.
var directory = []string{"Directory.Read.All", "Directory.ReadWrite.All", "Directory.AccessAsUser.All"}

func with(scopes ...string) []string { return append(scopes, directory...) }

// Requirements are what each Entra feature needs from a Graph token.
var Requirements = []microsoft.Requirement{
	{Feature: "entra devices", Resource: "graph", AllOf: [][]string{with("Device.Read.All")}},
	{Feature: "entra groups", Resource: "graph", AllOf: [][]string{with("GroupMember.Read.All", "Group.Read.All", "Group.ReadWrite.All")}},
	{Feature: "entra users", Resource: "graph", AllOf: [][]string{with("User.Read.All", "User.ReadWrite.All")}},
	// Active roles are read through the user's memberships, which directory read covers.
	{Feature: "entra roles", Resource: "graph", AllOf: [][]string{with("RoleManagement.Read.Directory", "RoleManagement.Read.All")}},
	// PIM eligibility is not covered by directory read, not even Directory.AccessAsUser.All.
	{Feature: "entra pim eligibility", Resource: "graph", AllOf: [][]string{{"RoleEligibilitySchedule.Read.Directory",
		"RoleEligibilitySchedule.ReadWrite.Directory", "RoleManagement.Read.Directory", "RoleManagement.Read.All",
		"RoleManagement.ReadWrite.Directory"}}},
	{Feature: "entra apps", Resource: "graph", AllOf: [][]string{with("Application.Read.All", "Application.ReadWrite.All")}},
	// Graph asks for both: the audit log itself, and directory read to resolve names.
	{Feature: "entra sign-ins", Resource: "graph", AllOf: [][]string{{"AuditLog.Read.All"}, directory}},
	{Feature: "entra conditional access", Resource: "graph", AllOf: [][]string{{"Policy.Read.All", "Policy.ReadWrite.ConditionalAccess"}}},
}

// An OData cast on a directory relationship (".../microsoft.graph.group") is an advanced
// query: Graph requires ConsistencyLevel: eventual together with $count=true.
var eventual = map[string]string{"ConsistencyLevel": "eventual"}

const credentialSelect = "id,appId,displayName,passwordCredentials,keyCredentials"

// MemberKinds are the kinds of member a group's members can be narrowed to.
var MemberKinds = []string{"user", "device", "group", "servicePrincipal"}

// Client reads Entra ID for one tenant.
type Client struct {
	API *httpx.Client
}

// New is an Entra client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

func values(pairs ...string) url.Values {
	found := url.Values{}
	for index := 0; index+1 < len(pairs); index += 2 {
		found.Set(pairs[index], pairs[index+1])
	}
	return found
}

func (c *Client) all(ctx context.Context, path string, params url.Values, headers map[string]string) ([]fields.Object, error) {
	return httpx.Collect(c.API.Pages(ctx, path, httpx.Call{Params: params, Headers: headers}, ""))
}

// Devices -------------------------------------------------------------------------------

// FindDevices is the devices whose display name is name, else its short hostname, the
// most recently signed in first. Several are normal: stale registrations keep the name.
func (c *Client) FindDevices(ctx context.Context, name string) ([]Device, error) {
	for _, candidate := range util.CandidateNames(name) {
		devices, err := c.devicesMatching(ctx, "displayName eq "+util.ODataString(candidate))
		if err != nil || len(devices) > 0 {
			return devices, err
		}
	}
	// A short name, where Entra holds the FQDN: its first label, exactly.
	short := strings.TrimRight(strings.TrimSpace(name), ".")
	if short == "" || strings.Contains(short, ".") {
		return nil, nil
	}
	devices, err := c.devicesMatching(ctx, "startswith(displayName,"+util.ODataString(short+".")+")")
	if err != nil {
		return nil, err
	}
	var kept []Device
	for _, device := range devices {
		first, _, _ := strings.Cut(device.DisplayName, ".")
		if strings.EqualFold(first, short) {
			kept = append(kept, device)
		}
	}
	return kept, nil
}

func (c *Client) devicesMatching(ctx context.Context, query string) ([]Device, error) {
	items, err := c.all(ctx, "/v1.0/devices", values("$filter", query, "$select", DeviceSelect), nil)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, len(items))
	for index, item := range items {
		devices[index] = DeviceFrom(item)
	}
	// The most recently signed in first; one that never signed in last.
	slices.SortStableFunc(devices, func(a, b Device) int {
		switch {
		case a.LastSignIn.IsZero() || b.LastSignIn.IsZero():
			return boolCompare(a.LastSignIn.IsZero(), b.LastSignIn.IsZero())
		}
		return b.LastSignIn.Compare(a.LastSignIn)
	})
	return devices, nil
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// DeviceGroups is the groups a device belongs to, nested ones too unless not transitive.
func (c *Client) DeviceGroups(ctx context.Context, deviceID string, transitive bool) ([]Group, error) {
	return c.groupsOf(ctx, "devices", deviceID, transitive)
}

// LookUpDevices is each name's devices, as FindDevices finds them, and their membership
// of groups (nested too, when transitive), in the order asked.
//
// Each group's members are fetched once, and the first name is looked up on the calling
// goroutine, so a lapsed sign-in is met where someone can answer it; the rest are looked
// up workers at a time.
func (c *Client) LookUpDevices(ctx context.Context, names []string, groups []Group, transitive bool, workers int) ([]DeviceLookup, error) {
	members := map[string]map[string]bool{}
	for _, group := range groups {
		devices, err := c.GroupDevices(ctx, group.ID, transitive)
		if err != nil {
			return nil, err
		}
		members[group.ID] = map[string]bool{}
		for _, device := range devices {
			members[group.ID][device.ID] = true
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	found := make([][]Device, len(names))
	var err error
	if found[0], err = c.FindDevices(ctx, names[0]); err != nil {
		return nil, err
	}
	if err := c.findRest(ctx, names, found, workers); err != nil {
		return nil, err
	}
	lookups := make([]DeviceLookup, len(names))
	for index, name := range names {
		lookups[index] = DeviceLookup{Query: name, Devices: found[index], Groups: groups, Members: members}
	}
	return lookups, nil
}

func (c *Client) findRest(ctx context.Context, names []string, found [][]Device, workers int) error {
	slots := make(chan struct{}, max(workers, 1))
	failures := make([]error, len(names))
	var group sync.WaitGroup
	for index := 1; index < len(names); index++ {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; group.Done() }()
			found[index], failures[index] = c.FindDevices(ctx, names[index])
		}()
	}
	group.Wait()
	for _, err := range failures {
		if err != nil {
			return err
		}
	}
	return nil
}

// GroupDevices is the device members of a group, nested groups too when transitive, by
// name.
func (c *Client) GroupDevices(ctx context.Context, groupID string, transitive bool) ([]Device, error) {
	id, err := util.RequireGUID(groupID, "an Entra object id")
	if err != nil {
		return nil, err
	}
	items, err := c.all(ctx, "/v1.0/groups/"+id+"/"+relation(transitive, "transitiveMembers", "members")+"/microsoft.graph.device",
		values("$select", DeviceSelect, "$count", "true", "$top", "999"), eventual)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, len(items))
	for index, item := range items {
		devices[index] = DeviceFrom(item)
	}
	sort.SliceStable(devices, func(a, b int) bool {
		return strings.ToLower(devices[a].DisplayName) < strings.ToLower(devices[b].DisplayName)
	})
	return devices, nil
}

func relation(transitive bool, yes, no string) string {
	if transitive {
		return yes
	}
	return no
}

// Groups ---------------------------------------------------------------------------------

// GetGroup is one group, by object id or by exact display name: NotFound when nothing
// matches and Ambiguous when a display name is shared, since acting on the wrong group is
// worse than asking for its id.
func (c *Client) GetGroup(ctx context.Context, ref string) (Group, error) {
	ref = strings.TrimSpace(ref)
	if util.IsGUID(ref) {
		data, err := c.getOne(ctx, "/v1.0/groups/"+ref, GroupSelect, "no Entra group has id "+ref)
		return GroupFrom(data), err
	}
	items, err := c.named(ctx, "/v1.0/groups", ref, GroupSelect)
	if err != nil {
		return Group{}, err
	}
	var groups []Group
	for _, item := range items {
		groups = append(groups, GroupFrom(item))
	}
	return one(groups, ref, "Entra group", "Entra groups", func(group Group) string { return group.ID })
}

// GroupMembers is the members of a group of any type, or of one kind only.
func (c *Client) GroupMembers(ctx context.Context, groupID string, transitive bool, kind string) ([]DirectoryObject, error) {
	id, err := util.RequireGUID(groupID, "an Entra object id")
	if err != nil {
		return nil, err
	}
	path := "/v1.0/groups/" + id + "/" + relation(transitive, "transitiveMembers", "members")
	var items []fields.Object
	if kind == "" {
		items, err = c.all(ctx, path, values("$top", "999"), nil)
	} else {
		items, err = c.all(ctx, path+"/microsoft.graph."+kind, values("$count", "true", "$top", "999"), eventual)
	}
	if err != nil {
		return nil, err
	}
	members := make([]DirectoryObject, len(items))
	for index, item := range items {
		members[index] = DirectoryObjectFrom(item, kind)
	}
	sort.SliceStable(members, func(a, b int) bool {
		if members[a].Kind != members[b].Kind {
			return members[a].Kind < members[b].Kind
		}
		return strings.ToLower(members[a].DisplayName) < strings.ToLower(members[b].DisplayName)
	})
	return members, nil
}

// Users and principals -------------------------------------------------------------------

// GetUser is one user, by object id, by user principal name, or by exact display name.
func (c *Client) GetUser(ctx context.Context, ref string) (User, error) {
	ref = strings.TrimSpace(ref)
	if util.IsGUID(ref) || strings.Contains(ref, "@") {
		// A guest UPN holds '#', so the segment must be percent-encoded.
		data, err := c.getOne(ctx, "/v1.0/users/"+url.PathEscape(ref), UserSelect, "no Entra user is '"+ref+"'")
		return UserFrom(data), err
	}
	items, err := c.named(ctx, "/v1.0/users", ref, UserSelect)
	if err != nil {
		return User{}, err
	}
	var users []User
	for _, item := range items {
		users = append(users, UserFrom(item))
	}
	return one(users, ref, "Entra user", "Entra users", func(user User) string { return user.ID })
}

// UserGroups is the groups a user belongs to, nested ones too unless not transitive.
func (c *Client) UserGroups(ctx context.Context, userID string, transitive bool) ([]Group, error) {
	return c.groupsOf(ctx, "users", userID, transitive)
}

// FindPrincipal is a user, group or service principal, by object id, UPN or exact display
// name: the object id Azure role assignments are keyed on.
func (c *Client) FindPrincipal(ctx context.Context, ref string) (DirectoryObject, error) {
	ref = strings.TrimSpace(ref)
	if util.IsGUID(ref) {
		data, err := c.getOne(ctx, "/v1.0/directoryObjects/"+ref, "", "no Entra object has id "+ref)
		return DirectoryObjectFrom(data, ""), err
	}
	if strings.Contains(ref, "@") {
		user, err := c.GetUser(ctx, ref)
		return DirectoryObjectFrom(user.Raw, "user"), err
	}
	var found []DirectoryObject
	for _, source := range [][2]string{{"/v1.0/groups", "group"}, {"/v1.0/servicePrincipals", "servicePrincipal"}, {"/v1.0/users", "user"}} {
		items, err := c.named(ctx, source[0], ref, "id,displayName,appId,userPrincipalName")
		if err != nil {
			return DirectoryObject{}, err
		}
		for _, item := range items {
			found = append(found, DirectoryObjectFrom(item, source[1]))
		}
	}
	return one(found, ref, "Entra user, group or service principal", "Entra objects", func(item DirectoryObject) string { return item.ID })
}

// Roles -----------------------------------------------------------------------------------

// UserRoles is the directory roles a user holds now, and those PIM makes them eligible
// for. Active roles include ones held through a role-assignable group. Eligibility needs
// PIM (Entra ID P2) and the right to read it; when it cannot be read, the report says why
// instead of failing.
func (c *Client) UserRoles(ctx context.Context, userID string) (RoleReport, error) {
	id, err := util.RequireGUID(userID, "an Entra object id")
	if err != nil {
		return RoleReport{}, err
	}
	items, err := c.all(ctx, "/v1.0/users/"+id+"/transitiveMemberOf/microsoft.graph.directoryRole", values("$count", "true"), eventual)
	if err != nil {
		return RoleReport{}, err
	}
	var report RoleReport
	for _, item := range items {
		report.Active = append(report.Active, RoleFromDirectoryRole(item))
	}
	sortRoles(report.Active)
	eligible, err := c.all(ctx, "/v1.0/roleManagement/directory/roleEligibilitySchedules",
		values("$filter", "principalId eq "+util.ODataString(id), "$expand", "roleDefinition"), nil)
	if err != nil {
		found := errs.As(err)
		if found == nil || (found.Status != http.StatusBadRequest && found.Status != http.StatusForbidden) {
			return RoleReport{}, err
		}
		report.EligibleError = err.Error()
		return report, nil
	}
	report.Eligible = []RoleAssignment{}
	for _, item := range eligible {
		report.Eligible = append(report.Eligible, RoleFromEligibility(item))
	}
	sortRoles(report.Eligible)
	return report, nil
}

func sortRoles(roles []RoleAssignment) {
	sort.SliceStable(roles, func(a, b int) bool {
		return strings.ToLower(roles[a].RoleName) < strings.ToLower(roles[b].RoleName)
	})
}

// Sign-ins --------------------------------------------------------------------------------

// SignInQuery narrows SignIns: one user (a UPN or object id), a start, failures only.
type SignInQuery struct {
	User         string
	Since        time.Time
	FailuresOnly bool
	Limit        int
}

// SignIns is recent sign-ins, newest first. Needs Entra ID P1 in the tenant.
func (c *Client) SignIns(ctx context.Context, query SignInQuery) ([]SignIn, error) {
	var filters []string
	if user := strings.TrimSpace(query.User); user != "" {
		field := "userPrincipalName"
		if util.IsGUID(user) {
			field = "userId"
		}
		filters = append(filters, field+" eq "+util.ODataString(user))
	}
	if !query.Since.IsZero() {
		filters = append(filters, "createdDateTime ge "+util.ODataDatetime(query.Since))
	}
	if query.FailuresOnly {
		filters = append(filters, "status/errorCode ne 0")
	}
	limit := max(query.Limit, 1)
	params := values("$top", strconv.Itoa(min(limit, 999)))
	if len(filters) > 0 {
		params.Set("$filter", strings.Join(filters, " and "))
	}
	var events []SignIn
	for item, err := range c.API.All(ctx, "/v1.0/auditLogs/signIns", params, "") {
		if err != nil {
			return nil, err
		}
		events = append(events, SignInFrom(item))
		if len(events) == limit {
			break
		}
	}
	return events, nil
}

// App credentials -------------------------------------------------------------------------

// AppCredentials is every secret and certificate on app registrations (and, when asked,
// service principals).
func (c *Client) AppCredentials(ctx context.Context, servicePrincipals bool) ([]AppCredential, error) {
	sources := [][2]string{{"/v1.0/applications", "application"}}
	if servicePrincipals {
		sources = append(sources, [2]string{"/v1.0/servicePrincipals", "service principal"})
	}
	var found []AppCredential
	for _, source := range sources {
		items, err := c.all(ctx, source[0], values("$select", credentialSelect, "$top", "999"), nil)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			found = append(found, credentials(item, source[1])...)
		}
	}
	return found, nil
}

func credentials(item fields.Object, ownerKind string) []AppCredential {
	var found []AppCredential
	for _, kind := range [][2]string{{"passwordCredentials", "secret"}, {"keyCredentials", "certificate"}} {
		for _, entry := range fields.Objects(item[kind[0]]) {
			found = append(found, AppCredential{
				OwnerKind: ownerKind, OwnerName: fields.Text(item, "displayName"), OwnerID: fields.Text(item, "id"),
				AppID: fields.Text(item, "appId"), Kind: kind[1],
				Name: or(fields.Text(entry, "displayName"), fields.Text(entry, "hint")), KeyID: fields.Text(entry, "keyId"),
				Starts: fields.When(entry, "startDateTime"), Ends: fields.When(entry, "endDateTime"), Raw: entry,
			})
		}
	}
	return found
}

// Expiring is the credentials that end within within of now (and, when asked, expired
// ones), soonest first.
func Expiring(credentials []AppCredential, within time.Duration, now time.Time, includeExpired bool) []AppCredential {
	var selected []AppCredential
	for _, credential := range credentials {
		if !credential.Ends.IsZero() && credential.Ends.Sub(now) <= within && (includeExpired || !credential.Ends.Before(now)) {
			selected = append(selected, credential)
		}
	}
	SortByEnd(selected, now)
	return selected
}

// SortByEnd sorts credentials soonest ending first; one with no end is taken as ending now.
func SortByEnd(credentials []AppCredential, now time.Time) {
	end := func(credential AppCredential) time.Time {
		if credential.Ends.IsZero() {
			return now
		}
		return credential.Ends
	}
	sort.SliceStable(credentials, func(a, b int) bool { return end(credentials[a]).Before(end(credentials[b])) })
}

// Conditional Access ----------------------------------------------------------------------

// CAPolicies is every Conditional Access policy, by name.
func (c *Client) CAPolicies(ctx context.Context) ([]ConditionalAccessPolicy, error) {
	items, err := c.all(ctx, "/v1.0/identity/conditionalAccess/policies", nil, nil)
	if err != nil {
		return nil, err
	}
	policies := make([]ConditionalAccessPolicy, len(items))
	for index, item := range items {
		policies[index] = PolicyFrom(item)
	}
	sort.SliceStable(policies, func(a, b int) bool {
		return strings.ToLower(policies[a].DisplayName) < strings.ToLower(policies[b].DisplayName)
	})
	return policies, nil
}

// Helpers ---------------------------------------------------------------------------------

func (c *Client) groupsOf(ctx context.Context, collection, objectID string, transitive bool) ([]Group, error) {
	id, err := util.RequireGUID(objectID, "an Entra object id")
	if err != nil {
		return nil, err
	}
	items, err := c.all(ctx, "/v1.0/"+collection+"/"+id+"/"+relation(transitive, "transitiveMemberOf", "memberOf")+"/microsoft.graph.group",
		values("$select", GroupSelect, "$count", "true", "$top", "999"), eventual)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, len(items))
	for index, item := range items {
		groups[index] = GroupFrom(item)
	}
	sort.SliceStable(groups, func(a, b int) bool {
		return strings.ToLower(groups[a].DisplayName) < strings.ToLower(groups[b].DisplayName)
	})
	return groups, nil
}

func (c *Client) named(ctx context.Context, path, name, selectFields string) ([]fields.Object, error) {
	return c.all(ctx, path, values("$filter", "displayName eq "+util.ODataString(name), "$select", selectFields), nil)
}

func (c *Client) getOne(ctx context.Context, path, selectFields, missing string) (fields.Object, error) {
	var params url.Values
	if selectFields != "" {
		params = values("$select", selectFields)
	}
	data, err := c.API.Get(ctx, path, params)
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return nil, errs.NotFoundf("%s", missing)
	}
	return data, err
}

func one[T any](items []T, ref, singular, plural string, id func(T) string) (T, error) {
	var zero T
	switch len(items) {
	case 0:
		return zero, errs.NotFoundf("no %s is named '%s'", singular, ref)
	case 1:
		return items[0], nil
	}
	var ids []string
	for _, item := range items {
		ids = append(ids, id(item))
	}
	return zero, errs.Ambiguousf("%d %s are named '%s': %s", len(items), plural, ref, strings.Join(ids, ", ")).
		WithHint("pass the object id instead")
}
