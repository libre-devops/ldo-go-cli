package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/news"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/planner"
)

// plannerCommand is the planner group: plans and tasks, and raising a task for each Message
// Center post a plan does not have one for yet, or one a month summing them up.
func plannerCommand(rt *Runtime) *cobra.Command {
	group := newGroup("planner", "Microsoft Planner: plans, buckets and tasks, and tasks for Message Center posts.")
	group.AddCommand(plannerPlans(rt), plannerBuckets(rt), plannerTasks(rt), plannerAddNews(rt), plannerAddRollup(rt))
	return group
}

func plannerClient(rt *Runtime, common *Common) (*planner.Client, *news.Client, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return nil, nil, err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, nil, err
	}
	plans, err := planner.New(api)
	if err != nil {
		return nil, nil, err
	}
	posts, err := news.New(api)
	return plans, posts, err
}

func plannerPlans(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "plans",
		Short: "Your plans: those shared with you.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := plannerClient(rt, common)
			if err != nil {
				return err
			}
			found, err := client.Plans(rt.Ctx())
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []any{}
			for index, plan := range found {
				rows[index] = render.Cells(plan.Title, render.Moment(plan.Created), plan.ID)
				records = append(records, map[string]any{"id": plan.ID, "title": plan.Title, "owner": orNil(plan.Owner), "created": render.ISO(plan.Created)})
			}
			return rt.Console.Emit(output, []string{"TITLE", "CREATED", "ID"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	return command
}

func plannerBuckets(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "buckets PLAN",
		Short: "A plan's buckets: the columns of its board.",
		Long:  "A plan's buckets: the columns of its board. PLAN is the plan's title or id.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := plannerClient(rt, common)
			if err != nil {
				return err
			}
			plan, err := client.Plan(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			found, err := client.Buckets(rt.Ctx(), plan.ID)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []any{}
			for index, bucket := range found {
				rows[index] = render.Cells(bucket.Name, bucket.ID)
				records = append(records, map[string]any{"id": bucket.ID, "name": bucket.Name})
			}
			return rt.Console.Emit(output, []string{"BUCKET", "ID"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	return command
}

func plannerTasks(rt *Runtime) *cobra.Command {
	common := &Common{}
	var bucket string
	var openOnly bool
	command := &cobra.Command{
		Use:   "tasks PLAN",
		Short: "A plan's tasks: each one's bucket, progress and dates.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := plannerClient(rt, common)
			if err != nil {
				return err
			}
			plan, err := client.Plan(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			buckets, err := client.Buckets(rt.Ctx(), plan.ID)
			if err != nil {
				return err
			}
			names := map[string]string{}
			for _, item := range buckets {
				names[item.ID] = item.Name
			}
			found, err := client.Tasks(rt.Ctx(), plan.ID)
			if err != nil {
				return err
			}
			if bucket != "" {
				wanted, err := client.Bucket(rt.Ctx(), plan.ID, bucket)
				if err != nil {
					return err
				}
				found = slices.DeleteFunc(found, func(task planner.Task) bool { return task.BucketID != wanted.ID })
			}
			if openOnly {
				found = slices.DeleteFunc(found, planner.Task.Done)
			}
			return showTasks(rt, output, plan, found, names)
		},
	}
	command.Flags().StringVar(&bucket, "bucket", "", "Only this bucket's tasks.")
	command.Flags().BoolVar(&openOnly, "open", false, "Only tasks not complete.")
	common.AddOutput(command, true)
	return command
}

func showTasks(rt *Runtime, output render.Output, plan planner.Plan, found []planner.Task, names map[string]string) error {
	rows := make([][]render.Cell, len(found))
	records := []any{}
	for index, task := range found {
		done := render.Plain(strconv.Itoa(task.PercentComplete) + "%")
		if task.Done() {
			done = render.Coloured("done", "green")
		}
		due := "-"
		if !task.Due.IsZero() {
			due = render.Moment(task.Due)
		}
		rows[index] = []render.Cell{render.Plain(task.Title), render.Plain(or(names[task.BucketID], task.BucketID)), done, render.Plain(due),
			render.Plain(render.Moment(task.Created)), render.Plain(task.ID)}
		records = append(records, map[string]any{"id": task.ID, "title": task.Title, "bucket": orNil(names[task.BucketID]),
			"bucket_id": task.BucketID, "percent_complete": task.PercentComplete, "done": task.Done(), "due": render.ISO(task.Due),
			"created": render.ISO(task.Created), "completed": render.ISO(task.Completed)})
	}
	if err := rt.Console.Emit(output, []string{"TITLE", "BUCKET", "DONE", "DUE", "CREATED", "ID"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%d task(s) in %s", len(found), plan.Title)
	return nil
}

// target is the plan and bucket new tasks go in.
type target struct {
	client *planner.Client
	posts  *news.Client
	plan   planner.Plan
	bucket planner.Bucket
}

func (t target) where() string { return t.plan.Title + " / " + t.bucket.Name }

func openTarget(rt *Runtime, common *Common, plan, bucket string) (target, error) {
	client, posts, err := plannerClient(rt, common)
	if err != nil {
		return target{}, err
	}
	chosen, err := client.Plan(rt.Ctx(), plan)
	if err != nil {
		return target{}, err
	}
	column, err := client.Bucket(rt.Ctx(), chosen.ID, bucket)
	return target{client: client, posts: posts, plan: chosen, bucket: column}, err
}

func plannerAddNews(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &newsFlags{}
	var bucket, layout string
	var write bool
	command := &cobra.Command{
		Use:   "add-news PLAN",
		Short: "A task for each Message Center post the plan has none for yet, in one bucket.",
		Long: "A task for each Message Center post the plan has none for yet, in one bucket: the posts changed in the last " +
			"7 days by default.\n\nA post has a task when any task in the plan (in any bucket, done or not) has a title holding " +
			"its id in square brackets, as Microsoft's own Message Center sync writes them and as these are raised, or " +
			"starting with it (MC1183010: and its title, the short layout). Without --write this only says which it would " +
			"raise. Needs ServiceMessage.Read.All and a Planner scope (Tasks.ReadWrite to raise): use a profile with your own " +
			"app registration. Exits 3 when there are posts it has not raised a task for.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if bucket == "" {
				return Usagef("--bucket", "--bucket is required")
			}
			if !slices.Contains(news.Layouts, strings.ToLower(layout)) {
				return Usagef("--layout", "--layout must be sync or short")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			start, end, said, err := flags.window(rt, "7d")
			if err != nil {
				return err
			}
			into, err := openTarget(rt, common, args[0], bucket)
			if err != nil {
				return err
			}
			return addNews(rt, output, into, flags.query(start, end), strings.ToLower(layout), said, write)
		},
	}
	command.Flags().StringVar(&bucket, "bucket", "", "The bucket new tasks go in.")
	flags.add(command)
	command.Flags().StringVar(&layout, "layout", "sync", "How a task reads. sync: as Microsoft's own Message Center sync to Planner writes "+
		"them, the services, the title and the post's id in brackets, and the notes starting with its id, date, category and tags. "+
		"short: the id, then the title.")
	command.Flags().BoolVar(&write, "write", false, "Raise the tasks, rather than only say which.")
	common.AddOutput(command, true)
	return command
}

func addNews(rt *Runtime, output render.Output, into target, query news.Query, layout, said string, write bool) error {
	found, err := into.posts.Messages(rt.Ctx(), query)
	if err != nil {
		return err
	}
	tasks, err := into.client.Tasks(rt.Ctx(), into.plan.ID)
	if err != nil {
		return err
	}
	raised := planner.Keyed(tasks, news.MessageKey)
	rows := make([][]render.Cell, len(found))
	records := []any{}
	counts := map[string]int{}
	for index, message := range found {
		state := "to raise"
		cell := render.Coloured(state, "yellow")
		if _, has := raised[strings.ToUpper(message.ID)]; has {
			state, cell = "raised already", render.Coloured("raised already", "green")
		} else if write {
			if _, err := into.client.CreateTask(rt.Ctx(), into.plan.ID, into.bucket.ID, news.TaskTitle(message, layout, planner.TitleLimit),
				news.TaskNotes(message, layout)); err != nil {
				return err
			}
			state, cell = "raised", render.Coloured("raised", "green")
		}
		counts[state]++
		rows[index] = []render.Cell{render.Plain(message.ID), render.Plain(rt.Console.When(message.Updated)), render.Plain(message.Title), cell}
		records = append(records, map[string]any{"message": message.ID, "title": message.Title, "task": state})
	}
	if err := rt.Console.Emit(output, []string{"MESSAGE", "UPDATED", "TITLE", "TASK"}, rows, records); err != nil {
		return err
	}
	if write {
		rt.Console.Note("raised %d task(s) in %s; %d had one", counts["raised"], into.where(), counts["raised already"])
	} else {
		rt.Console.Note("%d to raise in %s, %d raised already (posts %s; --write raises them)", counts["to raise"], into.where(), counts["raised already"], said)
	}
	if counts["to raise"] > 0 {
		return Attention
	}
	return nil
}

func plannerAddRollup(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &newsFlags{}
	var bucket string
	var write bool
	command := &cobra.Command{
		Use:   "add-rollup PLAN",
		Short: "A task for each month's Message Center posts, summing them all up, in one bucket.",
		Long: "A task for each month's Message Center posts, summing them all up, in one bucket: the months the last 7 days " +
			"fall in by default.\n\nA month's rollup lists every post last changed in that month (the whole month, not only the " +
			"days asked for) and counts them by severity, service and category. Its title is Message Center rollup: 2026-09 and " +
			"how many posts. A month the plan has a rollup for already (in any bucket, done or not) has it brought up to date " +
			"instead, its progress left as it is. Without --write this only says what it would do. Needs what add-news does. " +
			"Exits 3 when a rollup is to raise or to update.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if bucket == "" {
				return Usagef("--bucket", "--bucket is required")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			start, end, _, err := flags.window(rt, "7d")
			if err != nil {
				return err
			}
			if start.IsZero() {
				return Usagef("--date", "give the first day, as in --date 2026-06-01..")
			}
			if end.IsZero() {
				end = rt.Clock()
			}
			into, err := openTarget(rt, common, args[0], bucket)
			if err != nil {
				return err
			}
			return addRollup(rt, output, into, flags.query(start, end), start, end, write)
		},
	}
	command.Flags().StringVar(&bucket, "bucket", "", "The bucket new rollups go in.")
	flags.add(command)
	command.Flags().BoolVar(&write, "write", false, "Raise and update the rollups, rather than only say which.")
	common.AddOutput(command, true)
	return command
}

var rollupToDo = []string{"to raise", "to update"}

func addRollup(rt *Runtime, output render.Output, into target, query news.Query, start, end time.Time, write bool) error {
	var rollups []news.Rollup
	for _, month := range news.Months(start, end) {
		monthQuery := query
		monthQuery.Since, monthQuery.Until = month.First, month.After
		found, err := into.posts.Messages(rt.Ctx(), monthQuery)
		if err != nil {
			return err
		}
		if len(found) > 0 {
			rollups = append(rollups, news.Rollup{Month: month.Name, Messages: found})
		}
	}
	tasks, err := into.client.Tasks(rt.Ctx(), into.plan.ID)
	if err != nil {
		return err
	}
	existing := planner.Keyed(tasks, news.RollupTitle)
	rows := make([][]render.Cell, len(rollups))
	records := []any{}
	var states []string
	for index, rollup := range rollups {
		task, has := existing[rollup.Month]
		var current *planner.Task
		if has {
			current = &task
		}
		state, fresh, err := roll(rt, into, rollup, current, write)
		if err != nil {
			return err
		}
		colour := "green"
		if slices.Contains(rollupToDo, state) {
			colour = "yellow"
		}
		states = append(states, state)
		rows[index] = []render.Cell{render.Plain(rollup.Month), render.Plain(strconv.Itoa(len(rollup.Messages))), render.Plain(strconv.Itoa(fresh)),
			render.Coloured(state, colour)}
		records = append(records, map[string]any{"month": rollup.Month, "posts": len(rollup.Messages), "new": fresh, "task": state})
	}
	if err := rt.Console.Emit(output, []string{"MONTH", "POSTS", "NEW", "TASK"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%s", rollupSummary(states, into.where(), write))
	for _, state := range states {
		if slices.Contains(rollupToDo, state) {
			return Attention
		}
	}
	return nil
}

// roll is what happened to one month's rollup, and how many of its posts it did not list.
func roll(rt *Runtime, into target, rollup news.Rollup, task *planner.Task, write bool) (string, int, error) {
	if task == nil {
		if write {
			if _, err := into.client.CreateTask(rt.Ctx(), into.plan.ID, into.bucket.ID, rollup.Title(), rollup.Description()); err != nil {
				return "", 0, err
			}
			return "raised", len(rollup.Messages), nil
		}
		return "to raise", len(rollup.Messages), nil
	}
	current, err := into.client.Description(rt.Ctx(), task.ID)
	if err != nil {
		return "", 0, err
	}
	listed := news.Listed(current)
	fresh := 0
	for id := range rollup.IDs() {
		if !listed[id] {
			fresh++
		}
	}
	rollup = rollup.Keeping(current)
	if task.Title == rollup.Title() && strings.TrimSpace(current) == strings.TrimSpace(rollup.Description()) {
		return "up to date", fresh, nil
	}
	if !write {
		return "to update", fresh, nil
	}
	if err := into.client.UpdateTask(rt.Ctx(), *task, rollup.Title(), rollup.Description()); err != nil {
		return "", 0, err
	}
	return "updated", fresh, nil
}

func rollupSummary(states []string, where string, write bool) string {
	counts := map[string]int{}
	for _, state := range states {
		counts[state]++
	}
	switch {
	case len(states) == 0:
		return "no posts in those months, so no rollup for " + where
	case write:
		return fmt.Sprintf("raised %d and updated %d rollup(s) in %s; %d up to date", counts["raised"], counts["updated"], where, counts["up to date"])
	}
	return fmt.Sprintf("%d to raise and %d to update in %s, %d up to date (--write makes the changes)", counts["to raise"], counts["to update"],
		where, counts["up to date"])
}
