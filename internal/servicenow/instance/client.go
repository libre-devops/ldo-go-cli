package instance

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
)

// Security Incident Response: its plugin, its application scope and its main table.
const (
	SIRPlugin = "com.snc.security_incident"
	SIRScope  = "sn_si"
	SIRTable  = "sn_si_incident"
)

// Client reads the instance's own records through the Table API.
type Client struct {
	Tables *servicenow.Tables
}

// CurrentUser is the signed-in user, as the instance sees it; or, with a name, the user
// called that.
func (c *Client) CurrentUser(ctx context.Context, userName string) (User, error) {
	// gs.getUserID() is one of the functions an encoded query may call, and it names
	// whoever the request is authenticated as, however they signed in.
	query := "sys_id=javascript:gs.getUserID()"
	if userName != "" {
		var err error
		if query, err = servicenow.Condition("user_name", userName, ""); err != nil {
			return User{}, err
		}
	}
	record, found, err := c.Tables.First(ctx, "sys_user", query, UserFields...)
	if err != nil {
		return User{}, err
	}
	if !found {
		who := "for this sign-in"
		if userName != "" {
			who = "named '" + userName + "'"
		}
		return User{}, errs.NotFoundf("no ServiceNow user %s", who)
	}
	return UserFrom(record), nil
}

// Roles are every role the user holds, directly or through a group or another role.
func (c *Client) Roles(ctx context.Context, user User) ([]string, error) {
	condition, err := servicenow.Condition("user", user.SysID, "")
	if err != nil {
		return nil, err
	}
	rows, err := c.Tables.Records(ctx, "sys_user_has_role", servicenow.Query{Query: condition + "^state=active", Fields: []string{"role.name"}})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	roles := []string{}
	for _, row := range rows {
		if name := fields.Text(row, "role.name"); name != "" && !seen[name] {
			seen[name] = true
			roles = append(roles, name)
		}
	}
	sort.Strings(roles)
	return roles, nil
}

// Release is the release, or false when this account may not read system properties.
func (c *Client) Release(ctx context.Context) (Release, bool, error) {
	rows, err := c.Tables.Records(ctx, "sys_properties", servicenow.Query{Query: "nameINglide.buildtag.last,glide.war", Fields: []string{"name", "value"}})
	if found := errs.As(err); found != nil && found.Status == http.StatusForbidden {
		return Release{}, false, nil
	}
	if err != nil {
		return Release{}, false, err
	}
	values := map[string]string{}
	for _, row := range rows {
		values[fields.Text(row, "name")] = fields.Text(row, "value")
	}
	// glide.buildtag.last is not kept as a record on every release; glide.war is.
	tag := values["glide.buildtag.last"]
	if tag == "" {
		tag = values["glide.war"]
	}
	if tag == "" {
		return Release{}, false, nil
	}
	return ReleaseFrom(tag), true, nil
}

// Applications are the scoped applications, by name or scope, active ones first.
func (c *Client) Applications(ctx context.Context, search string, activeOnly bool) ([]Application, error) {
	query := ""
	if search != "" {
		byName, err := servicenow.Condition("name", search, "LIKE")
		if err != nil {
			return nil, err
		}
		byScope, _ := servicenow.Condition("scope", search, "LIKE")
		query = byName + "^OR" + byScope
	}
	rows, err := c.Tables.Records(ctx, "sys_scope", servicenow.Query{Query: query, Fields: ApplicationFields})
	if err != nil {
		return nil, err
	}
	found := []Application{}
	for _, row := range rows {
		if app := ApplicationFrom(row); app.Active || !activeOnly {
			found = append(found, app)
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Active != found[j].Active {
			return found[i].Active
		}
		return strings.ToLower(found[i].Name) < strings.ToLower(found[j].Name)
	})
	return found, nil
}

// TableExists reports whether the instance has a table called name.
func (c *Client) TableExists(ctx context.Context, name string) (bool, error) {
	condition, err := servicenow.Condition("name", name, "")
	if err != nil {
		return false, err
	}
	_, found, err := c.Tables.First(ctx, "sys_db_object", condition, "name")
	return found, err
}

// SecurityIncidentResponse is whether Security Incident Response is installed: its table
// is the proof.
func (c *Client) SecurityIncidentResponse(ctx context.Context) (AppStatus, error) {
	name := "Security Incident Response"
	exists, err := c.TableExists(ctx, SIRTable)
	if err != nil || !exists {
		return AppStatus{Name: name, Detail: "no " + SIRTable + " table"}, err
	}
	app, found, err := c.Tables.First(ctx, "sys_scope", "scope="+SIRScope, "version", "active")
	if err != nil {
		return AppStatus{}, err
	}
	if found {
		return AppStatus{Name: name, Installed: true, Version: fields.Text(app, "version"), Detail: "application " + SIRScope}, nil
	}
	return AppStatus{Name: name, Installed: true, Detail: "table " + SIRTable}, nil
}
