package detections

import (
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
)

// Custom detection rules are Defender XDR's own analytics rules: an Advanced Hunting query
// on a schedule, and the alert (and any automated response) it raises. With Sentinel run
// from the Defender portal they are where detections now live.
//
// This reads the current shape of the API: status, schedule, queryCondition and
// detectionAction. Microsoft removes the legacy properties (isEnabled, detectorId,
// lastRunDetails and others) on 2026-10-01, so nothing here depends on them; until then a
// rule written the old way may carry only isEnabled or a schedule period, which are read
// as a fallback. With lastRunDetails gone, the API no longer says how a rule's last run
// went: a status of autoDisabled, Defender turning the rule off itself (usually after its
// query failed again and again), is the signal that is left.

// Statuses are the statuses a rule can have (folded), and what each means to a person.
var Statuses = [][2]string{{"enabled", "enabled"}, {"disabled", "disabled"}, {"autodisabled", "turned off by Defender"}}

// StatusLabel is what a status means to a person.
func StatusLabel(status string) string {
	for _, known := range Statuses {
		if known[0] == strings.ToLower(status) {
			return known[1]
		}
	}
	return status
}

// frequencies are how often a rule runs (schedule.frequency), as a person says it. PT0S is
// continuous: the rule runs on events as they arrive (near real time).
var frequencies = map[string]string{"PT0S": "continuous", "PT1H": "every 1h", "PT3H": "every 3h", "PT12H": "every 12h",
	"PT24H": "every 24h", "P1D": "every 24h"}

// legacyPeriods are the legacy schedule.period (removed on 2026-10-01), as the frequency
// each stood for.
var legacyPeriods = map[string]string{"0": "PT0S", "1H": "PT1H", "3H": "PT3H", "12H": "PT12H", "24H": "PT24H"}

// Rule is one custom detection rule. Tactics are its ATT&CK tactics in the order the rule
// lists them, and Techniques its techniques and sub-techniques, together.
type Rule struct {
	ID          string
	DisplayName string
	Description string
	Status      string
	Frequency   string
	NextRun     time.Time
	Title       string
	Severity    string
	Tactics     []string
	Techniques  []string
	Query       string
	CreatedBy   string
	Created     time.Time
	ModifiedBy  string
	Modified    time.Time
	Raw         fields.Object
}

// AutoDisabled reports whether Defender turned the rule off itself, usually after its
// query kept failing.
func (r Rule) AutoDisabled() bool { return strings.EqualFold(r.Status, "autodisabled") }

// Schedule is how often it runs, as a person says it: continuous, every 3h.
func (r Rule) Schedule() string {
	if said, ok := frequencies[r.Frequency]; ok {
		return said
	}
	if r.Frequency == "" {
		return "-"
	}
	return r.Frequency
}

// RuleFrom is a rule as Graph returns it, in the current shape or the legacy one.
func RuleFrom(data fields.Object) Rule {
	schedule := fields.Map(data["schedule"])
	template := AlertTemplate(data)
	tactics, techniques := mitre(template)
	return Rule{
		ID: fields.Text(data, "id"), DisplayName: fields.Text(data, "displayName"), Description: fields.Text(data, "description"),
		Status: RuleStatus(data), Frequency: RuleFrequency(data), NextRun: fields.When(schedule, "nextRunDateTime"),
		Title: fields.Text(template, "title"), Severity: fields.Text(template, "severity"), Tactics: tactics, Techniques: techniques,
		Query: fields.Text(fields.Map(data["queryCondition"]), "queryText"), CreatedBy: fields.Text(data, "createdBy"),
		Created: fields.When(data, "createdDateTime"), ModifiedBy: fields.Text(data, "lastModifiedBy"),
		Modified: fields.When(data, "lastModifiedDateTime"), Raw: data,
	}
}

// AlertTemplate is the alert a rule raises: detectionAction.alertTemplate.
func AlertTemplate(data fields.Object) fields.Object {
	return fields.Map(fields.Map(data["detectionAction"])["alertTemplate"])
}

// RuleStatus is status, else what the legacy isEnabled said, else empty.
func RuleStatus(data fields.Object) string {
	if status := fields.Text(data, "status"); status != "" {
		return status
	}
	enabled, ok := fields.Flag(data["isEnabled"])
	switch {
	case !ok:
		return ""
	case enabled:
		return "enabled"
	}
	return "disabled"
}

// RuleFrequency is schedule.frequency (ISO 8601), else the legacy period as one, else
// empty.
func RuleFrequency(data fields.Object) string {
	schedule := fields.Map(data["schedule"])
	if frequency := fields.Text(schedule, "frequency"); frequency != "" {
		return frequency
	}
	return legacyPeriods[fields.Text(schedule, "period")]
}

// mitre is the tactics, and every technique and sub-technique under them; the legacy
// category and mitreTechniques when a rule has no tactics.
func mitre(template fields.Object) ([]string, []string) {
	var tactics, techniques []string
	for _, tactic := range fields.Objects(template["tactics"]) {
		tactics = appendText(tactics, fields.Text(tactic, "tactic"))
		for _, technique := range fields.Objects(tactic["techniques"]) {
			techniques = appendText(techniques, fields.Text(technique, "technique"))
			for _, sub := range fields.Items(technique["subTechniques"]) {
				techniques = appendText(techniques, fields.String(sub))
			}
		}
	}
	if len(fields.Items(template["tactics"])) == 0 && fields.Text(template, "category") != "" {
		tactics = appendText(tactics, fields.Text(template, "category"))
		for _, item := range fields.Items(template["mitreTechniques"]) {
			techniques = appendText(techniques, fields.String(item))
		}
	}
	return tactics, techniques
}

func appendText(values []string, value string) []string {
	if value == "" {
		return values
	}
	return append(values, value)
}
