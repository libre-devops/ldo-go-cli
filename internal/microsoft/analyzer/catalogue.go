package analyzer

// The Linux and macOS analyzer records a finding as an id alone: its platform (3 Linux, 2
// macOS), then five digits, the second of which is the severity (0 good, 1 warning, 2
// error). Its catalogue of what each means ships beside the tool, not in its results, so
// the checks it has are described here, and one this does not know is still shown, by id.

// Platforms are the platform digit of a finding id.
var Platforms = map[string]string{"1": "windows", "2": "macos", "3": "linux"}

var severityDigits = map[string]string{"0": "informational", "1": "warning", "2": "error"}

const (
	edrCNC     = "EDR cloud (command and control)"
	edrCyber   = "EDR cloud (cyber data)"
	avCloud    = "Antivirus cloud"
	allPassed  = "every test connection succeeded"
	someFailed = "some test connections failed"
	allFailed  = "the test connections failed"
)

// Checks are (category, check, result), by the five digits after the platform.
var Checks = map[string][3]string{
	"30002": {"Connectivity", edrCNC, allPassed},
	"31001": {"Connectivity", edrCNC, someFailed},
	"32003": {"Connectivity", edrCNC, allFailed},
	"30005": {"Connectivity", edrCyber, allPassed},
	"31004": {"Connectivity", edrCyber, someFailed},
	"32006": {"Connectivity", edrCyber, allFailed},
	"30008": {"Connectivity", avCloud, allPassed},
	"31007": {"Connectivity", avCloud, someFailed},
	"32009": {"Connectivity", avCloud, allFailed},
	"12001": {"Environment", "Operating system", "not supported"},
	"10038": {"Environment", "Operating system", "supported in preview"},
	"10002": {"Processes", "Defender processes", "running"},
	"12002": {"Processes", "Defender processes", "not running"},
	"11010": {"Environment", "Conflicting binaries", "other software with audit rules found"},
	"21035": {"Anti-spoofing", "Anti-spoofing", "ready, not yet stable"},
	"21036": {"Anti-spoofing", "Anti-spoofing", "unstable"},
	"20037": {"Anti-spoofing", "Anti-spoofing", "stable"},
}

// Guidance is what to read when a category's check is not good, by platform and category.
var Guidance = map[[2]string]string{
	{"linux", "Connectivity"}: "Allow the Defender for Endpoint addresses through the proxy and firewall: " +
		"https://learn.microsoft.com/defender-endpoint/linux-support-connectivity",
	{"linux", "Environment"}: "Check the system requirements: " +
		"https://learn.microsoft.com/defender-endpoint/microsoft-defender-endpoint-linux",
}

// Describe is (platform, severity, category, check, result) for a Linux or macOS finding
// id; one this does not know keeps its id as its check.
func Describe(id string) (platform, severity, category, check, result string) {
	if id != "" {
		platform = Platforms[id[:1]]
	}
	code := ""
	if len(id) > 1 {
		code = id[1:]
	}
	severity = "informational"
	if len(code) > 1 {
		if found, ok := severityDigits[code[1:2]]; ok {
			severity = found
		}
	}
	known, ok := Checks[code]
	if !ok {
		return platform, severity, "Other", "check " + id, ""
	}
	return platform, severity, known[0], known[1], known[2]
}
