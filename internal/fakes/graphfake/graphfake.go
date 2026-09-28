// Package graphfake is Microsoft Graph objects for tests: devices, users and groups as
// Graph returns them, and pages of them.
package graphfake

import (
	"fmt"
	"time"
)

// Page is a Graph collection page of items.
func Page(items ...map[string]any) map[string]any {
	value := make([]any, len(items))
	for index, item := range items {
		value[index] = item
	}
	return map[string]any{"value": value}
}

// ID is a GUID for a test object, from a number, so tests read which is which.
func ID(number int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", number)
}

// Device is an Entra device, last signed in at when (zero for never).
func Device(number int, name string, when time.Time) map[string]any {
	device := map[string]any{
		"id": ID(number), "displayName": name, "deviceId": ID(1000 + number), "operatingSystem": "Windows",
		"operatingSystemVersion": "10.0.26100", "accountEnabled": true, "trustType": "AzureAd",
	}
	if !when.IsZero() {
		device["approximateLastSignInDateTime"] = when.UTC().Format(time.RFC3339)
	}
	return device
}

// User is an Entra user.
func User(number int, upn, name string) map[string]any {
	return map[string]any{"id": ID(number), "userPrincipalName": upn, "displayName": name, "mail": upn,
		"accountEnabled": true, "userType": "Member"}
}

// Group is an Entra group; dynamic for rule-based membership.
func Group(number int, name string, dynamic bool) map[string]any {
	types := []any{}
	if dynamic {
		types = append(types, "DynamicMembership")
	}
	return map[string]any{"id": ID(number), "displayName": name, "groupTypes": types, "securityEnabled": true}
}
