package devices

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/poll"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// Checking a list of devices against expectations, once (fast) or until done (watch).
//
// Speed comes from three things. Lookups for different devices run in parallel. Each
// expected group's members are fetched once per pass rather than once per device. And a
// watch rechecks only the devices not yet complete, unless asked to recheck them all.
//
// A rejected token or a missing permission (HTTP 401 or 403) ends the check, since every
// device would fail the same way. Anything else that fails for one device is recorded as
// an error for that device, and a watch simply tries again next pass.

// Checker checks devices against Expectations with whichever clients they need.
type Checker struct {
	Entra   *entra.Client
	XDR     *xdr.Client
	Intune  *intune.Client
	Workers int
	Now     func() time.Time
}

func fatal(err error) bool {
	found := errs.As(err)
	return found == nil || found.Status == 401 || found.Status == 403 || found.Kind != errs.API
}

// Check is one pass over names, in order.
func (c *Checker) Check(ctx context.Context, names []string, expectations Expectations) (Run, error) {
	if err := c.require(expectations); err != nil {
		return Run{}, err
	}
	if err := c.prepare(ctx, expectations); err != nil {
		return Run{}, err
	}
	members, err := c.groupMembers(ctx, expectations.Groups)
	if err != nil {
		return Run{}, err
	}
	if len(names) == 0 {
		return Run{CheckedAt: c.now(), Expected: 0}, nil
	}
	reports, err := c.checkAll(ctx, names, expectations, members)
	if errs.Is(err, errs.Reauth) {
		// A sign-in lapsed mid-pass, in a worker. Ask here, on the calling goroutine (this
		// fails again if nobody can sign in), then run the pass again.
		if err := c.prepare(ctx, expectations); err != nil {
			return Run{}, err
		}
		reports, err = c.checkAll(ctx, names, expectations, members)
	}
	if err != nil {
		return Run{}, err
	}
	return Run{Reports: reports, CheckedAt: c.now(), Expected: len(names)}, nil
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// prepare gets each service's token on this goroutine before the workers need it: a token
// is normally cached already, so this is cheap; when a sign-in has lapsed, it is where a
// credential that can ask someone to sign in again gets to ask.
func (c *Checker) prepare(ctx context.Context, expectations Expectations) error {
	if expectations.NeedsEntra() && c.Entra != nil {
		if err := c.Entra.API.EnsureToken(ctx); err != nil {
			return err
		}
	}
	if expectations.NeedsDefender() && c.XDR != nil {
		if err := c.XDR.API.EnsureToken(ctx); err != nil {
			return err
		}
	}
	if expectations.NeedsIntune() && c.Intune != nil {
		return c.Intune.API.EnsureToken(ctx)
	}
	return nil
}

func (c *Checker) require(expectations Expectations) error {
	var missing []string
	for _, need := range []struct {
		name   string
		needed bool
		has    bool
	}{{"Entra", expectations.NeedsEntra(), c.Entra != nil}, {"Defender", expectations.NeedsDefender(), c.XDR != nil},
		{"Intune", expectations.NeedsIntune(), c.Intune != nil}} {
		if need.needed && !need.has {
			missing = append(missing, need.name)
		}
	}
	if len(missing) > 0 {
		return errors.New("these checks need a client for " + strings.Join(missing, ", "))
	}
	return expectations.Check()
}

func (c *Checker) groupMembers(ctx context.Context, groups []string) (map[string]map[string]bool, error) {
	members := map[string]map[string]bool{}
	if c.Entra == nil {
		return members, nil
	}
	for _, ref := range groups {
		group, err := c.Entra.GetGroup(ctx, ref)
		if err != nil {
			return nil, err
		}
		devices, err := c.Entra.GroupDevices(ctx, group.ID, true)
		if err != nil {
			return nil, err
		}
		members[ref] = map[string]bool{}
		for _, device := range devices {
			members[ref][device.ID] = true
		}
	}
	return members, nil
}

func (c *Checker) checkAll(ctx context.Context, names []string, expectations Expectations, members map[string]map[string]bool) ([]Report, error) {
	reports := make([]Report, len(names))
	failures := make([]error, len(names))
	slots := make(chan struct{}, max(min(c.Workers, len(names)), 1))
	var group sync.WaitGroup
	for index, name := range names {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; group.Done() }()
			reports[index], failures[index] = c.checkOne(ctx, name, expectations, members)
		}()
	}
	group.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	return reports, nil
}

// checkOne looks name up in each service the checks need, then judges every check.
func (c *Checker) checkOne(ctx context.Context, name string, expectations Expectations, members map[string]map[string]bool) (Report, error) {
	problems := map[string]string{}
	report := Report{Name: name}
	var err error
	if expectations.NeedsEntra() {
		report.Entra, err = guarded(problems, "entra", func() ([]entra.Device, error) { return c.Entra.FindDevices(ctx, name) })
		if err != nil {
			return Report{}, err
		}
	}
	if expectations.NeedsDefender() {
		lookup, err := guarded(problems, "defender", func() (xdr.MachineLookup, error) { return c.XDR.FindMachine(ctx, name) })
		if err != nil {
			return Report{}, err
		}
		report.Defender = &lookup
	}
	if expectations.NeedsIntune() {
		report.Intune, err = guarded(problems, "intune", func() ([]intune.ManagedDevice, error) { return c.Intune.FindDevices(ctx, name) })
		if err != nil {
			return Report{}, err
		}
	}
	var machine *xdr.Machine
	if report.Defender != nil {
		machine = report.Defender.Machine()
	}
	var newest *intune.ManagedDevice
	if len(report.Intune) > 0 {
		newest = &report.Intune[0]
	}
	if expectations.InEntra {
		report.Outcomes = append(report.Outcomes, entraOutcome(report.Entra, problems["entra"]))
	}
	report.Outcomes = append(report.Outcomes, defenderOutcomes(expectations, machine, problems["defender"])...)
	report.Outcomes = append(report.Outcomes, groupOutcomes(expectations.Groups, report.Entra, members, problems["entra"])...)
	report.Outcomes = append(report.Outcomes, intuneOutcomes(expectations, newest, problems["intune"])...)
	return report, nil
}

// guarded is call's result; an API error for this one device is noted under key rather
// than returned, unless it is one every device would meet (401, 403, a lapsed sign-in).
func guarded[T any](problems map[string]string, key string, call func() (T, error)) (T, error) {
	found, err := call()
	if err == nil {
		return found, nil
	}
	var zero T
	if fatal(err) {
		return zero, err
	}
	problems[key] = err.Error()
	return zero, nil
}

// Watch checks names every interval until all are complete or a limit hits.
//
// A device that met everything is not checked again unless recheck is set, so later
// passes only cost calls for the devices still outstanding. A pass that fails outright
// (other than on a rejected token) is reported and the next pass tries again.
func Watch(ctx context.Context, checker *Checker, names []string, expectations Expectations, limits poll.Limits, recheck bool,
	clock poll.Clock, hooks poll.Hooks[Run]) (poll.Outcome[Run], error) {
	latest := map[string]Report{}
	current := func(problem string) Run {
		var reports []Report
		for _, name := range names {
			if report, ok := latest[name]; ok {
				reports = append(reports, report)
			}
		}
		return Run{Reports: reports, CheckedAt: clock.Now(), Expected: len(names), Error: problem}
	}
	pass := func(int) (Run, error) {
		pending := names
		if !recheck {
			pending = nil
			for _, name := range names {
				if report, ok := latest[name]; !ok || !report.Complete() {
					pending = append(pending, name)
				}
			}
		}
		run, err := checker.Check(ctx, pending, expectations)
		if err != nil {
			if fatal(err) {
				return Run{}, err
			}
			return current(err.Error()), nil
		}
		for _, report := range run.Reports {
			latest[report.Name] = report
		}
		return current(""), nil
	}
	return poll.Run(ctx, pass, Run.Complete, limits, clock, hooks)
}

func entraOutcome(devices []entra.Device, problem string) Outcome {
	switch {
	case problem != "":
		return Outcome{"entra", "error", problem}
	case len(devices) == 0:
		return Outcome{"entra", "unmet", "not in Entra"}
	}
	disabled := true
	for _, device := range devices {
		disabled = disabled && device.Enabled != nil && !*device.Enabled
	}
	if disabled {
		return Outcome{"entra", "unmet", "disabled in Entra"}
	}
	if len(devices) == 1 {
		return Outcome{"entra", "met", "present"}
	}
	return Outcome{"entra", "met", strconv.Itoa(len(devices)) + " objects share the name"}
}

// Judging the checks: every check has the same shape. The lookup failed (error), it
// found nothing (unmet, and why), or it found a record, which a judge meets or not with a
// detail. judge holds that shape once; each service lists its checks as judges.

func judge[R any](check, problem string, found *R, missing string, verdict func(R) (bool, string)) Outcome {
	switch {
	case problem != "":
		return Outcome{check, "error", problem}
	case found == nil:
		return Outcome{check, "unmet", missing}
	}
	met, detail := verdict(*found)
	if met {
		return Outcome{check, "met", detail}
	}
	return Outcome{check, "unmet", detail}
}

type machineCheck struct {
	name    string
	verdict func(xdr.Machine) (bool, string)
}

// defenderOutcomes are onboarded, active, each tag and each device group, from the
// Defender record.
func defenderOutcomes(expectations Expectations, machine *xdr.Machine, problem string) []Outcome {
	var checks []machineCheck
	if expectations.Onboarded {
		checks = append(checks, machineCheck{"defender", onboarded})
	}
	if expectations.Active {
		checks = append(checks, machineCheck{"active", active})
	}
	for _, tag := range expectations.Tags {
		checks = append(checks, machineCheck{"tag " + tag, hasTag(tag)})
	}
	for _, group := range expectations.DeviceGroups {
		checks = append(checks, machineCheck{"device group " + group, inDeviceGroup(group)})
	}
	var outcomes []Outcome
	for _, check := range checks {
		outcomes = append(outcomes, judge(check.name, problem, machine, "no Defender record", check.verdict))
	}
	return outcomes
}

func onboarded(machine xdr.Machine) (bool, string) {
	if machine.OnboardingStatus == "Onboarded" {
		return true, "onboarded, " + machine.HealthStatus
	}
	if machine.OnboardingStatus == "" {
		return false, "not onboarded"
	}
	return false, machine.OnboardingStatus
}

func active(machine xdr.Machine) (bool, string) {
	if machine.HealthStatus == "Active" {
		return true, "Active"
	}
	if machine.HealthStatus == "" {
		return false, "unknown"
	}
	return false, machine.HealthStatus
}

func hasTag(tag string) func(xdr.Machine) (bool, string) {
	return func(machine xdr.Machine) (bool, string) {
		if slices.ContainsFunc(machine.MachineTags, func(item string) bool { return strings.EqualFold(item, tag) }) {
			return true, "tagged"
		}
		return false, "tag missing"
	}
}

func inDeviceGroup(group string) func(xdr.Machine) (bool, string) {
	return func(machine xdr.Machine) (bool, string) {
		if strings.EqualFold(machine.DeviceGroup, group) {
			return true, "in the device group"
		}
		if machine.DeviceGroup == "" {
			return false, "in none"
		}
		return false, "in " + machine.DeviceGroup
	}
}

// groupOutcomes are membership of each Entra group, by any of the Entra objects that
// share the name.
func groupOutcomes(groups []string, devices []entra.Device, members map[string]map[string]bool, problem string) []Outcome {
	var found *[]entra.Device
	if len(devices) > 0 {
		found = &devices
	}
	var outcomes []Outcome
	for _, group := range groups {
		outcomes = append(outcomes, judge("group "+group, problem, found, "not in Entra", func(devices []entra.Device) (bool, string) {
			for _, device := range devices {
				if members[group][device.ID] {
					return true, "member"
				}
			}
			return false, "not a member"
		}))
	}
	return outcomes
}

// intuneOutcomes are enrolled and compliant, from the newest Intune record.
func intuneOutcomes(expectations Expectations, newest *intune.ManagedDevice, problem string) []Outcome {
	var outcomes []Outcome
	if expectations.InIntune {
		outcomes = append(outcomes, judge("intune", problem, newest, "not enrolled in Intune", func(device intune.ManagedDevice) (bool, string) {
			if device.ManagementAgent == "" {
				return true, "enrolled"
			}
			return true, device.ManagementAgent
		}))
	}
	if expectations.Compliant {
		outcomes = append(outcomes, judge("compliant", problem, newest, "not enrolled in Intune", func(device intune.ManagedDevice) (bool, string) {
			if device.Compliant() {
				return true, "compliant"
			}
			if device.ComplianceState == "" {
				return false, "unknown"
			}
			return false, device.ComplianceState
		}))
	}
	return outcomes
}
