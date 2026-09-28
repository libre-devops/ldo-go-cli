package devices

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// Defender Antivirus versions on devices: the signature, engine and platform, by KQL.
//
// The versions come from Advanced Hunting, as you would look them up by hand:
// DeviceTvmInfoGathering holds what each device last reported, with the signature, engine
// and platform versions and the antivirus mode in its AdditionalFields;
// DeviceTvmSecureConfigurationAssessment holds the "antivirus definitions up to date"
// check (scid-2011), which says whether that signature is current. The query is built
// here; running it is the caller's, so either Advanced Hunting API will do.

// UpToDateCheck is the definitions check's configuration id.
const UpToDateCheck = "scid-2011"

// AVModes are AdditionalFields.AvMode's numbers Defender documents.
var AVModes = map[string]string{"0": "active", "1": "passive", "4": "EDR block"}

const avQuery = `let wanted = dynamic({names});
let versions = DeviceTvmInfoGathering
| where DeviceName in~ (wanted) or tostring(split(DeviceName, ".")[0]) in~ (wanted)
| summarize arg_max(Timestamp, DeviceName, OSPlatform, AdditionalFields) by DeviceId
| extend Fields = todynamic(AdditionalFields)
| project DeviceId, DeviceName, OSPlatform, Reported = Timestamp,
    AvSignatureVersion = tostring(Fields.AvSignatureVersion),
    AvEngineVersion = tostring(Fields.AvEngineVersion),
    AvPlatformVersion = tostring(Fields.AvPlatformVersion),
    AvMode = tostring(Fields.AvMode);
let freshness = DeviceTvmSecureConfigurationAssessment
| where ConfigurationId == "{check}"
| where DeviceName in~ (wanted) or tostring(split(DeviceName, ".")[0]) in~ (wanted)
| summarize arg_max(Timestamp, IsCompliant, IsApplicable, Context) by DeviceId
| project DeviceId, SignatureUpToDate = iff(IsApplicable, IsCompliant, bool(null)),
    AssessmentContext = tostring(Context);
versions
| join kind=leftouter freshness on DeviceId
| project-away DeviceId1
| order by DeviceName asc`

var digits = regexp.MustCompile(`\d+`)

// AVStatus is one device's antivirus versions, as Advanced Hunting last saw them. Found is
// false for a name Advanced Hunting had nothing for; UpToDate is nil when the definitions
// check does not apply or has not run.
type AVStatus struct {
	Query      string
	Found      bool
	DeviceID   string
	DeviceName string
	OSPlatform string
	Signature  string
	Engine     string
	Platform   string
	Mode       string
	UpToDate   *bool
	Reported   time.Time
	Raw        fields.Object
}

// OlderThan reports whether the signature is below minimum (or unknown).
func (s AVStatus) OlderThan(minimum string) bool {
	if s.Signature == "" {
		return true
	}
	have, err := VersionKey(s.Signature)
	if err != nil {
		return true
	}
	want, _ := VersionKey(minimum)
	return compareVersions(have, want) < 0
}

// AVQuery is the KQL for names: each device's FQDN and short name are both looked for.
func AVQuery(names []string) (string, error) {
	var wanted []string
	seen := map[string]bool{}
	for _, name := range names {
		host, err := util.RequireHost(name)
		if err != nil {
			return "", err
		}
		for _, candidate := range util.CandidateNames(host) {
			if lowered := strings.ToLower(candidate); !seen[lowered] {
				seen[lowered] = true
				wanted = append(wanted, lowered)
			}
		}
	}
	if len(wanted) == 0 {
		return "", errs.Inputf("no devices named")
	}
	encoded, _ := json.Marshal(wanted)
	list := strings.ReplaceAll(string(encoded), `","`, `", "`)
	return strings.ReplaceAll(strings.ReplaceAll(avQuery, "{names}", list), "{check}", UpToDateCheck), nil
}

// AVStatuses is one status per name asked for, in order, matched on the FQDN or short
// name. A name that matches several devices (stale records keep a name) gives one status
// for each, newest report first.
func AVStatuses(names []string, result render.QueryResult) []AVStatus {
	var statuses []AVStatus
	for _, name := range names {
		full := strings.ToLower(strings.TrimRight(strings.TrimSpace(name), "."))
		short := strings.ToLower(util.ShortName(full))
		var matched []fields.Object
		for _, row := range result.Rows {
			device := strings.ToLower(fields.Text(row, "DeviceName"))
			if device == full || device == short || strings.ToLower(util.ShortName(device)) == short {
				matched = append(matched, row)
			}
		}
		if len(matched) == 0 {
			statuses = append(statuses, AVStatus{Query: name})
			continue
		}
		sort.SliceStable(matched, func(a, b int) bool { return fields.Text(matched[a], "Reported") > fields.Text(matched[b], "Reported") })
		for _, row := range matched {
			statuses = append(statuses, avStatus(name, row))
		}
	}
	return statuses
}

// VersionKey is 1.419.123.0 as (1, 419, 123, 0), for comparing versions numerically.
func VersionKey(version string) ([]int, error) {
	parts := digits.FindAllString(version, -1)
	if len(parts) == 0 {
		return nil, errs.Inputf("'%s' is not a version number", version)
	}
	key := make([]int, len(parts))
	for index, part := range parts {
		key[index], _ = strconv.Atoi(part)
	}
	return key, nil
}

func compareVersions(a, b []int) int {
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			if a[index] < b[index] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// ModeLabel is 0 as active; an unknown mode is kept as it came.
func ModeLabel(value any) string {
	text := ""
	if value != nil {
		text = strings.TrimSpace(fields.String(value))
	}
	if label, ok := AVModes[text]; ok {
		return label
	}
	return text
}

func avStatus(query string, row fields.Object) AVStatus {
	var upToDate *bool
	if flag, ok := row["SignatureUpToDate"].(bool); ok {
		upToDate = &flag
	}
	return AVStatus{
		Query: query, Found: true, DeviceID: fields.Text(row, "DeviceId"), DeviceName: fields.Text(row, "DeviceName"),
		OSPlatform: fields.Text(row, "OSPlatform"), Signature: fields.Text(row, "AvSignatureVersion"),
		Engine: fields.Text(row, "AvEngineVersion"), Platform: fields.Text(row, "AvPlatformVersion"), Mode: ModeLabel(row["AvMode"]),
		UpToDate: upToDate, Reported: fields.When(row, "Reported"), Raw: row,
	}
}
