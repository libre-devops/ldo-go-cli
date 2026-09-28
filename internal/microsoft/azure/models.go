package azure

import (
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Subscription is an Azure subscription the credential can see.
type Subscription struct {
	ID       string
	Name     string
	State    string
	TenantID string
	Raw      fields.Object
}

// SubscriptionFrom is a subscription as ARM lists it.
func SubscriptionFrom(data fields.Object) Subscription {
	return Subscription{ID: strings.ToLower(fields.Text(data, "subscriptionId")), Name: fields.Text(data, "displayName"),
		State: fields.Text(data, "state"), TenantID: strings.ToLower(fields.Text(data, "tenantId")), Raw: data}
}

// RoleAssignment is an Azure RBAC role assignment. RoleName is resolved from the
// definition.
type RoleAssignment struct {
	ID               string
	Scope            string
	RoleName         string
	RoleDefinitionID string
	PrincipalID      string
	PrincipalType    string
	Condition        string
	Raw              fields.Object
}

// RoleAssignmentFrom is a role assignment as ARM returns it, with its role's name looked
// up separately.
func RoleAssignmentFrom(data fields.Object, roleName string) RoleAssignment {
	properties := fields.Map(data["properties"])
	return RoleAssignment{ID: fields.Text(data, "id"), Scope: fields.Text(properties, "scope"), RoleName: roleName,
		RoleDefinitionID: fields.Text(properties, "roleDefinitionId"), PrincipalID: fields.Text(properties, "principalId"),
		PrincipalType: fields.Text(properties, "principalType"), Condition: fields.Text(properties, "condition"), Raw: data}
}

func number(value any) *float64 {
	if found, ok := fields.Number(value); ok {
		return &found
	}
	return nil
}

// SecureScore is a subscription's Defender for Cloud secure score.
type SecureScore struct {
	SubscriptionID string
	Current        *float64
	Max            *float64
	Percentage     *float64
	Raw            fields.Object
}

// SecureScoreFrom is a subscription's secure score as Defender for Cloud returns it.
func SecureScoreFrom(subscriptionID string, data fields.Object) SecureScore {
	score := fields.Map(fields.Map(data["properties"])["score"])
	return SecureScore{SubscriptionID: subscriptionID, Current: number(score["current"]), Max: number(score["max"]),
		Percentage: number(score["percentage"]), Raw: data}
}

// SecureScoreControl is one security control and how much of the secure score it costs.
type SecureScoreControl struct {
	Name          string
	Current       *float64
	Max           *float64
	Percentage    *float64
	Healthy       int
	Unhealthy     int
	NotApplicable int
	Raw           fields.Object
}

// PointsLost is the points this control could still earn.
func (c SecureScoreControl) PointsLost() float64 { return value(c.Max) - value(c.Current) }

func value(number *float64) float64 {
	if number == nil {
		return 0
	}
	return *number
}

// SecureScoreControlFrom is one control, with how many resources pass and fail it.
func SecureScoreControlFrom(data fields.Object) SecureScoreControl {
	properties := fields.Map(data["properties"])
	score := fields.Map(properties["score"])
	count := func(key string) int {
		if found, ok := properties[key].(float64); ok && found == float64(int(found)) {
			return int(found)
		}
		return 0
	}
	name := fields.Text(properties, "displayName")
	if name == "" {
		name = fields.Text(data, "name")
	}
	return SecureScoreControl{Name: name, Current: number(score["current"]), Max: number(score["max"]),
		Percentage: number(score["percentage"]), Healthy: count("healthyResourceCount"), Unhealthy: count("unhealthyResourceCount"),
		NotApplicable: count("notApplicableResourceCount"), Raw: data}
}

// Assessment is a Defender for Cloud recommendation's result for one resource.
type Assessment struct {
	ID         string
	Name       string
	Status     string
	Severity   string
	ResourceID string
	Cause      string
	Raw        fields.Object
}

// Unhealthy reports whether the resource fails the assessment.
func (a Assessment) Unhealthy() bool { return strings.EqualFold(a.Status, "unhealthy") }

// AssessmentFrom is one assessment of one resource. An assessment is an extension
// resource: its id is the resource's with the assessment appended, so what it is on is
// its scope.
func AssessmentFrom(data fields.Object) Assessment {
	properties := fields.Map(data["properties"])
	status := fields.Map(properties["status"])
	metadata := fields.Map(properties["metadata"])
	id := fields.Text(data, "id")
	resource := ""
	if parsed, ok := microsoft.TryParseResourceID(id); ok {
		resource = parsed.Scope()
	}
	name := fields.Text(properties, "displayName")
	if name == "" {
		name = fields.Text(data, "name")
	}
	cause := fields.Text(status, "cause")
	if cause == "" {
		cause = fields.Text(status, "description")
	}
	return Assessment{ID: id, Name: name, Status: fields.Text(status, "code"), Severity: fields.Text(metadata, "severity"),
		ResourceID: resource, Cause: cause, Raw: data}
}

// DefenderPlan is a Defender for Cloud plan and whether it is on (Standard) or off (Free).
type DefenderPlan struct {
	Name           string
	PricingTier    string
	SubPlan        string
	TrialRemaining string
	EnabledSince   time.Time
	Deprecated     bool
	Raw            fields.Object
}

// Enabled reports whether the plan is on (the Standard tier; Free is off).
func (p DefenderPlan) Enabled() bool { return strings.EqualFold(p.PricingTier, "standard") }

// DefenderPlanFrom is one Defender for Cloud plan as ARM returns it.
func DefenderPlanFrom(data fields.Object) DefenderPlan {
	properties := fields.Map(data["properties"])
	return DefenderPlan{Name: fields.Text(data, "name"), PricingTier: fields.Text(properties, "pricingTier"),
		SubPlan: fields.Text(properties, "subPlan"), TrialRemaining: fields.Text(properties, "freeTrialRemainingTime"),
		EnabledSince: fields.When(properties, "enablementTime"), Deprecated: properties["deprecated"] == true, Raw: data}
}

// Workspace is a Log Analytics workspace by all three of its names: its resource ID, its
// Name, and its WorkspaceID (the GUID the portal calls the Workspace ID, which the query
// API wants).
type Workspace struct {
	ID             string
	Name           string
	WorkspaceID    string
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Raw            fields.Object
}

// WorkspaceFrom is a workspace as ARM returns it (or Resource Graph, projecting the same
// fields).
func WorkspaceFrom(data fields.Object) Workspace {
	id := fields.Text(data, "id")
	workspace := Workspace{ID: id, Name: fields.Text(data, "name"),
		WorkspaceID: strings.ToLower(fields.Text(fields.Map(data["properties"]), "customerId")), Location: fields.Text(data, "location"), Raw: data}
	if parsed, ok := microsoft.TryParseResourceID(id); ok {
		workspace.SubscriptionID, workspace.ResourceGroup = parsed.Subscription, parsed.ResourceGroup
	}
	return workspace
}
