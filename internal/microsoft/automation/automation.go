// Package automation reads Azure Automation through ARM: accounts, the jobs their
// runbooks ran, and the jobs' logs.
//
// A job is one run of a runbook. It writes streams, as the portal's job page shows them:
// output, warnings, errors, and verbose, progress and debug records when the runbook
// turns those on. Jobs are kept for 30 days. Everything here reads: nothing starts, stops
// or changes a runbook. ARM tokens carry no scopes to check: Azure RBAC decides (Reader is
// enough to read jobs).
package automation

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// APIVersion is the Automation API version read.
const APIVersion = "2023-11-01"

// Provider is an Automation account's resource type.
const Provider = "Microsoft.Automation/automationAccounts"

// What each part of a path may hold, so nothing typed can reach another resource. A job
// started by hand is a GUID; one a schedule started is SCH_, the schedule and runbook GUIDs
// and a timestamp, joined by underscores (96 characters).
var (
	accountName = regexp.MustCompile(`^[A-Za-z0-9-]{1,50}$`)
	groupName   = regexp.MustCompile(`^[A-Za-z0-9._()-]{1,90}$`)
	jobID       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	streamID    = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,128}$`)
)

// Failed are the job states that need someone to look: the runbook did not finish as it
// should.
var Failed = []string{"Failed", "Suspended", "Stopped", "Blocked"}

// Streams are the streams a job writes, as the portal's tabs name them, and as ARM spells
// them.
var Streams = [][2]string{{"output", "Output"}, {"error", "Error"}, {"warning", "Warning"}, {"verbose", "Verbose"},
	{"progress", "Progress"}, {"debug", "Debug"}}

// Account is an Automation account, and where it lives.
type Account struct {
	ID             string
	Name           string
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Raw            fields.Object
}

// AccountFrom is an account as ARM returns it; its subscription and resource group come
// from its id.
func AccountFrom(data fields.Object) Account {
	account := Account{ID: fields.Text(data, "id"), Name: fields.Text(data, "name"), Location: fields.Text(data, "location"), Raw: data}
	if parsed, ok := microsoft.TryParseResourceID(account.ID); ok {
		account.SubscriptionID, account.ResourceGroup = parsed.Subscription, parsed.ResourceGroup
	}
	return account
}

// Job is one run of a runbook.
type Job struct {
	ID        string
	Runbook   string
	Status    string
	Created   time.Time
	Started   time.Time
	Ended     time.Time
	RunOn     string
	StartedBy string
	Exception string
	Raw       fields.Object
}

// JobFrom is a job as ARM lists it. The job's resource name is its id: the one the
// portal shows. RunOn is empty for Azure's own sandboxes, a Hybrid Runbook Worker group's
// name otherwise.
func JobFrom(data fields.Object) Job {
	properties := fields.Map(data["properties"])
	id := fields.Text(data, "name")
	if id == "" {
		id = fields.Text(properties, "jobId")
	}
	return Job{ID: id, Runbook: fields.Text(fields.Map(properties["runbook"]), "name"), Status: fields.Text(properties, "status"),
		Created: fields.When(properties, "creationTime"), Started: fields.When(properties, "startTime"),
		Ended: fields.When(properties, "endTime"), RunOn: fields.Text(properties, "runOn"),
		StartedBy: fields.Text(properties, "startedBy"), Exception: fields.Text(properties, "exception"), Raw: data}
}

// IsFailed reports whether the job failed, was stopped or was suspended.
func (j Job) IsFailed() bool { return slices.Contains(Failed, j.Status) }

// Stream is one record a job wrote: a line of output, a warning, an error and so on.
// Only a single stream's GET carries the full Text; a listing has the Summary.
type Stream struct {
	ID      string
	Time    time.Time
	Stream  string
	Summary string
	Text    string
	Raw     fields.Object
}

// StreamFrom is a job stream record.
func StreamFrom(data fields.Object) Stream {
	properties := fields.Map(data["properties"])
	return Stream{ID: fields.Text(properties, "jobStreamId"), Time: fields.When(properties, "time"),
		Stream: fields.Text(properties, "streamType"), Summary: fields.Text(properties, "summary"),
		Text: fields.Text(properties, "streamText"), Raw: data}
}

// Message is the record's full text when it was read singly, else its summary.
func (s Stream) Message() string {
	if s.Text != "" {
		return s.Text
	}
	return s.Summary
}

// Client reads Automation accounts and their jobs through ARM.
type Client struct {
	API *httpx.Client
}

// New is an Automation client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.ARM("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

var apiVersion = url.Values{"api-version": {APIVersion}}

// Accounts is every Automation account in the subscriptions, by name.
func (c *Client) Accounts(ctx context.Context, subscriptions []string) ([]Account, error) {
	var found []Account
	for _, subscription := range subscriptions {
		id, err := util.RequireGUID(subscription, "a subscription id")
		if err != nil {
			return nil, err
		}
		items, err := httpx.Collect(c.API.All(ctx, "/subscriptions/"+id+"/providers/"+Provider, apiVersion, "nextLink"))
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			found = append(found, AccountFrom(item))
		}
	}
	sort.SliceStable(found, func(a, b int) bool {
		nameA, nameB := strings.ToLower(found[a].Name), strings.ToLower(found[b].Name)
		if nameA != nameB {
			return nameA < nameB
		}
		return found[a].ID < found[b].ID
	})
	return found, nil
}

// FindAccount is one account, by its resource id, or by name within subscriptions. A
// name several accounts share is refused, with their ids to choose from.
func (c *Client) FindAccount(ctx context.Context, ref string, subscriptions []string, resourceGroup string) (Account, error) {
	ref = strings.TrimSpace(ref)
	if microsoft.LooksLikeResourceID(ref) {
		path, err := accountPath(ref)
		if err != nil {
			return Account{}, err
		}
		data, err := c.API.Get(ctx, path, apiVersion)
		if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
			return Account{}, errs.NotFoundf("no Automation account %s", ref)
		}
		if err != nil {
			return Account{}, err
		}
		return AccountFrom(data), nil
	}
	name, err := checked(ref, accountName, "an Automation account name")
	if err != nil {
		return Account{}, err
	}
	all, err := c.Accounts(ctx, subscriptions)
	if err != nil {
		return Account{}, err
	}
	var matches []Account
	for _, account := range all {
		if strings.EqualFold(account.Name, name) && (resourceGroup == "" || strings.EqualFold(account.ResourceGroup, resourceGroup)) {
			matches = append(matches, account)
		}
	}
	switch len(matches) {
	case 0:
		where := ""
		if resourceGroup != "" {
			where = " in resource group " + resourceGroup
		}
		return Account{}, errs.NotFoundf("no Automation account is named '%s'%s", name, where).
			WithHint("check the subscription (-s), or list them: 'azure automation accounts'")
	case 1:
		return matches[0], nil
	}
	var ids []string
	for _, account := range matches {
		ids = append(ids, account.ID)
	}
	return Account{}, errs.Ambiguousf("%d Automation accounts are named '%s'", len(matches), name).
		WithHint("pass -g for its resource group, or its resource id: %s", strings.Join(ids, ", "))
}

// Jobs is every job the account keeps (30 days of them), newest first.
func (c *Client) Jobs(ctx context.Context, account Account) ([]Job, error) {
	path, err := accountPath(account.ID)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, path+"/jobs", apiVersion, "nextLink"))
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, len(items))
	for index, item := range items {
		jobs[index] = JobFrom(item)
	}
	when := func(job Job) time.Time {
		if job.Created.IsZero() {
			return job.Started
		}
		return job.Created
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		whenA, whenB := when(jobs[a]), when(jobs[b])
		if whenA.IsZero() != whenB.IsZero() {
			return !whenA.IsZero()
		}
		return whenA.After(whenB)
	})
	return jobs, nil
}

func (c *Client) jobPath(account Account, job string) (string, error) {
	path, err := accountPath(account.ID)
	if err != nil {
		return "", err
	}
	id, err := checked(strings.TrimSpace(job), jobID, "a job id")
	if err != nil {
		return "", err
	}
	return path + "/jobs/" + id, nil
}

// Job is one job, with what only a single job's GET gives: who started it, and why it
// failed.
func (c *Client) Job(ctx context.Context, account Account, job string) (Job, error) {
	path, err := c.jobPath(account, job)
	if err != nil {
		return Job{}, err
	}
	data, err := c.API.Get(ctx, path, apiVersion)
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return Job{}, errs.NotFoundf("no job %s in %s", job, account.Name)
	}
	if err != nil {
		return Job{}, err
	}
	return JobFrom(data), nil
}

// Streams is every record the job wrote, oldest first, each with its summary.
func (c *Client) Streams(ctx context.Context, account Account, job string) ([]Stream, error) {
	path, err := c.jobPath(account, job)
	if err != nil {
		return nil, err
	}
	items, err := httpx.Collect(c.API.All(ctx, path+"/streams", apiVersion, "nextLink"))
	if err != nil {
		return nil, err
	}
	streams := make([]Stream, len(items))
	for index, item := range items {
		streams[index] = StreamFrom(item)
	}
	sort.SliceStable(streams, func(a, b int) bool {
		if streams[a].Time.IsZero() != streams[b].Time.IsZero() {
			return !streams[a].Time.IsZero()
		}
		return streams[a].Time.Before(streams[b].Time)
	})
	return streams, nil
}

// Stream is one record in full: a listing carries only its summary.
func (c *Client) Stream(ctx context.Context, account Account, job, stream string) (Stream, error) {
	if !streamID.MatchString(stream) {
		return Stream{}, errs.Inputf("'%s' is not a job stream id", stream)
	}
	path, err := c.jobPath(account, job)
	if err != nil {
		return Stream{}, err
	}
	data, err := c.API.Get(ctx, path+"/streams/"+url.PathEscape(stream), apiVersion)
	if err != nil {
		return Stream{}, err
	}
	return StreamFrom(data), nil
}

// Output is the job's output stream as text, as the portal's Output tab shows it.
func (c *Client) Output(ctx context.Context, account Account, job string) (string, error) {
	path, err := c.jobPath(account, job)
	if err != nil {
		return "", err
	}
	return c.API.Text(ctx, path+"/output", httpx.Call{Params: apiVersion, Headers: map[string]string{"Accept": "text/plain"}})
}

// accountPath is an Automation account's ARM path, from its resource id, each part checked.
func accountPath(resourceID string) (string, error) {
	found, err := microsoft.ParseResourceID(resourceID)
	if err != nil {
		return "", err
	}
	if !found.IsType(Provider) || found.ResourceGroup == "" || found.Parent != nil {
		kind := found.Type()
		if kind == "" {
			kind = "tenant"
		}
		return "", errs.Inputf("'%s' is not an Automation account's resource id", resourceID).WithHint("it is the resource id of a %s", kind)
	}
	group, err := checked(found.ResourceGroup, groupName, "a resource group name")
	if err != nil {
		return "", err
	}
	name, err := checked(found.Name(), accountName, "an Automation account name")
	if err != nil {
		return "", err
	}
	return "/subscriptions/" + found.Subscription + "/resourceGroups/" + group + "/providers/" + Provider + "/" + name, nil
}

func checked(value string, pattern *regexp.Regexp, what string) (string, error) {
	if !pattern.MatchString(value) {
		return "", errs.Inputf("'%s' is not %s", value, what)
	}
	return value, nil
}
