package cli

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/atlassian/jira"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

var (
	jiraProject     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,254}$`)
	categoryColours = map[string]string{"Done": "green", "In Progress": "yellow"}
)

func jiraCommand(rt *Runtime) *cobra.Command {
	group := newGroup("jira", "Jira Cloud: issues by JQL, one issue with its description, and projects.")
	group.AddCommand(jiraWhoamiCommand(rt), jiraIssuesCommand(rt), jiraIssueCommand(rt), jiraProjectsCommand(rt))
	return group
}

// atlassianOutput gives cmd -o and the Atlassian -p; a list also gets --sort and --unique.
func atlassianOutput(cmd *cobra.Command, common *Common, list bool) {
	common.AddOutput(cmd, list)
	cmd.Flags().Lookup("profile").Usage = "Atlassian profile. Default: $" + brand.EnvVar("ATLASSIAN_PROFILE") +
		", else default_profile, else the one from the environment."
}

// withJira is the chosen profile and a Jira client for it.
func withJira(rt *Runtime, name string) (atlassian.Profile, *jira.Client, error) {
	profile, err := rt.AtlassianProfile(name)
	if err != nil {
		return profile, nil, err
	}
	client, err := rt.Jira(profile)
	return profile, client, err
}

func jiraWhoamiCommand(rt *Runtime) *cobra.Command {
	var common Common
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Who the profile's API token reads Jira as, and on which site.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			profile, client, err := withJira(rt, common.Profile)
			if err != nil {
				return err
			}
			me, err := client.Whoami(rt.Ctx())
			if err != nil {
				return err
			}
			email := fields.Text(me, "emailAddress")
			if email == "" {
				email = profile.Email
			}
			record := map[string]any{"profile": profile.Name, "site": profile.Site, "account_id": me["accountId"],
				"name": me["displayName"], "email": email, "active": me["active"], "time_zone": me["timeZone"]}
			active, known := me["active"].(bool)
			shownActive := "-"
			if known {
				shownActive = yesNo(active)
			}
			items := [][2]string{{"Profile", profile.Name}, {"Site", profile.Site}, {"Name", fields.Text(me, "displayName")},
				{"Email", email}, {"Account id", fields.Text(me, "accountId")}, {"Active", shownActive}}
			return showPairs(rt, output, items, record)
		},
	}
	atlassianOutput(command, &common, false)
	return command
}

// showPairs is label and value lines for a person, else one row headed by the labels.
func showPairs(rt *Runtime, output render.Output, items [][2]string, record any) error {
	if output == render.Table {
		rt.Console.Println(pairs(rt, items))
		return nil
	}
	headers := make([]string, len(items))
	row := make([]render.Cell, len(items))
	for index, item := range items {
		headers[index], row[index] = strings.ToUpper(item[0]), render.Plain(item[1])
	}
	return rt.Console.Emit(output, headers, [][]render.Cell{row}, record)
}

func jiraIssuesCommand(rt *Runtime) *cobra.Command {
	var common Common
	var project string
	var limit int
	command := &cobra.Command{
		Use:   "issues [JQL]",
		Short: "Issues a JQL query finds, as Jira orders them.",
		Long: "Issues a JQL query finds, as Jira orders them.\n\n" +
			"Jira refuses a query with no restriction at all, so give one (a project, a status, a date): without a query, " +
			"it is the issues not done.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			jql := ""
			if len(args) == 1 {
				jql = args[0]
			}
			query, err := jiraQuery(jql, project)
			if err != nil {
				return err
			}
			profile, client, err := withJira(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Issues(rt.Ctx(), query, limit)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := make([]any, len(found))
			for index, issue := range found {
				rows[index] = issueRow(rt, issue)
				records[index] = issueRecord(issue)
			}
			if err := rt.Console.Emit(output, []string{"KEY", "TYPE", "STATUS", "PRIORITY", "ASSIGNEE", "UPDATED", "SUMMARY"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d issue(s) (profile %s)", len(found), profile.Name)
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", "", "Only this project's issues not done (instead of a query).")
	command.Flags().IntVarP(&limit, "limit", "n", 50, "How many to show.")
	atlassianOutput(command, &common, true)
	return command
}

func jiraQuery(jql, project string) (string, error) {
	if strings.TrimSpace(jql) != "" && project != "" {
		return "", Usagef("--project", "put the project in the query instead")
	}
	if project != "" {
		if !jiraProject.MatchString(strings.TrimSpace(project)) {
			return "", Usagef("--project", "%q is not a project key", project)
		}
		return "project = " + strings.ToUpper(strings.TrimSpace(project)) + " AND " + jira.DefaultJQL, nil
	}
	if strings.TrimSpace(jql) != "" {
		return strings.TrimSpace(jql), nil
	}
	return jira.DefaultJQL, nil
}

func issueRow(rt *Runtime, issue jira.Issue) []render.Cell {
	return []render.Cell{render.Plain(issue.Key), render.Plain(issue.Type), render.Coloured(issue.Status, categoryColours[issue.StatusCategory]),
		render.Plain(issue.Priority), render.Plain(or(issue.Assignee, "-")), render.Plain(rt.Console.When(issue.Updated)), render.Plain(issue.Summary)}
}

func issueRecord(issue jira.Issue) map[string]any {
	labels := issue.Labels
	if labels == nil {
		labels = []string{}
	}
	return map[string]any{
		"key": issue.Key, "id": issue.ID, "summary": issue.Summary, "type": issue.Type, "status": issue.Status,
		"status_category": issue.StatusCategory, "priority": orNil(issue.Priority), "assignee": orNil(issue.Assignee),
		"reporter": orNil(issue.Reporter), "project": issue.Project, "labels": labels, "created": render.ISO(issue.Created),
		"updated": render.ISO(issue.Updated), "url": issue.URL,
	}
}

func jiraIssueCommand(rt *Runtime) *cobra.Command {
	var common Common
	var onlyMarkdown bool
	command := &cobra.Command{
		Use:   "issue KEY",
		Short: "One issue: what it is, where it stands, who has it, and its description as Markdown.",
		Long:  "One issue: what it is, where it stands, who has it, and its description as Markdown. KEY is the issue's key, e.g. OPS-123.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			_, client, err := withJira(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Issue(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if onlyMarkdown {
				rt.Console.Println(found.Description)
				return nil
			}
			return showIssue(rt, output, found)
		},
	}
	command.Flags().BoolVar(&onlyMarkdown, "markdown", false, "Only the description, as Markdown.")
	atlassianOutput(command, &common, false)
	return command
}

func showIssue(rt *Runtime, output render.Output, found jira.Issue) error {
	labels := strings.Join(found.Labels, ", ")
	items := [][2]string{
		{"Key", found.Key}, {"Summary", found.Summary}, {"Type", found.Type}, {"Status", found.Status + " (" + found.StatusCategory + ")"},
		{"Priority", or(found.Priority, "-")}, {"Assignee", or(found.Assignee, "unassigned")}, {"Reporter", found.Reporter},
		{"Created", render.Moment(found.Created)}, {"Updated", render.Moment(found.Updated)}, {"Labels", or(labels, "-")}, {"Link", found.URL},
	}
	if output == render.Table {
		rt.Console.Println(pairs(rt, items))
		rt.Console.Println("")
		rt.Console.Println(or(found.Description, "(no description)"))
		return nil
	}
	record := issueRecord(found)
	record["description"] = orNil(found.Description)
	return showPairs(rt, output, append(items, [2]string{"Description", found.Description}), record)
}

func jiraProjectsCommand(rt *Runtime) *cobra.Command {
	var common Common
	command := &cobra.Command{
		Use:   "projects",
		Short: "The projects the account can see.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			_, client, err := withJira(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Projects(rt.Ctx())
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := make([]any, len(found))
			for index, item := range found {
				rows[index] = render.Cells(item.Key, item.Name, item.Type, item.URL)
				records[index] = map[string]any{"key": item.Key, "id": item.ID, "name": item.Name, "type": item.Type, "url": item.URL}
			}
			return rt.Console.Emit(output, []string{"KEY", "NAME", "TYPE", "LINK"}, rows, records)
		},
	}
	atlassianOutput(command, &common, true)
	return command
}
