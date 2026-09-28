package cli

import (
	"github.com/spf13/cobra"
)

// entraCommand is the entra group: devices, groups, users, the tenant, then tokens.
func entraCommand(rt *Runtime) *cobra.Command {
	group := newGroup("entra", "Entra ID: devices, users, groups, roles and apps.")
	group.AddCommand(
		entraDevices(rt), entraDeviceGroups(rt),
		entraGroupDevices(rt), entraGroupMembers(rt),
		entraUserGroups(rt), entraUserRoles(rt), entraSignIns(rt),
		entraAppCredentials(rt), entraCAPolicies(rt),
		tokenCommand(rt, "token RESOURCE", "Get an access token with the profile's credential and check its claims.",
			"Get an access token with the profile's credential and check its claims.\n\n"+
				"RESOURCE is graph, mde, arm, loganalytics, keyvault, or a resource URL. Exits 1 when a "+
				"check fails (or warns, with --strict). The token itself is printed only with --raw.", ""),
		inspectTokenCommand(rt), signOutCommand(rt))
	return group
}
