// Package pim reads Privileged Identity Management: eligible and active roles, requests,
// approvals and settings, in the three PIM areas in one shape: Azure resource roles
// (through Azure Resource Manager), Entra roles and PIM for Groups (through Microsoft
// Graph). Every call reads; nothing is activated here.
package pim

import (
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Areas are the three PIM areas, in order.
var Areas = []string{"azure", "entra", "groups"}

var roleManagement = []string{"RoleManagement.Read.Directory", "RoleManagement.Read.All", "RoleManagement.ReadWrite.Directory"}

func withRoleManagement(scopes ...string) []string { return append(scopes, roleManagement...) }

// Requirements are what each PIM feature needs from a Graph token, as Graph itself
// reports it: Graph asks for a ReadWrite scope even to list requests. PIM for Azure
// resources goes through ARM and rests on Azure RBAC instead.
var Requirements = []microsoft.Requirement{
	{Feature: "pim entra eligible", Resource: "graph", AllOf: [][]string{withRoleManagement("RoleEligibilitySchedule.Read.Directory",
		"RoleEligibilitySchedule.ReadWrite.Directory")}},
	{Feature: "pim entra active", Resource: "graph", AllOf: [][]string{withRoleManagement("RoleAssignmentSchedule.Read.Directory",
		"RoleAssignmentSchedule.ReadWrite.Directory")}},
	{Feature: "pim entra requests", Resource: "graph", AllOf: [][]string{{"RoleAssignmentSchedule.ReadWrite.Directory",
		"RoleManagement.ReadWrite.Directory"}}},
	{Feature: "pim entra settings", Resource: "graph", AllOf: [][]string{withRoleManagement("RoleManagementPolicy.Read.Directory",
		"RoleManagementPolicy.ReadWrite.Directory")}},
	{Feature: "pim groups eligible", Resource: "graph", AllOf: [][]string{{"PrivilegedEligibilitySchedule.Read.AzureADGroup",
		"PrivilegedEligibilitySchedule.ReadWrite.AzureADGroup", "PrivilegedAccess.Read.AzureADGroup", "PrivilegedAccess.ReadWrite.AzureADGroup"}}},
	{Feature: "pim groups active", Resource: "graph", AllOf: [][]string{{"PrivilegedAssignmentSchedule.Read.AzureADGroup",
		"PrivilegedAssignmentSchedule.ReadWrite.AzureADGroup", "PrivilegedAccess.Read.AzureADGroup", "PrivilegedAccess.ReadWrite.AzureADGroup"}}},
	{Feature: "pim groups requests", Resource: "graph", AllOf: [][]string{{"PrivilegedAssignmentSchedule.ReadWrite.AzureADGroup",
		"PrivilegedAccess.ReadWrite.AzureADGroup"}}},
	{Feature: "pim groups settings", Resource: "graph", AllOf: [][]string{{"RoleManagementPolicy.Read.AzureADGroup",
		"RoleManagementPolicy.ReadWrite.AzureADGroup"}}},
}

// Assignment is a role someone is eligible for, or holds now.
//
// Role is the role's name (for PIM for Groups, member or owner of the group); Scope where
// it applies: an ARM scope, a directory scope, or the group. MemberType is Direct, Group
// (through a group) or Inherited (from a scope above). AssignmentType is Activated
// (through PIM) or Assigned (standing) for active roles, and empty for eligible ones.
type Assignment struct {
	Area           string
	State          string
	Role           string
	Scope          string
	PrincipalID    string
	PrincipalName  string
	MemberType     string
	AssignmentType string
	Starts         time.Time
	Ends           time.Time
	Raw            fields.Object
}

// Permanent reports whether nothing ends it: standing access, the thing PIM exists to
// reduce.
func (a Assignment) Permanent() bool { return a.Ends.IsZero() }

// Activated reports whether the role is held because it was activated, rather than
// assigned outright.
func (a Assignment) Activated() bool { return strings.EqualFold(a.AssignmentType, "activated") }

// Request is a request to activate, assign or remove a role, and where it has got to.
type Request struct {
	Area          string
	ID            string
	Action        string
	Status        string
	Role          string
	Scope         string
	PrincipalID   string
	PrincipalName string
	Justification string
	Created       time.Time
	Starts        time.Time
	Ends          time.Time
	Duration      string
	Ticket        string
	Raw           fields.Object
}

// Pending reports whether the request is waiting for an approver.
func (r Request) Pending() bool { return strings.EqualFold(r.Status, "pendingapproval") }

// Settings is a role's PIM settings: what activating it takes, and how long it lasts.
type Settings struct {
	Area                  string
	Role                  string
	Scope                 string
	MaxActivation         string
	RequiresMFA           bool
	RequiresJustification bool
	RequiresTicket        bool
	RequiresApproval      bool
	Approvers             []string
	AuthenticationContext string
	EligibleExpiry        string
	ActiveExpiry          string
	Rules                 []fields.Object
}

// SettingsFromRules is what a role's PIM policy asks of an activation, read from its
// rules. Azure resources (ARM effectiveRules) and Graph (policy.rules) use the same rule
// ids, such as Expiration_EndUser_Assignment for how long an activation may last, so one
// reader serves all three areas.
func SettingsFromRules(area, role, scope string, rules []fields.Object) Settings {
	byID := map[string]fields.Object{}
	var kept []fields.Object
	for _, rule := range rules {
		id := fields.Text(rule, "id")
		if _, seen := byID[id]; !seen {
			kept = append(kept, rule)
		}
		byID[id] = rule
	}
	enabled := map[string]bool{}
	for _, item := range fields.Items(byID["Enablement_EndUser_Assignment"]["enabledRules"]) {
		enabled[strings.ToLower(fields.String(item))] = true
	}
	setting := fields.Map(byID["Approval_EndUser_Assignment"]["setting"])
	context := byID["AuthenticationContext_EndUser_Assignment"]
	claim := ""
	if context["isEnabled"] == true {
		claim = fields.Text(context, "claimValue")
	}
	return Settings{
		Area: area, Role: role, Scope: scope, MaxActivation: fields.Text(byID["Expiration_EndUser_Assignment"], "maximumDuration"),
		RequiresMFA: enabled["multifactorauthentication"], RequiresJustification: enabled["justification"],
		RequiresTicket: enabled["ticketing"], RequiresApproval: setting["isApprovalRequired"] == true, Approvers: approvers(setting),
		AuthenticationContext: claim, EligibleExpiry: expiry(byID["Expiration_Admin_Eligibility"]),
		ActiveExpiry: expiry(byID["Expiration_Admin_Assignment"]), Rules: kept,
	}
}

func approvers(setting fields.Object) []string {
	var found []string
	for _, stage := range fields.Objects(setting["approvalStages"]) {
		for _, approver := range fields.Objects(stage["primaryApprovers"]) {
			for _, key := range []string{"description", "userId", "groupId", "id"} {
				if name := fields.Text(approver, key); name != "" {
					found = append(found, name)
					break
				}
			}
		}
	}
	return found
}

// expiry is permanent allowed, the maximum duration, or empty when the rule is absent.
func expiry(rule fields.Object) string {
	switch {
	case len(rule) == 0:
		return ""
	case rule["isExpirationRequired"] == false:
		return "permanent allowed"
	}
	if duration := fields.Text(rule, "maximumDuration"); duration != "" {
		return duration
	}
	return "required"
}
