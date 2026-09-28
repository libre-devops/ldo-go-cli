package cli

import (
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/automation"
)

// An account is named by its name (found across the subscriptions in scope, narrowed by
// -g) or by its resource id. A job is named by its id; without one, logs and output take
// the newest job, of --runbook when given. Everything reads.

var jobColours = map[string]string{"Completed": "green", "Running": "cyan", "Failed": "red", "Suspended": "red"}

var streamColours = map[string]string{"Error": "red", "Warning": "yellow", "Verbose": "bright_black"}

// automationCommand is azure automation: accounts, jobs, logs, output.
func automationCommand(rt *Runtime) *cobra.Command {
	group := newGroup("automation", "Azure Automation: accounts, runbook jobs, and each job's logs and output.")
	group.AddCommand(automationAccounts(rt), automationJobs(rt), automationLogs(rt), automationOutput(rt))
	return group
}

// accountFlags name an Automation account's place: its resource group and subscriptions.
type accountFlags struct {
	group         string
	subscriptions []string
	runbook       string
}

func (f *accountFlags) add(cmd *cobra.Command, runbook bool) {
	cmd.Flags().StringVarP(&f.group, "resource-group", "g", "", "The account's resource group, when names repeat.")
	cmd.Flags().StringArrayVarP(&f.subscriptions, "subscription", "s", nil, "Subscription id to search. Repeatable. Default: "+
		"the profile's subscription, else every one in the tenant.")
	if runbook {
		cmd.Flags().StringVarP(&f.runbook, "runbook", "r", "", "Only this runbook's jobs.")
	}
}

// account is the client and the account ref names. A resource id needs no search, so
// the subscriptions are only listed for a name.
func (f *accountFlags) account(rt *Runtime, common *Common, ref string) (*automation.Client, automation.Account, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return nil, automation.Account{}, err
	}
	client, err := automationClient(rt, profile)
	if err != nil {
		return nil, automation.Account{}, err
	}
	var scope []string
	if !strings.HasPrefix(strings.TrimSpace(ref), "/") {
		if scope, err = automationScope(rt, profile, f.subscriptions); err != nil {
			return nil, automation.Account{}, err
		}
	}
	found, err := client.FindAccount(rt.Ctx(), ref, scope, f.group)
	return client, found, err
}

func automationClient(rt *Runtime, profile microsoft.Profile) (*automation.Client, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	return automation.New(api)
}

func automationScope(rt *Runtime, profile microsoft.Profile, given []string) ([]string, error) {
	if len(given) > 0 {
		return given, nil
	}
	client, err := azureClient(rt, profile)
	if err != nil {
		return nil, err
	}
	return subscriptionIDs(rt, client, profile, nil)
}

func automationAccounts(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &accountFlags{}
	command := &cobra.Command{
		Use:   "accounts",
		Short: "List the Automation accounts in the subscriptions in scope.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			scope, err := automationScope(rt, profile, flags.subscriptions)
			if err != nil {
				return err
			}
			client, err := automationClient(rt, profile)
			if err != nil {
				return err
			}
			found, err := client.Accounts(rt.Ctx(), scope)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []fields.Object{}
			for index, item := range found {
				rows[index] = render.Cells(item.Name, item.ResourceGroup, item.Location, item.SubscriptionID)
				records = append(records, item.Raw)
			}
			if err := rt.Console.Emit(output, []string{"ACCOUNT", "RESOURCE GROUP", "LOCATION", "SUBSCRIPTION"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d account(s)", len(found))
			return nil
		},
	}
	command.Flags().StringArrayVarP(&flags.subscriptions, "subscription", "s", nil, "Subscription id to search. Repeatable. "+
		"Default: the profile's subscription, else every one in the tenant.")
	common.AddOutput(command, true)
	return command
}

func automationJobs(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &accountFlags{}
	var statuses []string
	var failed bool
	var since string
	var limit int
	command := &cobra.Command{
		Use:   "jobs ACCOUNT",
		Short: "List a runbook's recent jobs (runs), newest first: when, how long, and how each ended.",
		Long: "List a runbook's recent jobs (runs), newest first: when, how long, and how each ended.\n\n" +
			"ACCOUNT is the Automation account's name or resource id. Jobs are kept for 30 days. Exits 3 when any job " +
			"shown failed, was suspended or stopped.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			var window time.Duration
			if since != "" {
				span, err := Duration("--since", since)
				if err != nil {
					return err
				}
				window = span
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, found, err := flags.account(rt, common, args[0])
			if err != nil {
				return err
			}
			jobs, err := client.Jobs(rt.Ctx(), found)
			if err != nil {
				return err
			}
			now := rt.Clock()
			var shown []automation.Job
			for _, job := range jobs {
				if len(shown) < limit && jobWanted(job, flags.runbook, statuses, failed, window, now) {
					shown = append(shown, job)
				}
			}
			return showJobs(rt, output, found, shown, now)
		},
	}
	flags.add(command, true)
	command.Flags().StringArrayVar(&statuses, "status", nil, "Only jobs in this state, e.g. failed, completed, running. Repeatable.")
	command.Flags().BoolVar(&failed, "failed", false, "Only jobs that failed, were suspended or stopped.")
	command.Flags().StringVar(&since, "since", "", "Only jobs created in this window, e.g. 24h, 7d.")
	command.Flags().IntVarP(&limit, "limit", "n", 20, "How many to show.")
	common.AddOutput(command, true)
	return command
}

func jobWanted(job automation.Job, runbook string, statuses []string, failed bool, window time.Duration, now time.Time) bool {
	if runbook != "" && !strings.EqualFold(job.Runbook, runbook) {
		return false
	}
	if len(statuses) > 0 {
		match := false
		for _, status := range statuses {
			match = match || strings.EqualFold(job.Status, status)
		}
		if !match {
			return false
		}
	}
	if failed && !job.IsFailed() {
		return false
	}
	return window == 0 || (!job.Created.IsZero() && now.Sub(job.Created) <= window)
}

func showJobs(rt *Runtime, output render.Output, account automation.Account, shown []automation.Job, now time.Time) error {
	rows := make([][]render.Cell, len(shown))
	records := []fields.Object{}
	failures := 0
	for index, job := range shown {
		colour := jobColours[job.Status]
		if job.IsFailed() {
			colour = "red"
			failures++
		}
		started := job.Started
		if started.IsZero() {
			started = job.Created
		}
		rows[index] = []render.Cell{render.Plain(job.ID), render.Plain(job.Runbook), render.Coloured(job.Status, colour),
			render.Plain(render.When(started, now)), render.Plain(took(job, now)), render.Plain(or(job.RunOn, "Azure"))}
		records = append(records, job.Raw)
	}
	if err := rt.Console.Emit(output, []string{"JOB", "RUNBOOK", "STATUS", "STARTED", "TOOK", "RUN ON"}, rows, records); err != nil {
		return err
	}
	note := strconv.Itoa(len(shown)) + " job(s) in " + account.Name
	if failures > 0 {
		note += ", " + strconv.Itoa(failures) + " failed"
	}
	rt.Console.Note("%s", note)
	if failures > 0 {
		return Attention
	}
	return nil
}

func took(job automation.Job, now time.Time) string {
	if job.Started.IsZero() {
		return "-"
	}
	if job.Ended.IsZero() {
		return util.FormatDuration(now.Sub(job.Started)) + " so far"
	}
	return util.FormatDuration(job.Ended.Sub(job.Started))
}

func streamKinds(given []string) (map[string]bool, error) {
	if len(given) == 0 {
		return nil, nil
	}
	kinds := map[string]bool{}
	var names []string
	for _, stream := range automation.Streams {
		names = append(names, stream[0])
	}
	for _, item := range given {
		found := ""
		for _, stream := range automation.Streams {
			if stream[0] == strings.ToLower(strings.TrimSpace(item)) {
				found = stream[1]
			}
		}
		if found == "" {
			return nil, errs.Inputf("unknown stream '%s'", item).WithHint("use one of %s", strings.Join(names, ", "))
		}
		kinds[found] = true
	}
	return kinds, nil
}

// newestJob is the account's newest job, of runbook when given.
func newestJob(rt *Runtime, client *automation.Client, account automation.Account, runbook string) (automation.Job, error) {
	jobs, err := client.Jobs(rt.Ctx(), account)
	if err != nil {
		return automation.Job{}, err
	}
	for _, job := range jobs {
		if runbook == "" || strings.EqualFold(job.Runbook, runbook) {
			return job, nil
		}
	}
	what := account.Name
	if runbook != "" {
		what = "runbook '" + runbook + "'"
	}
	return automation.Job{}, errs.NotFoundf("no jobs for %s in the last 30 days", what)
}

func automationLogs(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &accountFlags{}
	var streams []string
	var full bool
	var names []string
	for _, stream := range automation.Streams {
		names = append(names, stream[0])
	}
	command := &cobra.Command{
		Use:   "logs ACCOUNT [JOB]",
		Short: "A job's logs, oldest first: its output, warnings, errors and other streams.",
		Long: "A job's logs, oldest first: its output, warnings, errors and other streams.\n\n" +
			"Without a job id, the newest job, of --runbook when given. Verbose, progress and debug records appear only " +
			"when the runbook turns them on. Exits 3 when the job failed.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			kinds, err := streamKinds(streams)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, found, err := flags.account(rt, common, args[0])
			if err != nil {
				return err
			}
			chosen, err := chosenJob(rt, client, found, args, flags.runbook)
			if err != nil {
				return err
			}
			records, err := jobRecords(rt, client, found, chosen, kinds, full)
			if err != nil {
				return err
			}
			return showLogs(rt, output, found, chosen, records)
		},
	}
	flags.add(command, true)
	command.Flags().StringArrayVar(&streams, "stream", nil, "Only these streams: "+strings.Join(names, ", ")+". Repeatable. Default: all.")
	command.Flags().BoolVar(&full, "full", false, "Read each record in full, not its summary (a call each).")
	common.AddOutput(command, true)
	return command
}

// chosenJob is the job named, read singly, else the newest (of the runbook).
func chosenJob(rt *Runtime, client *automation.Client, account automation.Account, args []string, runbook string) (automation.Job, error) {
	id := ""
	if len(args) > 1 {
		id = args[1]
	} else {
		newest, err := newestJob(rt, client, account, runbook)
		if err != nil {
			return automation.Job{}, err
		}
		id = newest.ID
	}
	return client.Job(rt.Ctx(), account, id)
}

func jobRecords(rt *Runtime, client *automation.Client, account automation.Account, job automation.Job, kinds map[string]bool,
	full bool) ([]automation.Stream, error) {
	all, err := client.Streams(rt.Ctx(), account, job.ID)
	if err != nil {
		return nil, err
	}
	var records []automation.Stream
	for _, item := range all {
		if kinds != nil && !kinds[item.Stream] {
			continue
		}
		if full {
			if item, err = client.Stream(rt.Ctx(), account, job.ID, item.ID); err != nil {
				return nil, err
			}
		}
		records = append(records, item)
	}
	return records, nil
}

func showLogs(rt *Runtime, output render.Output, account automation.Account, job automation.Job, records []automation.Stream) error {
	if output == render.Table {
		rt.Console.Println(pairs(rt, jobPairs(rt, account, job)))
		rt.Console.Println("")
	}
	rows := make([][]render.Cell, len(records))
	streams := []any{}
	for index, item := range records {
		rows[index] = []render.Cell{render.Plain(rt.Console.When(item.Time)), render.Coloured(item.Stream, streamColours[item.Stream]),
			render.Plain(item.Message())}
		streams = append(streams, map[string]any{"id": item.ID, "time": render.ISO(item.Time), "stream": item.Stream, "message": item.Message()})
	}
	if err := rt.Console.Emit(output, []string{"TIME", "STREAM", "MESSAGE"}, rows, map[string]any{"job": job.Raw, "streams": streams}); err != nil {
		return err
	}
	rt.Console.Note("%d record(s)", len(records))
	if job.IsFailed() {
		return Attention
	}
	return nil
}

func jobPairs(rt *Runtime, account automation.Account, job automation.Job) [][2]string {
	started := job.Started
	if started.IsZero() {
		started = job.Created
	}
	items := [][2]string{{"Account", account.Name + " (" + account.ResourceGroup + ")"}, {"Runbook", job.Runbook}, {"Job", job.ID},
		{"Status", job.Status}, {"Started", rt.Console.When(started)}, {"Took", took(job, rt.Clock())}, {"Run on", or(job.RunOn, "Azure")}}
	if job.StartedBy != "" {
		items = append(items, [2]string{"Started by", job.StartedBy})
	}
	if job.Exception != "" {
		items = append(items, [2]string{"Exception", job.Exception})
	}
	return items
}

func automationOutput(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &accountFlags{}
	command := &cobra.Command{
		Use:   "output ACCOUNT [JOB]",
		Short: "A job's output as plain text, as the portal's Output tab shows it, for piping.",
		Long:  "A job's output as plain text, as the portal's Output tab shows it, for piping.\n\nWithout a job id, the newest job, of --runbook when given.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			client, found, err := flags.account(rt, common, args[0])
			if err != nil {
				return err
			}
			id := ""
			if len(args) > 1 {
				id = args[1]
			} else {
				newest, err := newestJob(rt, client, found, flags.runbook)
				if err != nil {
					return err
				}
				id = newest.ID
			}
			text, err := client.Output(rt.Ctx(), found, id)
			if err != nil {
				return err
			}
			rt.Console.Println(strings.TrimRight(text, "\n"))
			rt.Console.Note("job %s in %s", id, found.Name)
			return nil
		},
	}
	flags.add(command, true)
	common.AddProfile(command)
	return command
}
