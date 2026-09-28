// Package devices is devices across Entra, Defender and Intune: checks, watches, a
// combined view, and antivirus versions. It is the one composite package: it uses the
// entra, xdr and intune feature packages, and nothing else of the vendor's.
package devices

import (
	"fmt"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// Expectations are the state every device should be in. Each enabled item is one check
// per device.
type Expectations struct {
	InEntra      bool
	Onboarded    bool
	Active       bool
	Tags         []string
	DeviceGroups []string
	Groups       []string
	InIntune     bool
	Compliant    bool
}

// Check is an Input error when there is nothing to check.
func (e Expectations) Check() error {
	if len(e.Checks()) == 0 {
		return errs.Inputf("nothing to check").WithHint("enable at least one of Entra, Defender, tags, device groups, groups")
	}
	return nil
}

// NeedsEntra reports whether any check needs an Entra lookup: presence, or groups.
func (e Expectations) NeedsEntra() bool { return e.InEntra || len(e.Groups) > 0 }

// NeedsDefender reports whether any check needs a Defender lookup.
func (e Expectations) NeedsDefender() bool {
	return e.Onboarded || e.Active || len(e.Tags) > 0 || len(e.DeviceGroups) > 0
}

// NeedsIntune reports whether any check needs an Intune lookup: enrolment or compliance.
func (e Expectations) NeedsIntune() bool { return e.InIntune || e.Compliant }

// Checks are the check names, in the order they are reported.
func (e Expectations) Checks() []string {
	var names []string
	if e.InEntra {
		names = append(names, "entra")
	}
	if e.Onboarded {
		names = append(names, "defender")
	}
	if e.Active {
		names = append(names, "active")
	}
	for _, tag := range e.Tags {
		names = append(names, "tag "+tag)
	}
	for _, group := range e.DeviceGroups {
		names = append(names, "device group "+group)
	}
	for _, group := range e.Groups {
		names = append(names, "group "+group)
	}
	if e.InIntune {
		names = append(names, "intune")
	}
	if e.Compliant {
		names = append(names, "compliant")
	}
	return names
}

// Outcome is one check on one device: met, unmet or error.
type Outcome struct {
	Check  string
	Status string
	Detail string
}

// Report is everything one check pass found for one device name.
type Report struct {
	Name     string
	Outcomes []Outcome
	Entra    []entra.Device
	Defender *xdr.MachineLookup
	Intune   []intune.ManagedDevice
}

// Complete reports whether the device met every check.
func (r Report) Complete() bool {
	for _, outcome := range r.Outcomes {
		if outcome.Status != "met" {
			return false
		}
	}
	return true
}

// Outcome is the outcome of the check named check, or false when it was not asked for.
func (r Report) Outcome(check string) (Outcome, bool) {
	for _, outcome := range r.Outcomes {
		if outcome.Check == check {
			return outcome, true
		}
	}
	return Outcome{}, false
}

// Run is one pass over every device. Error is set when the pass itself failed.
type Run struct {
	Reports   []Report
	CheckedAt time.Time
	Expected  int
	Error     string
}

// Complete reports whether the pass ran, reported on every device asked for, and each
// met every check.
func (r Run) Complete() bool {
	if r.Error != "" || len(r.Reports) != r.Expected {
		return false
	}
	for _, report := range r.Reports {
		if !report.Complete() {
			return false
		}
	}
	return true
}

// Counts is how many devices meet each check.
func (r Run) Counts(checks []string) []Count {
	counts := make([]Count, len(checks))
	for index, check := range checks {
		counts[index] = Count{Check: check}
		for _, report := range r.Reports {
			if outcome, ok := report.Outcome(check); ok && outcome.Status == "met" {
				counts[index].Met++
			}
		}
	}
	return counts
}

// Count is how many devices meet one check.
type Count struct {
	Check string
	Met   int
}

// Finding is one observation about a device from devices show: ok, info or warn.
type Finding struct {
	Level   string
	Message string
}

// View is a device across Entra, Defender and Intune, with what looks wrong about it.
// Defender and Intune are nil when that service was not asked, or could not be read (a
// finding says which).
type View struct {
	Name     string
	Entra    []entra.Device
	Groups   map[string][]entra.Group
	Defender *xdr.MachineLookup
	Intune   *[]intune.ManagedDevice
	Findings []Finding
}

func warnf(format string, args ...any) Finding { return Finding{"warn", fmt.Sprintf(format, args...)} }
