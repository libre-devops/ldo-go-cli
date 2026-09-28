package entra

import (
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
)

// Every model keeps the full Graph record in Raw, for JSON output.

// DeviceSelect are the device properties read.
const DeviceSelect = "id,displayName,deviceId,operatingSystem,operatingSystemVersion," +
	"accountEnabled,trustType,approximateLastSignInDateTime"

// Device is an Entra device object.
type Device struct {
	ID              string
	DisplayName     string
	DeviceID        string
	OperatingSystem string
	OSVersion       string
	Enabled         *bool
	TrustType       string
	LastSignIn      time.Time
	Raw             fields.Object
}

func flag(value any) *bool {
	if found, ok := fields.Flag(value); ok {
		return &found
	}
	return nil
}

// DeviceFrom is a device as Graph returns it.
func DeviceFrom(data fields.Object) Device {
	return Device{
		ID: fields.Text(data, "id"), DisplayName: fields.Text(data, "displayName"), DeviceID: fields.Text(data, "deviceId"),
		OperatingSystem: fields.Text(data, "operatingSystem"), OSVersion: fields.Text(data, "operatingSystemVersion"),
		Enabled: flag(data["accountEnabled"]), TrustType: fields.Text(data, "trustType"),
		LastSignIn: fields.When(data, "approximateLastSignInDateTime"), Raw: data,
	}
}

// GroupSelect are the group properties read.
const GroupSelect = "id,displayName,description,groupTypes,securityEnabled,mailEnabled,membershipRule"

// Group is an Entra group. Dynamic is true for rule-based membership.
type Group struct {
	ID              string
	DisplayName     string
	Description     string
	Dynamic         bool
	SecurityEnabled *bool
	MembershipRule  string
	Raw             fields.Object
}

// GroupFrom is a group as Graph returns it; Dynamic from its group types.
func GroupFrom(data fields.Object) Group {
	dynamic := false
	for _, kind := range fields.Strings(data["groupTypes"]) {
		dynamic = dynamic || kind == "DynamicMembership"
	}
	return Group{
		ID: fields.Text(data, "id"), DisplayName: fields.Text(data, "displayName"), Description: fields.Text(data, "description"),
		Dynamic: dynamic, SecurityEnabled: flag(data["securityEnabled"]), MembershipRule: fields.Text(data, "membershipRule"),
		Raw: data,
	}
}

// DeviceLookup is one device name looked up: the Entra devices that have it (stale
// registrations share names, so there may be several), and which of the groups asked
// about they are in.
type DeviceLookup struct {
	Query   string
	Devices []Device
	Groups  []Group
	// Members is each group's id, and the object ids of every device in it.
	Members map[string]map[string]bool
}

// Found reports whether Entra has any device with the name.
func (l DeviceLookup) Found() bool { return len(l.Devices) > 0 }

// InGroup reports whether device, or else any device with the name, is in group.
func (l DeviceLookup) InGroup(group Group, device *Device) bool {
	ids := l.Members[group.ID]
	if device != nil {
		return ids[device.ID]
	}
	for _, item := range l.Devices {
		if ids[item.ID] {
			return true
		}
	}
	return false
}

// InEveryGroup reports whether a device with the name is in every group asked about.
func (l DeviceLookup) InEveryGroup() bool {
	for _, group := range l.Groups {
		if !l.InGroup(group, nil) {
			return false
		}
	}
	return true
}

// UserSelect are the user properties read.
const UserSelect = "id,displayName,userPrincipalName,mail,accountEnabled,userType,onPremisesSyncEnabled"

// User is an Entra user.
type User struct {
	ID                string
	DisplayName       string
	UserPrincipalName string
	Mail              string
	Enabled           *bool
	UserType          string
	Synced            *bool
	Raw               fields.Object
}

// UserFrom is a user as Graph returns it.
func UserFrom(data fields.Object) User {
	return User{
		ID: fields.Text(data, "id"), DisplayName: fields.Text(data, "displayName"),
		UserPrincipalName: fields.Text(data, "userPrincipalName"), Mail: fields.Text(data, "mail"),
		Enabled: flag(data["accountEnabled"]), UserType: fields.Text(data, "userType"),
		Synced: flag(data["onPremisesSyncEnabled"]), Raw: data,
	}
}

// DirectoryObject is a group member of any type. Kind is user, device, group, ...; Detail
// is what best tells one of its kind apart: a user's UPN, a device's operating system, a
// service principal's app id.
type DirectoryObject struct {
	ID          string
	Kind        string
	DisplayName string
	Detail      string
	Raw         fields.Object
}

// DirectoryObjectFrom is any directory object, its kind from @odata.type unless kind says.
func DirectoryObjectFrom(data fields.Object, kind string) DirectoryObject {
	if kind == "" {
		kind = strings.TrimPrefix(fields.Text(data, "@odata.type"), "#microsoft.graph.")
	}
	if kind == "" {
		kind = "object"
	}
	detail := map[string]string{
		"user": fields.Text(data, "userPrincipalName"), "device": fields.Text(data, "operatingSystem"),
		"servicePrincipal": fields.Text(data, "appId"),
	}[kind]
	return DirectoryObject{ID: fields.Text(data, "id"), Kind: kind, DisplayName: fields.Text(data, "displayName"),
		Detail: detail, Raw: data}
}

// RoleAssignment is a directory role a principal holds: active now, or eligible through
// PIM.
type RoleAssignment struct {
	RoleName       string
	RoleTemplateID string
	State          string
	Scope          string
	Ends           time.Time
	Raw            fields.Object
}

// RoleFromDirectoryRole is a directoryRole the principal is a member of (tenant-wide,
// active).
func RoleFromDirectoryRole(data fields.Object) RoleAssignment {
	return RoleAssignment{RoleName: fields.Text(data, "displayName"), RoleTemplateID: fields.Text(data, "roleTemplateId"),
		State: "active", Scope: "/", Raw: data}
}

// RoleFromEligibility is a PIM unifiedRoleEligibilitySchedule expanded with its role
// definition.
func RoleFromEligibility(data fields.Object) RoleAssignment {
	definition := fields.Map(data["roleDefinition"])
	expiration := fields.Map(fields.Map(data["scheduleInfo"])["expiration"])
	roleID := fields.Text(data, "roleDefinitionId")
	return RoleAssignment{
		RoleName: or(fields.Text(definition, "displayName"), roleID), RoleTemplateID: or(fields.Text(definition, "templateId"), roleID),
		State: "eligible", Scope: or(fields.Text(data, "directoryScopeId"), "/"), Ends: fields.When(expiration, "endDateTime"),
		Raw: data,
	}
}

func or(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}

// RoleReport is a principal's directory roles. Eligible is nil when PIM could not be
// read, and EligibleError says why.
type RoleReport struct {
	Active        []RoleAssignment
	Eligible      []RoleAssignment
	EligibleError string
}

// SignIn is one Entra sign-in event. ErrorCode 0 is a success.
type SignIn struct {
	ID                string
	Created           time.Time
	User              string
	App               string
	IPAddress         string
	ClientApp         string
	ConditionalAccess string
	ErrorCode         int
	FailureReason     string
	DeviceName        string
	OperatingSystem   string
	Location          string
	Raw               fields.Object
}

// Succeeded reports whether the sign-in succeeded (error code 0).
func (s SignIn) Succeeded() bool { return s.ErrorCode == 0 }

// SignInFrom is a sign-in log entry as Graph returns it.
func SignInFrom(data fields.Object) SignIn {
	status := fields.Map(data["status"])
	device := fields.Map(data["deviceDetail"])
	location := fields.Map(data["location"])
	var place []string
	for _, part := range []string{fields.Text(location, "city"), fields.Text(location, "countryOrRegion")} {
		if part != "" {
			place = append(place, part)
		}
	}
	code := 0
	if number, ok := status["errorCode"].(float64); ok {
		code = int(number)
	}
	return SignIn{
		ID: fields.Text(data, "id"), Created: fields.When(data, "createdDateTime"), User: fields.Text(data, "userPrincipalName"),
		App: fields.Text(data, "appDisplayName"), IPAddress: fields.Text(data, "ipAddress"),
		ClientApp: fields.Text(data, "clientAppUsed"), ConditionalAccess: fields.Text(data, "conditionalAccessStatus"),
		ErrorCode: code, FailureReason: fields.Text(status, "failureReason"), DeviceName: fields.Text(device, "displayName"),
		OperatingSystem: fields.Text(device, "operatingSystem"), Location: strings.Join(place, ", "), Raw: data,
	}
}

// AppCredential is a client secret or certificate on an app registration or a service
// principal.
type AppCredential struct {
	OwnerKind string // application, or service principal
	OwnerName string
	OwnerID   string
	AppID     string
	Kind      string // secret, or certificate
	Name      string
	KeyID     string
	Starts    time.Time
	Ends      time.Time
	Raw       fields.Object
}

// DaysLeft is whole days until expiry (negative once expired), and false with no end.
func (c AppCredential) DaysLeft(now time.Time) (int, bool) {
	if c.Ends.IsZero() {
		return 0, false
	}
	// Whole days, rounded down as Python's timedelta.days does.
	span := c.Ends.Sub(now)
	days := int(span / (24 * time.Hour))
	if span < 0 && span%(24*time.Hour) != 0 {
		days--
	}
	return days, true
}

// ConditionalAccessPolicy is a Conditional Access policy, flattened to who and what it
// targets and what it requires.
type ConditionalAccessPolicy struct {
	ID                  string
	DisplayName         string
	State               string
	IncludeUsers        []string
	ExcludeUsers        []string
	IncludeGroups       []string
	ExcludeGroups       []string
	IncludeRoles        []string
	IncludeApplications []string
	ExcludeApplications []string
	ClientAppTypes      []string
	GrantControls       []string
	GrantOperator       string
	SessionControls     []string
	Modified            time.Time
	Raw                 fields.Object
}

// PolicyFrom is a Conditional Access policy as Graph returns it, conditions and controls
// flattened.
func PolicyFrom(data fields.Object) ConditionalAccessPolicy {
	conditions := fields.Map(data["conditions"])
	users := fields.Map(conditions["users"])
	apps := fields.Map(conditions["applications"])
	grant := fields.Map(data["grantControls"])
	session := fields.Map(data["sessionControls"])
	controls := append(fields.Strings(grant["builtInControls"]), fields.Strings(grant["customAuthenticationFactors"])...)
	if truthy(grant["termsOfUse"]) {
		controls = append(controls, "terms of use")
	}
	var sessionControls []string
	for _, name := range sortedKeys(session) {
		if name != "@odata.type" && truthy(session[name]) {
			sessionControls = append(sessionControls, name)
		}
	}
	modified := fields.When(data, "modifiedDateTime")
	if modified.IsZero() {
		modified = fields.When(data, "createdDateTime")
	}
	return ConditionalAccessPolicy{
		ID: fields.Text(data, "id"), DisplayName: fields.Text(data, "displayName"), State: fields.Text(data, "state"),
		IncludeUsers: fields.Strings(users["includeUsers"]), ExcludeUsers: fields.Strings(users["excludeUsers"]),
		IncludeGroups: fields.Strings(users["includeGroups"]), ExcludeGroups: fields.Strings(users["excludeGroups"]),
		IncludeRoles: fields.Strings(users["includeRoles"]), IncludeApplications: fields.Strings(apps["includeApplications"]),
		ExcludeApplications: fields.Strings(apps["excludeApplications"]), ClientAppTypes: fields.Strings(conditions["clientAppTypes"]),
		GrantControls: controls, GrantOperator: fields.Text(grant, "operator"), SessionControls: sessionControls,
		Modified: modified, Raw: data,
	}
}

// truthy is Python's truth for a JSON value: not null, false, zero, or empty.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func sortedKeys(data fields.Object) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
