package graphfake

import "time"

// Incident is a Graph security incident created at created, with alerts.
func Incident(id, severity, status string, created time.Time, alerts ...map[string]any) map[string]any {
	value := make([]any, len(alerts))
	for index, alert := range alerts {
		value[index] = alert
	}
	return map[string]any{"id": id, "displayName": "Incident " + id, "severity": severity, "status": status,
		"createdDateTime": created.UTC().Format(time.RFC3339), "lastUpdateDateTime": created.UTC().Format(time.RFC3339),
		"assignedTo": "ana@corp.example", "incidentWebUrl": "https://security.microsoft.com/incidents/" + id,
		"customTags": []any{"patching"}, "alerts": value}
}

// IncidentAlert is an alert in an incident, from source, naming a device and a user.
func IncidentAlert(id, source string) map[string]any {
	return map[string]any{"id": id, "title": "Alert " + id, "severity": "high", "status": "new", "serviceSource": source,
		"createdDateTime": "2026-09-24T09:00:00Z", "evidence": []any{
			map[string]any{"@odata.type": "#microsoft.graph.security.deviceEvidence", "deviceDnsName": "web01.corp.example"},
			map[string]any{"@odata.type": "#microsoft.graph.security.userEvidence", "userAccount": map[string]any{"accountName": "ana"}},
			"not an object",
		}}
}
