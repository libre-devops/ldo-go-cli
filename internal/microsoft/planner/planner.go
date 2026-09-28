// Package planner reads Microsoft Planner through Graph: plans, buckets and tasks, and
// creates or updates a task. Creating a task and changing one's title and description are
// the only changes this makes, and only when asked: a command offers them behind an
// explicit flag, after showing what it would do. It covers basic plans (not premium ones).
package planner

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what Planner needs from a Graph token: the Azure CLI's
// Group.ReadWrite.All reads group plans, and a personal plan's tasks are made with
// Tasks.ReadWrite.
var Requirements = []microsoft.Requirement{
	{Feature: "planner plans and tasks", Resource: "graph", AllOf: [][]string{{"Tasks.Read", "Tasks.ReadWrite", "Group.Read.All", "Group.ReadWrite.All"}}},
	{Feature: "planner tasks created", Resource: "graph", AllOf: [][]string{{"Tasks.ReadWrite", "Group.ReadWrite.All"}}},
}

// Planner's ids are 28 characters of letters, digits, _ and -.
var plannerID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// TitleLimit is the longest title Planner takes.
const TitleLimit = 255

// Plan is one plan: a board of buckets and tasks.
type Plan struct {
	ID      string
	Title   string
	Owner   string
	Created time.Time
}

// Bucket is one column of a plan's board.
type Bucket struct {
	ID     string
	Name   string
	PlanID string
}

// Task is one task: its title, bucket, progress and dates. ETag is which version of the
// task this is: Planner changes one only with it.
type Task struct {
	ID              string
	Title           string
	PlanID          string
	BucketID        string
	PercentComplete int
	Due             time.Time
	Created         time.Time
	Completed       time.Time
	ETag            string
}

// Done reports whether the task is complete.
func (t Task) Done() bool { return t.PercentComplete >= 100 }

// TaskFrom is a task from Graph's JSON.
func TaskFrom(data fields.Object) Task {
	percent, _ := fields.Number(data["percentComplete"])
	return Task{ID: fields.Text(data, "id"), Title: fields.Text(data, "title"), PlanID: fields.Text(data, "planId"),
		BucketID: fields.Text(data, "bucketId"), PercentComplete: int(percent), Due: fields.When(data, "dueDateTime"),
		Created: fields.When(data, "createdDateTime"), Completed: fields.When(data, "completedDateTime"), ETag: fields.Text(data, "@odata.etag")}
}

// Client reads (and, when asked, writes) Planner.
type Client struct {
	API *httpx.Client
}

// New is a Planner client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("Planner (Microsoft Graph)")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

func checkedID(value string) (string, error) {
	id := strings.TrimSpace(value)
	if !plannerID.MatchString(id) {
		return "", errs.Inputf("'%s' is not a Planner id", value)
	}
	return id, nil
}

// Plans is the plans shared with the signed-in user.
func (c *Client) Plans(ctx context.Context) ([]Plan, error) {
	items, err := httpx.Collect(c.API.All(ctx, "/v1.0/me/planner/plans", nil, ""))
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, len(items))
	for index, item := range items {
		plans[index] = Plan{ID: fields.Text(item, "id"), Title: fields.Text(item, "title"), Owner: fields.Text(item, "owner"),
			Created: fields.When(item, "createdDateTime")}
	}
	return plans, nil
}

// Plan is a plan by its id or its title (ignoring case), among the user's.
func (c *Client) Plan(ctx context.Context, ref string) (Plan, error) {
	wanted := strings.TrimSpace(ref)
	plans, err := c.Plans(ctx)
	if err != nil {
		return Plan{}, err
	}
	var found []Plan
	var titles []string
	for _, plan := range plans {
		titles = append(titles, plan.Title)
		if plan.ID == wanted || strings.EqualFold(plan.Title, wanted) {
			found = append(found, plan)
		}
	}
	switch len(found) {
	case 0:
		there := strings.Join(titles, ", ")
		if there == "" {
			there = "none"
		}
		return Plan{}, errs.NotFoundf("no plan '%s' among yours", ref).WithHint("there are: %s", there)
	case 1:
		return found[0], nil
	}
	return Plan{}, errs.Ambiguousf("%d plans are called '%s'", len(found), ref).WithHint("give its id")
}

// Buckets is the plan's buckets.
func (c *Client) Buckets(ctx context.Context, planID string) ([]Bucket, error) {
	id, err := checkedID(planID)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, "/v1.0/planner/plans/"+id+"/buckets", nil, ""))
	if err != nil {
		return nil, err
	}
	buckets := make([]Bucket, len(items))
	for index, item := range items {
		buckets[index] = Bucket{ID: fields.Text(item, "id"), Name: fields.Text(item, "name"), PlanID: fields.Text(item, "planId")}
	}
	return buckets, nil
}

// Bucket is the plan's bucket called name (ignoring case).
func (c *Client) Bucket(ctx context.Context, planID, name string) (Bucket, error) {
	buckets, err := c.Buckets(ctx, planID)
	if err != nil {
		return Bucket{}, err
	}
	var names []string
	for _, bucket := range buckets {
		if strings.EqualFold(bucket.Name, strings.TrimSpace(name)) {
			return bucket, nil
		}
		names = append(names, bucket.Name)
	}
	there := strings.Join(names, ", ")
	if there == "" {
		there = "none"
	}
	return Bucket{}, errs.NotFoundf("no bucket '%s' in the plan", name).WithHint("there are: %s", there)
}

// Tasks is every task in the plan, done or not.
func (c *Client) Tasks(ctx context.Context, planID string) ([]Task, error) {
	id, err := checkedID(planID)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, "/v1.0/planner/plans/"+id+"/tasks", nil, ""))
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, len(items))
	for index, item := range items {
		tasks[index] = TaskFrom(item)
	}
	return tasks, nil
}

func cut(title string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > TitleLimit {
		return string(runes[:TitleLimit])
	}
	return string(runes)
}

// CreateTask is a new task in the bucket, with description in its details when given.
func (c *Client) CreateTask(ctx context.Context, planID, bucketID, title, description string) (Task, error) {
	if strings.TrimSpace(title) == "" {
		return Task{}, errs.Inputf("a task needs a title")
	}
	plan, err := checkedID(planID)
	if err != nil {
		return Task{}, err
	}
	bucket, err := checkedID(bucketID)
	if err != nil {
		return Task{}, err
	}
	data, err := c.API.Post(ctx, "/v1.0/planner/tasks", map[string]any{"planId": plan, "bucketId": bucket, "title": cut(title)}, nil)
	if err != nil {
		return Task{}, err
	}
	task := TaskFrom(data)
	if description != "" {
		if err := c.describe(ctx, task.ID, description); err != nil {
			return task, err
		}
	}
	return task, nil
}

func detailsPath(taskID string) (string, error) {
	id, err := checkedID(taskID)
	if err != nil {
		return "", err
	}
	return "/v1.0/planner/tasks/" + id + "/details", nil
}

// Description is the task's description: the notes its details hold.
func (c *Client) Description(ctx context.Context, taskID string) (string, error) {
	path, err := detailsPath(taskID)
	if err != nil {
		return "", err
	}
	data, err := c.API.Get(ctx, path, nil)
	return fields.Text(data, "description"), err
}

// UpdateTask gives task this title and description; Planner refuses when the task has
// changed since it was read (its ETag).
func (c *Client) UpdateTask(ctx context.Context, task Task, title, description string) error {
	if strings.TrimSpace(title) == "" {
		return errs.Inputf("a task needs a title")
	}
	id, err := checkedID(task.ID)
	if err != nil {
		return err
	}
	if _, err := c.API.Do(ctx, http.MethodPatch, "/v1.0/planner/tasks/"+id, httpx.Call{Headers: map[string]string{"If-Match": task.ETag},
		JSON: map[string]any{"title": cut(title)}, AllowEmpty: true}); err != nil {
		return err
	}
	return c.describe(ctx, task.ID, description)
}

// describe sets a task's description. Planner changes a task's details only with their
// current etag, which a new task's details have from the moment it is made.
func (c *Client) describe(ctx context.Context, taskID, description string) error {
	path, err := detailsPath(taskID)
	if err != nil {
		return err
	}
	current, err := c.API.Get(ctx, path, nil)
	if err != nil {
		return err
	}
	_, err = c.API.Do(ctx, http.MethodPatch, path, httpx.Call{Headers: map[string]string{"If-Match": fields.Text(current, "@odata.etag")},
		JSON: map[string]any{"description": description, "previewType": "description"}, AllowEmpty: true})
	return err
}

// Keyed is the tasks whose title holds a key key finds (its first group, when it has
// one), by that key: how a plan says which things it has a task for already.
func Keyed(tasks []Task, key *regexp.Regexp) map[string]Task {
	found := map[string]Task{}
	for _, task := range tasks {
		match := key.FindStringSubmatch(strings.TrimSpace(task.Title))
		if match == nil {
			continue
		}
		value := match[0]
		if len(match) > 1 {
			value = match[1]
		}
		value = strings.ToUpper(value)
		if _, seen := found[value]; !seen {
			found[value] = task
		}
	}
	return found
}
