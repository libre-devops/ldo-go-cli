package servicenow

import "strings"

// What each ServiceNow feature needs: roles, since the instance's access controls decide.
// admin passes every access control, so it satisfies every requirement.

// Admin is the role that passes every access control.
const Admin = "admin"

// RoleRequirement is that Feature needs any one of AnyOf (or admin).
type RoleRequirement struct {
	Feature string
	AnyOf   []string
}

// MetBy reports whether roles meet the requirement: admin, or any of the roles it accepts.
func (r RoleRequirement) MetBy(roles []string) bool {
	held := map[string]bool{}
	for _, role := range roles {
		held[strings.ToLower(role)] = true
	}
	if held[Admin] {
		return true
	}
	for _, role := range r.AnyOf {
		if held[strings.ToLower(role)] {
			return true
		}
	}
	return false
}
