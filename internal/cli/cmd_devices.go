package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/poll"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/devices"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/graph"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// devicesCommand is the devices group; device finds it too.
func devicesCommand(rt *Runtime) *cobra.Command {
	group := newGroup("devices", "Devices across Entra, Defender and Intune: check, watch, show, and AV versions.")
	group.Aliases = []string{"device"}
	group.AddCommand(devicesCheck(rt), devicesWatch(rt), devicesShow(rt), devicesAVSignature(rt))
	return group
}

var outcomeColours = map[string]string{"met": "green", "unmet": "yellow", "error": "red"}

// expectationFlags are what a check expects of every device.
type expectationFlags struct {
	entra, defender, active, intune, compliant bool
	noEntra, noDefender                        bool
	tags, deviceGroups, groups                 []string
	workers                                    int
}

func (f *expectationFlags) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.BoolVar(&f.entra, "entra", true, "Expect the device in Entra ID.")
	flags.BoolVar(&f.noEntra, "no-entra", false, "Do not expect the device in Entra ID.")
	flags.BoolVar(&f.defender, "defender", true, "Expect it onboarded to Defender.")
	flags.BoolVar(&f.noDefender, "no-defender", false, "Do not expect it onboarded to Defender.")
	flags.BoolVar(&f.active, "active", false, "Expect Defender health to be Active.")
	flags.StringArrayVar(&f.tags, "tag", nil, "Expect this Defender machine tag. Repeatable.")
	flags.StringArrayVar(&f.deviceGroups, "device-group", nil, "Expect the device in this Defender device group (its name). Repeatable.")
	flags.StringArrayVar(&f.groups, "group", nil, "Expect membership of this Entra group: its object id or name. Repeatable.")
	flags.BoolVar(&f.intune, "intune", false, "Expect it enrolled in Intune.")
	flags.BoolVar(&f.compliant, "compliant", false, "Expect Intune to report it compliant.")
	flags.IntVar(&f.workers, "workers", 8, "Devices looked up at once.")
}

func (f *expectationFlags) expectations(cmd *cobra.Command) (devices.Expectations, error) {
	if cmd.Flags().Changed("workers") && (f.workers < 1 || f.workers > 32) {
		return devices.Expectations{}, Usagef("--workers", "%d is not in the range 1<=x<=32", f.workers)
	}
	expected := devices.Expectations{InEntra: f.entra && !f.noEntra, Onboarded: f.defender && !f.noDefender, Active: f.active,
		Tags: f.tags, DeviceGroups: f.deviceGroups, Groups: f.groups, InIntune: f.intune, Compliant: f.compliant}
	return expected, expected.Check()
}

// deviceChecker builds only the clients the expectations need, so an Entra-only check
// never asks Defender or Intune for a token.
func deviceChecker(rt *Runtime, profile microsoft.Profile, expected devices.Expectations, workers int) (*devices.Checker, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	checker := &devices.Checker{Workers: workers, Now: rt.Clock}
	if expected.NeedsEntra() {
		if checker.Entra, err = entra.New(api); err != nil {
			return nil, err
		}
	}
	if expected.NeedsDefender() {
		if checker.XDR, err = xdr.New(api, profile.MDEURL); err != nil {
			return nil, err
		}
	}
	if expected.NeedsIntune() {
		if checker.Intune, err = intune.New(api); err != nil {
			return nil, err
		}
	}
	return checker, nil
}

func devicesCheck(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	flags := &expectationFlags{}
	command := &cobra.Command{
		Use:   "check [NAMES]...",
		Short: "Check a list of devices once, fast, against what you expect of them.",
		Long: "Check a list of devices once, fast, against what you expect of them.\n\n" +
			"By default each device must be in Entra and onboarded to Defender. Exits 3 when any device misses any expectation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			expected, err := flags.expectations(cmd)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			checker, err := deviceChecker(rt, profile, expected, flags.workers)
			if err != nil {
				return err
			}
			run, err := checker.Check(rt.Ctx(), wanted, expected)
			if err != nil {
				return err
			}
			if err := renderRun(rt, output, run, expected); err != nil {
				return err
			}
			rt.Console.Note("%s (profile %s)", runSummary(run, expected), profile.Name)
			if !run.Complete() {
				return Attention
			}
			return nil
		},
	}
	names.Add(command)
	flags.add(command)
	common.AddOutput(command, true)
	return command
}

// watchEnds are why a watch stopped, by the poll's reason.
var watchEnds = map[poll.Reason]string{
	poll.Complete:  "every device meets every expectation",
	poll.Timeout:   "timed out before every device was complete",
	poll.MaxPasses: "reached --max-passes before every device was complete",
}

func devicesWatch(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	flags := &expectationFlags{}
	var interval, timeout string
	var maxPasses int
	var recheck bool
	command := &cobra.Command{
		Use:   "watch [NAMES]...",
		Short: "Check devices repeatedly until every one meets every expectation, or a limit hits.",
		Long: "Check devices repeatedly until every one meets every expectation, or a limit hits.\n\n" +
			"Progress goes to stderr after each pass; the final state goes to stdout. Exits 0 when complete, 3 when " +
			"a limit stopped it first, and 130 on Ctrl-C.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positive(cmd, "max-passes", maxPasses); err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			expected, err := flags.expectations(cmd)
			if err != nil {
				return err
			}
			limits, described, err := watchLimits(interval, timeout, maxPasses)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			checker, err := deviceChecker(rt, profile, expected, flags.workers)
			if err != nil {
				return err
			}
			rt.Console.Note("watching %d device(s) %s; Ctrl-C to stop", len(wanted), described)
			return runWatch(rt, output, checker, wanted, expected, limits, recheck)
		},
	}
	names.Add(command)
	command.Flags().StringVar(&interval, "interval", "5m", "Time between passes, e.g. 90s, 5m, 1h.")
	command.Flags().StringVar(&timeout, "timeout", "", "Give up after this long, e.g. 2h. Default: never.")
	command.Flags().IntVar(&maxPasses, "max-passes", 0, "Give up after this many passes.")
	command.Flags().BoolVar(&recheck, "recheck", false, "Recheck devices that already met everything, each pass.")
	flags.add(command)
	common.AddOutput(command, true)
	return command
}

func runWatch(rt *Runtime, output render.Output, checker *devices.Checker, wanted []string, expected devices.Expectations,
	limits poll.Limits, recheck bool) error {
	var last *devices.Run // the latest pass, shown if Ctrl-C stops the watch
	hooks := poll.Hooks[devices.Run]{
		OnPass: func(number int, run devices.Run) {
			last = &run
			if run.Error != "" {
				rt.Console.Warn("pass %d: failed, will retry: %s", number, run.Error)
			} else {
				rt.Console.Note("pass %d: %s", number, runSummary(run, expected))
			}
		},
		OnWait: func(wait time.Duration) { rt.Console.Note("next pass in %s", util.FormatDuration(wait)) },
	}
	outcome, err := devices.Watch(rt.Ctx(), checker, wanted, expected, limits, recheck, rt.PollClock(), hooks)
	if errors.Is(err, context.Canceled) {
		if last != nil {
			_ = renderRun(rt, output, *last, expected)
		}
		rt.Console.Note("stopped")
		return &ExitStatus{Code: ExitInterrupted}
	}
	if err != nil {
		return err
	}
	if err := renderRun(rt, output, outcome.Result, expected); err != nil {
		return err
	}
	rt.Console.Note("%s after %d pass(es), %s", watchEnds[outcome.Reason], outcome.Passes, util.FormatDuration(outcome.Elapsed))
	if !outcome.Complete() {
		return Attention
	}
	return nil
}

// watchLimits is a watch's limits, and how to say them: every 5m, up to 2h, at most 3
// pass(es).
func watchLimits(interval, timeout string, maxPasses int) (poll.Limits, string, error) {
	every, err := Duration("--interval", interval)
	if err != nil {
		return poll.Limits{}, "", err
	}
	limits := poll.Limits{Interval: every, MaxPasses: maxPasses}
	described := "every " + util.FormatDuration(every)
	if timeout != "" {
		if limits.Timeout, err = Duration("--timeout", timeout); err != nil {
			return poll.Limits{}, "", err
		}
		described += ", up to " + util.FormatDuration(limits.Timeout)
	}
	if maxPasses > 0 {
		described += fmt.Sprintf(", at most %d pass(es)", maxPasses)
	}
	return limits, described, limits.Check()
}

func renderRun(rt *Runtime, output render.Output, run devices.Run, expected devices.Expectations) error {
	checks := expected.Checks()
	headers := []string{"DEVICE", "MET"}
	for _, check := range checks {
		headers = append(headers, strings.ToUpper(check))
	}
	rows := make([][]render.Cell, len(run.Reports))
	records := []any{}
	for index, report := range run.Reports {
		// How many checks it meets, so --sort met:desc puts the complete ones first.
		met := 0
		for _, outcome := range report.Outcomes {
			if outcome.Status == "met" {
				met++
			}
		}
		colour := "yellow"
		if report.Complete() {
			colour = "green"
		}
		row := []render.Cell{render.Plain(report.Name), render.Coloured(fmt.Sprintf("%d/%d", met, len(report.Outcomes)), colour)}
		for _, check := range checks {
			outcome, ok := report.Outcome(check)
			switch {
			case !ok:
				row = append(row, render.Plain(""))
			case outcome.Status == "met":
				row = append(row, render.Coloured("ok", "green"))
			default:
				row = append(row, render.Coloured(outcome.Detail, outcomeColours[outcome.Status]))
			}
		}
		rows[index] = row
		records = append(records, reportRecord(report))
	}
	var problem any
	if run.Error != "" {
		problem = run.Error
	}
	return rt.Console.Emit(output, headers, rows, map[string]any{"complete": run.Complete(), "checked_at": render.ISO(run.CheckedAt),
		"error": problem, "devices": records})
}

func reportRecord(report devices.Report) map[string]any {
	checks := map[string]any{}
	for _, outcome := range report.Outcomes {
		checks[outcome.Check] = map[string]any{"status": outcome.Status, "detail": outcome.Detail}
	}
	entraRecords, defenderRecords, intuneRecords := []any{}, []any{}, []any{}
	for _, device := range report.Entra {
		entraRecords = append(entraRecords, device.Raw)
	}
	if report.Defender != nil {
		for _, record := range report.Defender.Records {
			defenderRecords = append(defenderRecords, record.Raw)
		}
	}
	for _, device := range report.Intune {
		intuneRecords = append(intuneRecords, device.Raw)
	}
	return map[string]any{"name": report.Name, "complete": report.Complete(), "checks": checks, "entra": entraRecords,
		"defender": defenderRecords, "intune": intuneRecords}
}

func runSummary(run devices.Run, expected devices.Expectations) string {
	done := 0
	for _, report := range run.Reports {
		if report.Complete() {
			done++
		}
	}
	var parts []string
	for _, count := range run.Counts(expected.Checks()) {
		parts = append(parts, fmt.Sprintf("%s %d/%d", count.Check, count.Met, run.Expected))
	}
	return fmt.Sprintf("%d/%d complete (%s)", done, run.Expected, strings.Join(parts, ", "))
}

var findingColours = map[string]string{"ok": "green", "warn": "yellow"}

func devicesShow(rt *Runtime) *cobra.Command {
	common := &Common{}
	var withIntune, noDefender bool
	withDefender := true
	var staleAfter string
	command := &cobra.Command{
		Use:   "show DEVICE",
		Short: "Show one device across Entra, Defender and Intune, and what looks wrong about it.",
		Long: "Show one device across Entra, Defender and Intune, and what looks wrong about it.\n\n" +
			"DEVICE is its FQDN or short hostname. Exits 3 when there is any warning.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			stale, err := Duration("--stale-after", staleAfter)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			api, err := rt.API(profile)
			if err != nil {
				return err
			}
			entraClient, err := entra.New(api)
			if err != nil {
				return err
			}
			var xdrClient *xdr.Client
			if withDefender && !noDefender {
				if xdrClient, err = xdr.New(api, profile.MDEURL); err != nil {
					return err
				}
			}
			var intuneClient *intune.Client
			if withIntune {
				if intuneClient, err = intune.New(api); err != nil {
					return err
				}
			}
			view, err := devices.Inspect(rt.Ctx(), args[0], entraClient, xdrClient, intuneClient, stale, rt.Clock())
			if err != nil {
				return err
			}
			return showView(rt, output, view)
		},
	}
	command.Flags().BoolVar(&withIntune, "intune", false, "Also read Intune (needs a token that can).")
	command.Flags().BoolVar(&withDefender, "defender", true, "Read Defender.")
	command.Flags().BoolVar(&noDefender, "no-defender", false, "Do not read Defender.")
	command.Flags().StringVar(&staleAfter, "stale-after", "7d", "Warn when Defender last saw it longer ago.")
	common.AddOutput(command, false)
	return command
}

func showView(rt *Runtime, output render.Output, view devices.View) error {
	if output == render.Table {
		printView(rt, view)
	} else {
		rows := make([][]render.Cell, len(view.Findings))
		for index, finding := range view.Findings {
			rows[index] = render.Cells(finding.Level, finding.Message)
		}
		if err := rt.Console.Emit(output, []string{"LEVEL", "FINDING"}, rows, viewRecord(view)); err != nil {
			return err
		}
	}
	for _, finding := range view.Findings {
		if finding.Level == "warn" {
			return Attention
		}
	}
	return nil
}

// viewRecord is everything devices show found, as the services returned it, for -o json.
func viewRecord(view devices.View) map[string]any {
	findings := []any{}
	for _, finding := range view.Findings {
		findings = append(findings, map[string]any{"level": finding.Level, "message": finding.Message})
	}
	entraRecords := []any{}
	for _, device := range view.Entra {
		entraRecords = append(entraRecords, map[string]any{"device": device.Raw, "groups": groupRecords(view.Groups[device.ID])})
	}
	var defender, intuneRecords any
	if view.Defender != nil {
		records := []any{}
		for _, record := range view.Defender.Records {
			records = append(records, record.Raw)
		}
		defender = records
	}
	if view.Intune != nil {
		records := []any{}
		for _, device := range *view.Intune {
			records = append(records, device.Raw)
		}
		intuneRecords = records
	}
	return map[string]any{"name": view.Name, "findings": findings, "entra": entraRecords, "defender": defender, "intune": intuneRecords}
}

// printView is a section for each service, then the findings.
func printView(rt *Runtime, view devices.View) {
	rt.Console.Println(title(rt, fmt.Sprintf("Entra (%d object(s))", len(view.Entra))))
	for _, device := range view.Entra {
		var names []string
		for _, group := range view.Groups[device.ID] {
			names = append(names, group.DisplayName)
		}
		rt.Console.Println(pairs(rt, [][2]string{{"Name", device.DisplayName}, {"Object id", device.ID}, {"Device id", device.DeviceID},
			{"OS", strings.TrimSpace(device.OperatingSystem + " " + device.OSVersion)}, {"Enabled", render.YesNo(device.Enabled)},
			{"Trust", device.TrustType}, {"Last sign-in", rt.Console.When(device.LastSignIn)}, {"Groups", or(strings.Join(names, ", "), "-")}}))
		rt.Console.Println("")
	}
	if view.Defender != nil {
		rt.Console.Println(title(rt, fmt.Sprintf("Defender (%d record(s))", len(view.Defender.Records))))
		if machine := view.Defender.Machine(); machine != nil {
			rt.Console.Println(pairs(rt, [][2]string{{"Name", machine.ComputerDNSName}, {"Machine id", machine.ID},
				{"Onboarding", machine.OnboardingStatus}, {"Health", machine.HealthStatus}, {"Last seen", rt.Console.When(machine.LastSeen)},
				{"OS", strings.TrimSpace(machine.OSPlatform + " " + machine.OSVersion)}, {"Agent", machine.AgentVersion},
				{"Risk", machine.RiskScore}, {"Exposure", machine.ExposureLevel}, {"Tags", strings.Join(machine.MachineTags, ", ")},
				{"Entra device id", machine.AADDeviceID}}))
		}
		rt.Console.Println("")
	}
	if view.Intune != nil {
		rt.Console.Println(title(rt, fmt.Sprintf("Intune (%d record(s))", len(*view.Intune))))
		if len(*view.Intune) > 0 {
			newest := (*view.Intune)[0]
			rt.Console.Println(pairs(rt, [][2]string{{"Name", newest.DeviceName}, {"Compliance", newest.ComplianceState},
				{"Last sync", rt.Console.When(newest.LastSync)}, {"User", newest.UserPrincipalName}, {"Entra device id", newest.AzureADDeviceID}}))
		}
		rt.Console.Println("")
	}
	rt.Console.Println(title(rt, "Findings"))
	rows := make([][]render.Cell, len(view.Findings))
	for index, finding := range view.Findings {
		rows[index] = []render.Cell{render.Coloured(finding.Level, findingColours[finding.Level]), render.Plain(finding.Message)}
	}
	rt.Console.Println(rt.Console.Table([]string{"LEVEL", "FINDING"}, rows))
}

func devicesAVSignature(rt *Runtime) *cobra.Command {
	common := &Common{}
	names := &NameFlags{}
	var atLeast string
	var endpoint, showQuery bool
	command := &cobra.Command{
		Use:   "av-signature [NAMES]...",
		Short: "The Defender Antivirus signature, engine and platform versions of devices.",
		Long: "The Defender Antivirus signature, engine and platform versions of devices.\n\n" +
			"One built-in Advanced Hunting query for every device named, through Graph like 'xdr hunt' (or " +
			"--endpoint). UP TO DATE is Defender's own definitions check. Exits 3 when a device is not found, is out " +
			"of date, or is older than --at-least.",
		RunE: func(_ *cobra.Command, args []string) error {
			wanted, err := names.Names(rt, args, "names")
			if err != nil {
				return err
			}
			if atLeast != "" {
				// A bad version fails before the query runs.
				if _, err := devices.VersionKey(atLeast); err != nil {
					return err
				}
			}
			query, err := devices.AVQuery(wanted)
			if err != nil {
				return err
			}
			if showQuery {
				rt.Console.Println(query)
				return nil
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			result, err := huntWith(rt, profile, query, endpoint)
			if err != nil {
				return err
			}
			return showAV(rt, output, devices.AVStatuses(wanted, result), atLeast, profile.Name)
		},
	}
	names.Add(command)
	command.Flags().StringVar(&atLeast, "at-least", "", "Flag signatures older than this, e.g. 1.419.0.0.")
	command.Flags().BoolVar(&endpoint, "endpoint", false, endpointHelp)
	command.Flags().BoolVar(&showQuery, "show-query", false, "Print the KQL instead of running it.")
	common.AddOutput(command, true)
	return command
}

// huntWith runs a hunting query through Graph, or Defender for Endpoint's own API.
func huntWith(rt *Runtime, profile microsoft.Profile, query string, endpoint bool) (render.QueryResult, error) {
	api, err := rt.API(profile)
	if err != nil {
		return render.QueryResult{}, err
	}
	if endpoint {
		client, err := xdr.New(api, profile.MDEURL)
		if err != nil {
			return render.QueryResult{}, err
		}
		return client.Hunt(rt.Ctx(), query)
	}
	client, err := graph.New(api)
	if err != nil {
		return render.QueryResult{}, err
	}
	return client.Hunt(rt.Ctx(), query, 0)
}

func showAV(rt *Runtime, output render.Output, statuses []devices.AVStatus, atLeast, profile string) error {
	rows := make([][]render.Cell, len(statuses))
	records := []any{}
	missing, stale, behind := 0, 0, 0
	for index, status := range statuses {
		rows[index] = avRow(rt, status, atLeast)
		records = append(records, avRecord(status, atLeast))
		switch {
		case !status.Found:
			missing++
		case status.UpToDate != nil && !*status.UpToDate:
			stale++
		}
		if atLeast != "" && status.Found && status.OlderThan(atLeast) {
			behind++
		}
	}
	if err := rt.Console.Emit(output, []string{"DEVICE", "MACHINE", "OS", "SIGNATURE", "ENGINE", "PLATFORM", "MODE", "UP TO DATE", "REPORTED"},
		rows, records); err != nil {
		return err
	}
	parts := []string{fmt.Sprintf("%d found", len(statuses)-missing)}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d not found", missing))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d out of date", stale))
	}
	if behind > 0 {
		parts = append(parts, fmt.Sprintf("%d older than %s", behind, atLeast))
	}
	rt.Console.Note("%s (profile %s)", strings.Join(parts, ", "), profile)
	if missing+stale+behind > 0 {
		return Attention
	}
	return nil
}

func avRow(rt *Runtime, status devices.AVStatus, atLeast string) []render.Cell {
	if !status.Found {
		row := []render.Cell{render.Plain(status.Query), render.Coloured("not found", "red")}
		for range 7 {
			row = append(row, render.Plain(""))
		}
		return row
	}
	signature := render.Plain(status.Signature)
	if atLeast != "" && status.OlderThan(atLeast) {
		signature = render.Coloured(or(status.Signature, "unknown"), "yellow")
	}
	fresh := render.Plain("-") // the definitions check does not apply, or has not run
	if status.UpToDate != nil {
		fresh = render.Coloured("no", "yellow")
		if *status.UpToDate {
			fresh = render.Coloured("yes", "green")
		}
	}
	return []render.Cell{render.Plain(status.Query), render.Plain(status.DeviceName), render.Plain(status.OSPlatform), signature,
		render.Plain(status.Engine), render.Plain(status.Platform), render.Plain(status.Mode), fresh, render.Plain(rt.Console.When(status.Reported))}
}

func avRecord(status devices.AVStatus, atLeast string) map[string]any {
	var upToDate, older any
	if status.UpToDate != nil {
		upToDate = *status.UpToDate
	}
	if atLeast != "" && status.Found {
		older = status.OlderThan(atLeast)
	}
	return map[string]any{"query": status.Query, "found": status.Found, "device_id": orNil(status.DeviceID),
		"device_name": orNil(status.DeviceName), "os_platform": orNil(status.OSPlatform), "signature_version": orNil(status.Signature),
		"engine_version": orNil(status.Engine), "platform_version": orNil(status.Platform), "mode": orNil(status.Mode),
		"up_to_date": upToDate, "older_than_minimum": older, "reported": render.ISO(status.Reported)}
}
