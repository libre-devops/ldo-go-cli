// Package armfake is Azure Resource Manager records for tests, as ARM returns them.
package armfake

// Subscription and tenant ids tests use.
const (
	Subscription = "22222222-2222-2222-2222-222222222222"
	Tenant       = "11111111-1111-1111-1111-111111111111"
)

// Page is an ARM page of items, with nextLink when there is more.
func Page(next string, items ...map[string]any) map[string]any {
	value := make([]any, len(items))
	for index, item := range items {
		value[index] = item
	}
	page := map[string]any{"value": value}
	if next != "" {
		page["nextLink"] = next
	}
	return page
}

// Group is a resource group's id.
func Group(name string) string { return "/subscriptions/" + Subscription + "/resourceGroups/" + name }

// AutomationAccount is an Automation account in a resource group.
func AutomationAccount(group, name string) map[string]any {
	return map[string]any{"id": Group(group) + "/providers/Microsoft.Automation/automationAccounts/" + name, "name": name, "location": "uksouth"}
}

// Job is an Automation job of runbook, created at created (RFC 3339).
func Job(id, runbook, status, created, ended string) map[string]any {
	properties := map[string]any{"runbook": map[string]any{"name": runbook}, "status": status, "creationTime": created, "startTime": created}
	if ended != "" {
		properties["endTime"] = ended
	}
	return map[string]any{"name": id, "properties": properties}
}
