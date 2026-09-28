package incidents

import (
	"slices"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
)

// In the unified security operations platform, one incident queue holds Defender's own
// incidents and those from Microsoft Sentinel. Each alert names the service that raised
// it (serviceSource), which is how an incident is known to come from Sentinel.

// Sources are Graph's serviceSource values, and a short name for each.
var Sources = map[string]string{
	"microsoftDefenderForEndpoint":   "Endpoint",
	"microsoftDefenderForIdentity":   "Identity",
	"microsoftDefenderForCloudApps":  "Cloud Apps",
	"microsoftDefenderForOffice365":  "Office 365",
	"microsoft365Defender":           "XDR",
	"azureAdIdentityProtection":      "Entra ID Protection",
	"microsoftAppGovernance":         "App Governance",
	"dataLossPrevention":             "DLP",
	"microsoftDefenderForCloud":      "Defender for Cloud",
	"microsoftSentinel":              "Sentinel",
	"microsoftInsiderRiskManagement": "Insider Risk",
}

// SeverityOrder is most severe first; unknown values rank below informational.
var SeverityOrder = []string{"high", "medium", "low", "informational"}

// OpenStatuses are Graph's statuses that mean open; Statuses all of them.
var (
	OpenStatuses = []string{"active", "inProgress", "awaitingAction"}
	Statuses     = append(slices.Clone(OpenStatuses), "resolved", "redirected")
)

// SeverityRank is 0 for high, up to 4 for unknown: sort ascending for most severe first.
func SeverityRank(severity string) int {
	if index := slices.Index(SeverityOrder, strings.ToLower(severity)); index >= 0 {
		return index
	}
	return len(SeverityOrder)
}

func sourceName(source string) string {
	if name, ok := Sources[source]; ok {
		return name
	}
	return source
}

func appendNew(values []string, value string) []string {
	if value == "" || slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

// Alert is one alert in an incident. Source is Graph's serviceSource.
type Alert struct {
	ID              string
	Title           string
	Severity        string
	Status          string
	Source          string
	DetectionSource string
	Created         time.Time
	Devices         []string
	Users           []string
	Raw             fields.Object
}

// SourceName is the product that raised the alert, by the name the portal gives it.
func (a Alert) SourceName() string {
	if a.Source == "" {
		return "unknown"
	}
	return sourceName(a.Source)
}

// AlertFrom is an alert as Graph returns it, with the devices and users its evidence names.
func AlertFrom(record fields.Object) Alert {
	var devices, users []string
	for _, evidence := range fields.Objects(record["evidence"]) {
		kind := fields.Text(evidence, "@odata.type")
		switch {
		case strings.HasSuffix(kind, "deviceEvidence"):
			name := fields.Text(evidence, "deviceDnsName")
			if name == "" {
				name = fields.Text(evidence, "hostName")
			}
			devices = appendNew(devices, name)
		case strings.HasSuffix(kind, "userEvidence"):
			account := fields.Map(evidence["userAccount"])
			name := fields.Text(account, "userPrincipalName")
			if name == "" {
				name = fields.Text(account, "accountName")
			}
			users = appendNew(users, name)
		}
	}
	return Alert{
		ID: fields.Text(record, "id"), Title: fields.Text(record, "title"), Severity: fields.Text(record, "severity"),
		Status: fields.Text(record, "status"), Source: fields.Text(record, "serviceSource"),
		DetectionSource: fields.Text(record, "detectionSource"), Created: fields.When(record, "createdDateTime"),
		Devices: devices, Users: users, Raw: record,
	}
}

// Incident is one incident, with its alerts.
type Incident struct {
	ID             string
	Title          string
	Severity       string
	Status         string
	Created        time.Time
	Updated        time.Time
	AssignedTo     string
	Classification string
	Determination  string
	WebURL         string
	Tags           []string
	Alerts         []Alert
	Raw            fields.Object
}

// Open reports whether the incident is still open (active, in progress, or awaiting
// action).
func (i Incident) Open() bool { return slices.Contains(OpenStatuses, i.Status) }

// Sources are the services that raised its alerts (Graph values), in first-seen order.
func (i Incident) Sources() []string {
	var found []string
	for _, alert := range i.Alerts {
		found = appendNew(found, alert.Source)
	}
	return found
}

// SourceNames are the products that raised the incident's alerts, by their portal names.
func (i Incident) SourceNames() []string {
	var names []string
	for _, source := range i.Sources() {
		names = append(names, sourceName(source))
	}
	return names
}

// Devices is every device the alerts name, once each, in order.
func (i Incident) Devices() []string {
	var found []string
	for _, alert := range i.Alerts {
		for _, name := range alert.Devices {
			found = appendNew(found, name)
		}
	}
	return found
}

// Users is every user the alerts name, once each, in order.
func (i Incident) Users() []string {
	var found []string
	for _, alert := range i.Alerts {
		for _, name := range alert.Users {
			found = appendNew(found, name)
		}
	}
	return found
}

// IncidentFrom is an incident as Graph returns it, with its alerts when expanded.
func IncidentFrom(record fields.Object) Incident {
	var tags []string
	for _, key := range []string{"customTags", "systemTags"} {
		for _, tag := range fields.Items(record[key]) {
			tags = append(tags, fields.String(tag))
		}
	}
	var alerts []Alert
	for _, alert := range fields.Objects(record["alerts"]) {
		alerts = append(alerts, AlertFrom(alert))
	}
	return Incident{
		ID: fields.Text(record, "id"), Title: fields.Text(record, "displayName"), Severity: fields.Text(record, "severity"),
		Status: fields.Text(record, "status"), Created: fields.When(record, "createdDateTime"),
		Updated: fields.When(record, "lastUpdateDateTime"), AssignedTo: fields.Text(record, "assignedTo"),
		Classification: fields.Text(record, "classification"), Determination: fields.Text(record, "determination"),
		WebURL: fields.Text(record, "incidentWebUrl"), Tags: tags, Alerts: alerts, Raw: record,
	}
}
