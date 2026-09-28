package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// The self-test: every read-only command, run against names you choose, to find bugs.
//
// Hidden from --help: it is for trying a build in a real tenant before a release. Each
// command runs in this process with its output captured and thrown away; only what
// happened is kept:
//
//   - ok: it succeeded;
//   - attention: it exited 3, having found something, as designed;
//   - refused: it stopped with an error it explained, most often a missing permission;
//   - usage: it rejected its own arguments, which is a bug in the test or the command;
//   - CRASH: it panicked, which is a bug, reported with the lines of this tool it came
//     through.
//
// Nothing here changes anything, and no token is printed. Run it with a profile that is
// already signed in: a sign-in cannot be answered while it runs.

// ourPackages is where this tool's own code lives, for the lines a crash came through.
const ourPackages = "github.com/libre-devops/ldo-go-cli/internal/"

const jsonSample = `{"value": [{"id": "1", "name": "web01", "on": true, "at": null}]}`

// selfTestCase is one command to run: its arguments, with {device}, {short}, {user},
// {group}, {workspace} and {vault} filled in, and what it needs to be worth running.
type selfTestCase struct {
	args  []string
	needs []string
	slow  bool
	stdin string
}

// Outcome is what one command did: its result, its exit code and time, and for a failure,
// what it said and the lines of this tool it came through.
type Outcome struct {
	Command  string   `json:"command"`
	Result   string   `json:"result"`
	ExitCode *int     `json:"exit_code"`
	Seconds  float64  `json:"seconds"`
	Detail   string   `json:"detail"`
	Hint     *string  `json:"hint"`
	Where    []string `json:"where"`
}

func words(line string) []string { return strings.Fields(line) }

func selfTestCases() []selfTestCase {
	device, group, user := []string{"device"}, []string{"group"}, []string{"user"}
	return []selfTestCase{
		{args: words("welcome")}, {args: words("profiles")}, {args: words("config path")}, {args: words("network test")},
		{args: words("az whoami")}, {args: words("json"), stdin: jsonSample}, {args: words("json --yaml"), stdin: jsonSample},
		{args: words("entra token graph")}, {args: words("entra token mde")}, {args: words("entra token arm")},
		{args: words("graph whoami")}, {args: words("graph get me")},
		{args: words("graph get organization --select id,displayName")}, {args: words("entra ca-policies")},
		{args: words("entra sign-ins --since 1d --limit 5")},
		{args: words("entra app-credentials --expiring 30d"), slow: true},
		{args: words("graph get-device {device}"), needs: device}, {args: words("entra devices {device}"), needs: device},
		{args: words("entra devices {short}"), needs: device}, {args: words("entra device-groups {device}"), needs: device},
		{args: words("xdr machines {device}"), needs: device}, {args: words("xdr machines {short}"), needs: device},
		{args: []string{"xdr", "machines", "{short}", "--sort", "last seen:desc", "--unique", "device"}, needs: device},
		{args: words("xdr alerts --device {device} --include-resolved"), needs: device},
		{args: words("xdr vulns {device}"), needs: device},
		{args: words("xdr timeline {device} --since 1h --endpoint"), needs: device},
		{args: words("xdr timeline {device} --since 1h --type process,network"), needs: device},
		{args: words("xdr vulns {device} --sort severity:desc --sort cvss:desc"), needs: device},
		{args: words("devices check {device}"), needs: device}, {args: words("devices show {device}"), needs: device},
		{args: words("devices av-signature {device} --endpoint"), needs: device},
		{args: words("devices av-signature {device}"), needs: device}, {args: words("intune devices {device}"), needs: device},
		{args: words("graph get-group {group}"), needs: group},
		{args: words("entra devices {device} --group {group}"), needs: []string{"device", "group"}},
		{args: words("devices check {device} --group {group}"), needs: []string{"device", "group"}},
		{args: words("entra group-devices {group}"), needs: group, slow: true},
		{args: words("graph get-user {user}"), needs: user}, {args: words("entra user-groups {user}"), needs: user},
		{args: words("entra user-roles {user}"), needs: user},
		{args: words("entra sign-ins --user {user} --since 7d --limit 5"), needs: user},
		{args: words("azure rbac {user}"), needs: user, slow: true},
		{args: words("xdr alerts --since 24h --limit 5")},
		{args: words("xdr alerts --since 7d --sort severity:desc --unique title")},
		{args: words("xdr indicators")}, {args: words("xdr detections list")},
		{args: words("xdr stale --older-than 180d"), slow: true},
		{args: []string{"xdr", "hunt", "DeviceInfo | take 1", "--endpoint"}}, {args: []string{"xdr", "hunt", "DeviceInfo | take 1"}},
		{args: words("xdr incidents top")}, {args: words("xdr incidents summary --since 1d")},
		{args: words("azure subscriptions")}, {args: []string{"azure", "resource-graph", "resources | take 1"}},
		{args: words("azure secure-score")}, {args: words("azure defender-plans")},
		{args: words("azure recommendations --severity high"), slow: true}, {args: words("azure automation accounts")},
		// Only a vault named: a request to every vault in the tenant, refused and logged by
		// each one the person cannot read, looks like reconnaissance to Defender for Key
		// Vault.
		{args: words("keyvault expiry {vault} --within 30d"), needs: []string{"vault"}},
		{args: []string{"logs", "query", "Heartbeat | take 1", "--workspace", "{workspace}"}, needs: []string{"workspace"}},
		{args: words("logs ingestion --workspace {workspace}"), needs: []string{"workspace"}},
		{args: words("pim eligible --azure")}, {args: words("pim active --azure")}, {args: words("pim eligible")},
		{args: words("news messages --since 7d --limit 5")}, {args: words("planner plans")},
		{args: words("snow whoami"), needs: []string{"snow"}}, {args: words("snow instance"), needs: []string{"snow"}},
		{args: words("jira whoami"), needs: []string{"atlassian"}}, {args: words("jira projects"), needs: []string{"atlassian"}},
		{args: words("confluence spaces"), needs: []string{"atlassian"}},
	}
}

// nameOptions are the option that gives each name a case can need.
var nameOptions = map[string]string{"device": "--device", "short": "--device", "user": "--user", "group": "--group",
	"workspace": "--workspace", "vault": "--vault", "snow": "--snow", "atlassian": "--atlassian"}

var selfTestResults = []string{"ok", "attention", "refused", "usage", "CRASH"}

type selfTestFlags struct {
	common                                Common
	device, user, group, workspace, vault string
	snow, atlassian, everything           bool
	only                                  []string
	report                                string
}

func selfTestCommand(rt *Runtime) *cobra.Command {
	var flags selfTestFlags
	command := &cobra.Command{
		Use:    "self-test",
		Short:  "Run every read-only command against the names given, and report what broke.",
		Hidden: true,
		Long: "Run every read-only command against the names given, and report what broke.\n\n" +
			"Output is thrown away; only each command's outcome is shown: ok, attention (exit 3, as designed), refused (an " +
			"explained error, often a permission), usage, or CRASH (a bug, with the line of " + brand.Command + " it came " +
			"from). After the table, each failure is shown in full with its hint. Exits 1 when anything crashed.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return runSelfTest(rt, &flags) },
	}
	options := command.Flags()
	options.StringVar(&flags.device, "device", "", "A device to look up: its FQDN is best.")
	options.StringVar(&flags.user, "user", "", "A user's UPN to look up.")
	options.StringVar(&flags.group, "group", "", "An Entra group, by display name or object id.")
	options.StringVar(&flags.workspace, "workspace", "", "A Log Analytics workspace: its Workspace ID, its resource id or its name.")
	options.StringVar(&flags.vault, "vault", "", "A Key Vault you can read, for keyvault expiry.")
	options.BoolVar(&flags.snow, "snow", false, "Also test the ServiceNow commands.")
	options.BoolVar(&flags.atlassian, "atlassian", false, "Also test the Jira and Confluence commands.")
	options.BoolVar(&flags.everything, "all", false, "Also run the slow ones (whole-tenant listings).")
	options.StringArrayVar(&flags.only, "only", nil, "Only commands starting with this, e.g. xdr. Repeatable.")
	options.StringVar(&flags.report, "report", "", "Also write every outcome, and where each crash was, here.")
	flags.common.AddOutput(command, true)
	options.Lookup("profile").Usage = "The profile every command uses."
	return command
}

func (f *selfTestFlags) names() map[string]string {
	names := map[string]string{"device": f.device, "user": f.user, "group": f.group, "workspace": f.workspace, "vault": f.vault}
	if f.device != "" {
		names["short"] = util.ShortName(f.device)
	}
	if f.snow {
		names["snow"] = "yes"
	}
	if f.atlassian {
		names["atlassian"] = "yes"
	}
	return names
}

// wanted reports whether a case is chosen by --all and --only, whatever names it needs.
func (f *selfTestFlags) wanted(item selfTestCase) bool {
	if item.slow && !f.everything {
		return false
	}
	if len(f.only) == 0 {
		return true
	}
	line := strings.Join(item.args, " ")
	return slices.ContainsFunc(f.only, func(prefix string) bool { return strings.HasPrefix(line, prefix) })
}

// selfTestRun is one command line to run, and what it reads on stdin.
type selfTestRun struct {
	args  []string
	stdin string
}

// plan is the command lines to run, each once, and how many cases were left out for want
// of a name, with the options that give them.
func (f *selfTestFlags) plan() ([]selfTestRun, int, []string) {
	names := f.names()
	var runs []selfTestRun
	seen := map[string]bool{}
	skipped := 0
	options := map[string]bool{}
	for _, item := range selfTestCases() {
		if !f.wanted(item) {
			continue
		}
		var missing []string
		for _, need := range item.needs {
			if names[need] == "" {
				missing = append(missing, need)
			}
		}
		if len(missing) > 0 {
			skipped++
			for _, need := range missing {
				options[nameOptions[need]] = true
			}
			continue
		}
		args := make([]string, len(item.args))
		for index, part := range item.args {
			for name, value := range names {
				part = strings.ReplaceAll(part, "{"+name+"}", value)
			}
			args[index] = part
		}
		// Filled in, a short --device makes {device} and {short} the same: run it once.
		key := strings.Join(args, "\x00") + "\x00" + item.stdin
		if !seen[key] {
			seen[key] = true
			runs = append(runs, selfTestRun{args: args, stdin: item.stdin})
		}
	}
	listed := make([]string, 0, len(options))
	for option := range options {
		listed = append(listed, option)
	}
	slices.Sort(listed)
	return runs, skipped, listed
}

func runSelfTest(rt *Runtime, flags *selfTestFlags) error {
	output, err := flags.common.Output(rt)
	if err != nil {
		return err
	}
	runs, skipped, options := flags.plan()
	rt.Console.Note("running %d commands; each one's output is discarded", len(runs))
	if skipped > 0 {
		rt.Console.Note("%d more need %s: give them to run those too", skipped, strings.Join(options, ", "))
	}
	width := len(fmt.Sprint(len(runs)))
	var outcomes []Outcome
	for number, planned := range runs {
		outcome, interrupted := runOne(rt, planned, flags.common.Profile)
		if interrupted {
			rt.Console.Warn("stopped after %d of %d", len(outcomes), len(runs))
			break
		}
		outcomes = append(outcomes, outcome)
		rt.Console.Note("%*d/%d  %-9s  %s %s", width, number+1, len(runs), outcome.Result, brand.Command, outcome.Command)
	}
	if err := showOutcomes(rt, output, outcomes); err != nil {
		return err
	}
	if flags.report != "" {
		if err := writeOutcomes(flags.report, outcomes); err != nil {
			return err
		}
		rt.Console.Note("wrote %s", flags.report)
	}
	counts := map[string]int{}
	for _, item := range outcomes {
		counts[item.Result]++
	}
	var summary []string
	for _, name := range selfTestResults {
		if counts[name] > 0 {
			summary = append(summary, fmt.Sprintf("%d %s", counts[name], name))
		}
	}
	rt.Console.Note("%s", strings.Join(summary, ", "))
	if counts["CRASH"] > 0 || counts["usage"] > 0 {
		return &ExitStatus{Code: ExitError}
	}
	return nil
}

func writeOutcomes(path string, outcomes []Outcome) error {
	if outcomes == nil {
		outcomes = []Outcome{}
	}
	encoded, err := json.MarshalIndent(outcomes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return errs.Inputf("cannot write %s: %v", path, err)
	}
	return nil
}

// caseRuntime is a runtime for one case: this one's settings and sign-ins, its output
// kept apart, and nobody to answer a question.
func caseRuntime(rt *Runtime, stdin, profile string, out, err *bytes.Buffer) *Runtime {
	getenv := rt.Env
	if profile != "" {
		getenv = func(name string) string {
			if name == brand.ProfileEnv {
				return profile
			}
			return rt.Env(name)
		}
	}
	rt.mu.Lock()
	if rt.tokens == nil {
		rt.tokens = map[string]auth.TokenProvider{}
	}
	// One case's sign-in serves the next, as one command's clients share theirs.
	tokens := rt.tokens
	rt.mu.Unlock()
	return &Runtime{
		ConfigPath: rt.ConfigPath, Console: &render.Console{Out: out, Err: err, Now: rt.Now}, Getenv: getenv,
		HTTPClient: rt.HTTPClient, AzRunner: rt.AzRunner, Runner: rt.Runner, LookPath: rt.LookPath, Credential: rt.Credential,
		TokenStore: rt.TokenStore, Now: rt.Now, Sleep: rt.Sleep, Stdin: strings.NewReader(stdin), StdinIsTerminal: stdin == "",
		Context: rt.Ctx(), Interactive: func() bool { return false }, HasBrowser: func() bool { return false },
		OpenBrowser: func(string) error { return nil }, tokens: tokens,
	}
}

// runOne runs one command in this process, with its output captured and dropped; and
// whether it was interrupted.
func runOne(rt *Runtime, planned selfTestRun, profile string) (outcome Outcome, interrupted bool) {
	var out, errOut bytes.Buffer
	sub := caseRuntime(rt, planned.stdin, profile, &out, &errOut)
	outcome = Outcome{Command: strings.Join(planned.args, " "), Where: []string{}}
	started := rt.Clock()
	// Each command line sets the default logger up for its own output: put it back after.
	logger := slog.Default()
	defer func() {
		slog.SetDefault(logger)
		if found := recover(); found != nil {
			outcome.Result, outcome.ExitCode = "CRASH", nil
			outcome.Detail = fmt.Sprintf("%T: %v", found, found)
			outcome.Where = crashLines()
		}
		outcome.Seconds = math.Round(rt.Clock().Sub(started).Seconds()*100) / 100
	}()
	code, err := execute(sub, planned.args)
	if code == ExitInterrupted || errors.Is(rt.Ctx().Err(), context.Canceled) {
		return outcome, true
	}
	classify(&outcome, code, err, errOut.String())
	return outcome, false
}

func classify(outcome *Outcome, code int, err error, stderr string) {
	outcome.ExitCode = &code
	switch code {
	case ExitOK:
		outcome.Result = "ok"
	case ExitAttention:
		outcome.Result, outcome.Detail = "attention", "exit 3: a finding, as designed"
	case ExitUsage:
		outcome.Result, outcome.Detail = "usage", lastLine(stderr)
	default:
		outcome.Result = "refused"
		if found := errs.As(err); found != nil {
			outcome.Detail = found.Message
			if found.Hint != "" {
				outcome.Hint = &found.Hint
			}
			return
		}
		said, hint := explanation(stderr)
		outcome.Detail, outcome.Hint = said, hint
	}
}

// crashLines are the lines of this tool a panic came through, innermost last, at most
// three, as file:line in function.
func crashLines() []string {
	callers := make([]uintptr, 64)
	count := runtime.Callers(3, callers)
	frames := runtime.CallersFrames(callers[:count])
	var ours []string
	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, ourPackages) && !strings.Contains(frame.Function, "runOne") {
			name := frame.Function[strings.LastIndex(frame.Function, ".")+1:]
			ours = append(ours, fmt.Sprintf("%s:%d in %s", filepath.Base(frame.File), frame.Line, name))
		}
		if !more {
			break
		}
	}
	// Callers are innermost first: the three nearest the panic, outermost of them first.
	ours = ours[:min(3, len(ours))]
	slices.Reverse(ours)
	return ours
}

// lastLine is the last line of text with anything on it, at most 200 bytes of it.
func lastLine(text string) string {
	last := ""
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			last = line
		}
	}
	return last[:min(200, len(last))]
}

// explanation is why a command that exited 1 without an error of ours stopped: every
// error and warning it wrote, one a line, and their hints. Its last line alone is often
// only its summary (a count of what it read), with the reason in a warning above it.
func explanation(text string) (string, *string) {
	var said, hints []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case (strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "warning:")) && !slices.Contains(said, line):
			said = append(said, line)
		case strings.HasPrefix(line, "hint:"):
			if hint := strings.TrimSpace(line[5:]); !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		}
	}
	if len(said) == 0 {
		return lastLine(text), nil
	}
	if len(hints) == 0 {
		return strings.Join(said, "\n"), nil
	}
	hint := strings.Join(hints, "\n")
	return strings.Join(said, "\n"), &hint
}

var resultColours = map[string]string{"ok": "green", "attention": "yellow", "refused": "yellow", "usage": "red", "CRASH": "red"}

func showOutcomes(rt *Runtime, output render.Output, outcomes []Outcome) error {
	rows := make([][]render.Cell, len(outcomes))
	records := make([]any, len(outcomes))
	for index, item := range outcomes {
		lines := strings.Split(item.Detail, "\n")
		detail := lines[0]
		if len(lines) > 1 {
			detail += fmt.Sprintf(" (and %d more)", len(lines)-1)
		}
		if len(item.Where) > 0 {
			detail += " (at " + item.Where[len(item.Where)-1] + ")"
		}
		rows[index] = []render.Cell{render.Plain(item.Command), render.Coloured(item.Result, resultColours[item.Result]),
			render.Plain(fmt.Sprintf("%.1fs", item.Seconds)), render.Plain(detail)}
		records[index] = item
	}
	if err := rt.Console.Emit(output, []string{"COMMAND", "RESULT", "TIME", "DETAIL"}, rows, records); err != nil {
		return err
	}
	if output == render.Table {
		showFailures(rt, outcomes)
	}
	return nil
}

// showFailures is each failure in full, since the table cuts DETAIL short when commands
// are long: what it said, its hint, and for a crash, the lines of this tool it came
// through.
func showFailures(rt *Runtime, outcomes []Outcome) {
	for _, item := range outcomes {
		if item.Result != "refused" && item.Result != "usage" && item.Result != "CRASH" {
			continue
		}
		rt.Console.Println("")
		heading := item.Result + ": " + brand.Command + " " + item.Command
		if rt.Console.ColourOut() {
			heading = colour.Style(heading, nil, true, false)
		}
		rt.Console.Println(heading)
		var lines [][2]string
		for _, said := range strings.Split(item.Detail, "\n") {
			lines = append(lines, [2]string{"Said", said})
		}
		hint := ""
		if item.Hint != nil {
			hint = *item.Hint
		}
		for _, line := range strings.Split(hint, "\n") {
			lines = append(lines, [2]string{"Hint", line})
		}
		for _, where := range item.Where {
			lines = append(lines, [2]string{"At", where})
		}
		rt.Console.Println(pairs(rt, lines))
	}
}
