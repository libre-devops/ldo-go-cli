package microsoft

import (
	"regexp"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// A Log Analytics workspace has three names, and they are easy to mix up: its Workspace
// ID (a GUID on its Overview page), which the query API wants; its resource id, which
// Resource Manager wants; and its name. WorkspaceRefOf takes any of them and says which
// it was, so a command can take whichever a person has to hand.

// WorkspaceType is a Log Analytics workspace's resource type.
const WorkspaceType = "Microsoft.OperationalInsights/workspaces"

// WorkspaceHint is what a person can give to name a workspace.
const WorkspaceHint = "give the workspace's Workspace ID (a GUID, on its Overview page), its resource id " +
	"(/subscriptions/.../providers/Microsoft.OperationalInsights/workspaces/NAME), or its name"

// A workspace's name: 4 to 63 letters, digits and hyphens, not starting or ending with one.
var workspaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{2,61}[A-Za-z0-9]$`)

// The kinds of workspace name.
const (
	WorkspaceID         = "workspace id"
	WorkspaceResourceID = "resource id"
	WorkspaceName       = "name"
)

// WorkspaceRef is a workspace as someone named it: Kind says which of its three names
// Value is.
type WorkspaceRef struct {
	Kind       string
	Value      string
	ResourceID *ResourceID
}

// WorkspaceRefOf is text as a workspace's Workspace ID, resource id or name, whichever it
// is; an Input error saying what it looks like instead when it is none of them.
func WorkspaceRefOf(text string) (WorkspaceRef, error) {
	value := strings.TrimSpace(text)
	if util.IsGUID(value) {
		return WorkspaceRef{Kind: WorkspaceID, Value: strings.ToLower(value)}, nil
	}
	if LooksLikeResourceID(value) {
		found, err := ParseResourceID(value)
		if err != nil {
			return WorkspaceRef{}, err
		}
		if !found.IsType(WorkspaceType) {
			kind := found.Type()
			if kind == "" {
				kind = "tenant"
			}
			return WorkspaceRef{}, errs.Inputf("that is the resource id of a %s, not of a Log Analytics workspace: %q",
				kind, found.ID).WithHint("%s", WorkspaceHint)
		}
		return WorkspaceRef{Kind: WorkspaceResourceID, Value: found.ID, ResourceID: &found}, nil
	}
	if workspaceName.MatchString(value) {
		return WorkspaceRef{Kind: WorkspaceName, Value: value}, nil
	}
	return WorkspaceRef{}, errs.Inputf("not a Log Analytics workspace: %q", text).WithHint("%s", WorkspaceHint)
}
