// Package xdrfake is Defender for Endpoint records for tests, as the API returns them.
package xdrfake

import (
	"strings"
	"time"
)

// MachineID is a machine id (40 hex characters) for a test machine, from a digit.
func MachineID(digit rune) string { return strings.Repeat(string(digit), 40) }

// Machine is a machine record, last seen at seen.
func Machine(digit rune, name string, seen time.Time) map[string]any {
	return map[string]any{"id": MachineID(digit), "computerDnsName": name, "onboardingStatus": "Onboarded",
		"healthStatus": "Active", "lastSeen": seen.UTC().Format(time.RFC3339), "osPlatform": "Windows11",
		"osVersion": "10.0", "machineTags": []any{"prod"}, "rbacGroupName": "Servers", "aadDeviceId": "a1"}
}

// Page is a Defender page of items.
func Page(items ...map[string]any) map[string]any {
	value := make([]any, len(items))
	for index, item := range items {
		value[index] = item
	}
	return map[string]any{"value": value}
}

// Alert is an alert created at created.
func Alert(id, severity, status string, created time.Time) map[string]any {
	return map[string]any{"id": id, "title": "Suspicious " + id, "severity": severity, "status": status,
		"alertCreationTime": created.UTC().Format(time.RFC3339), "computerDnsName": "web01", "category": "Execution",
		"detectionSource": "EDR", "machineId": MachineID('1')}
}
