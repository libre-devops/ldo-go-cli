package xdr

import (
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
)

// Machine is one Defender for Endpoint machine record. Raw keeps the full record for
// JSON output.
type Machine struct {
	ID               string
	ComputerDNSName  string
	OnboardingStatus string
	HealthStatus     string
	LastSeen         time.Time
	FirstSeen        time.Time
	OSPlatform       string
	OSVersion        string
	AgentVersion     string
	MachineTags      []string
	RiskScore        string
	ExposureLevel    string
	LastIPAddress    string
	AADDeviceID      string
	// DeviceGroup is the Defender device group (Settings > Endpoints > Device groups) the
	// machine falls in; "UnassignedGroup" when it matches none.
	DeviceGroup string
	Raw         fields.Object
}

// MachineFrom is a machine as Defender for Endpoint returns it.
func MachineFrom(data fields.Object) Machine {
	var tags []string
	for _, tag := range fields.Items(data["machineTags"]) {
		tags = append(tags, fields.String(tag))
	}
	return Machine{
		ID: fields.Text(data, "id"), ComputerDNSName: fields.Text(data, "computerDnsName"),
		OnboardingStatus: fields.Text(data, "onboardingStatus"), HealthStatus: fields.Text(data, "healthStatus"),
		LastSeen: fields.When(data, "lastSeen"), FirstSeen: fields.When(data, "firstSeen"),
		OSPlatform: fields.Text(data, "osPlatform"), OSVersion: fields.Text(data, "osVersion"),
		AgentVersion: fields.Text(data, "version"), MachineTags: tags, RiskScore: fields.Text(data, "riskScore"),
		ExposureLevel: fields.Text(data, "exposureLevel"), LastIPAddress: fields.Text(data, "lastIpAddress"),
		AADDeviceID: fields.Text(data, "aadDeviceId"), DeviceGroup: fields.Text(data, "rbacGroupName"), Raw: data,
	}
}

// MachineLookup is one device looked up by name. MatchedName is the name that found
// records (the FQDN, or the short hostname fallback), or "". Records are newest lastSeen
// first; more than one means Defender holds duplicate records for the device.
type MachineLookup struct {
	Query       string
	MatchedName string
	Records     []Machine
}

// Found reports whether Defender has any record with the name.
func (l MachineLookup) Found() bool { return len(l.Records) > 0 }

// Machine is the newest record, which is the one to trust, or nil.
func (l MachineLookup) Machine() *Machine {
	if len(l.Records) == 0 {
		return nil
	}
	return &l.Records[0]
}

// Alert is a Defender for Endpoint alert.
type Alert struct {
	ID              string
	Title           string
	Severity        string
	Status          string
	Category        string
	DetectionSource string
	MachineID       string
	ComputerDNSName string
	IncidentID      string
	Created         time.Time
	LastActivity    time.Time
	Raw             fields.Object
}

// Resolved reports whether the alert is resolved.
func (a Alert) Resolved() bool { return strings.EqualFold(a.Status, "resolved") }

// AlertFrom is an alert as Defender for Endpoint returns it.
func AlertFrom(data fields.Object) Alert {
	last := fields.When(data, "lastEventTime")
	if last.IsZero() {
		last = fields.When(data, "lastUpdateTime")
	}
	return Alert{
		ID: fields.Text(data, "id"), Title: fields.Text(data, "title"), Severity: fields.Text(data, "severity"),
		Status: fields.Text(data, "status"), Category: fields.Text(data, "category"),
		DetectionSource: fields.Text(data, "detectionSource"), MachineID: fields.Text(data, "machineId"),
		ComputerDNSName: fields.Text(data, "computerDnsName"), IncidentID: fields.Text(data, "incidentId"),
		Created: fields.When(data, "alertCreationTime"), LastActivity: last, Raw: data,
	}
}

// Vulnerability is a vulnerability (CVE) Defender reports on a machine. CVSS is its CVSS
// v3 score, when it has one.
type Vulnerability struct {
	ID              string
	Name            string
	Severity        string
	CVSS            *float64
	ExploitVerified bool
	PublicExploit   bool
	Published       time.Time
	Raw             fields.Object
}

// VulnerabilityFrom is a vulnerability as Defender for Endpoint returns it.
func VulnerabilityFrom(data fields.Object) Vulnerability {
	var cvss *float64
	if score, ok := data["cvssV3"].(float64); ok {
		cvss = &score
	}
	return Vulnerability{
		ID: fields.Text(data, "id"), Name: fields.Text(data, "name"), Severity: fields.Text(data, "severity"), CVSS: cvss,
		ExploitVerified: data["exploitVerified"] == true, PublicExploit: data["publicExploit"] == true,
		Published: fields.When(data, "publishedOn"), Raw: data,
	}
}

// Indicator is a custom indicator of compromise (file hash, IP, URL, domain or
// certificate).
type Indicator struct {
	ID            string
	Value         string
	IndicatorType string
	Action        string
	Title         string
	Severity      string
	Expires       time.Time
	CreatedBy     string
	Raw           fields.Object
}

// IndicatorFrom is a custom indicator as Defender for Endpoint returns it.
func IndicatorFrom(data fields.Object) Indicator {
	return Indicator{
		ID: fields.Text(data, "id"), Value: fields.Text(data, "indicatorValue"), IndicatorType: fields.Text(data, "indicatorType"),
		Action: fields.Text(data, "action"), Title: fields.Text(data, "title"), Severity: fields.Text(data, "severity"),
		Expires: fields.When(data, "expirationTime"), CreatedBy: fields.Text(data, "createdBy"), Raw: data,
	}
}
