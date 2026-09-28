package cli

import (
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/markdown"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/rowfilters"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/news"
)

var newsSeverityColours = map[string]string{"high": "yellow", "critical": "red"}

var bareSpan = regexp.MustCompile(`^(?i)\d+[smhd]$`)

// newsCommand is the news group.
func newsCommand(rt *Runtime) *cobra.Command {
	group := newGroup("news", "Microsoft 365 Message Center: what changes in the tenant's services, and when.")
	group.AddCommand(newsMessages(rt), newsMessage(rt))
	return group
}

// newsFlags choose which posts.
type newsFlags struct {
	date, since, category string
	services              []string
	security, major       bool
}

func (f *newsFlags) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.date, "date", "", "Only posts changed then: today, 29/09/2026, 2026-09-01..2026-09-14, last 7d, or 7d.")
	flags.StringVar(&f.since, "since", "", "Only posts changed in this span, e.g. 7d, 36h.")
	flags.StringArrayVar(&f.services, "service", nil, "Only posts for a service whose name holds this, e.g. xdr, Teams. Repeatable.")
	flags.BoolVar(&f.security, "security", false, "Only posts for the security services: Defender, Sentinel, Purview, Entra, Intune.")
	flags.StringVar(&f.category, "category", "", "Only this category: plan for change, stay informed, or prevent or fix issue.")
	flags.BoolVar(&f.major, "major", false, "Only major changes.")
}

// window is the span of change times asked for: --date (a day, a span of them, or a
// length of time), else --since, else fallback; and how to say it.
func (f *newsFlags) window(rt *Runtime, fallback string) (time.Time, time.Time, string, error) {
	if f.date != "" && f.since != "" {
		return time.Time{}, time.Time{}, "", Usagef("--since", "give one of --date and --since")
	}
	spec := strings.TrimSpace(f.date)
	now := rt.Clock().UTC()
	if spec != "" && !bareSpan.MatchString(spec) {
		first, last, err := rowfilters.DaySpan(spec, now)
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		var start, end time.Time
		if !first.IsZero() {
			start = first
		}
		if !last.IsZero() {
			end = last.AddDate(0, 0, 1)
		}
		return start, end, "changed " + spec, nil
	}
	span := spec
	if span == "" {
		span = f.since
	}
	if span == "" {
		span = fallback
	}
	length, err := util.ParseDuration(span)
	if err != nil {
		return time.Time{}, time.Time{}, "", Usagef("--since", "%s", err)
	}
	return now.Add(-length), time.Time{}, "changed in the last " + span, nil
}

// query is the posts the flags name, in the window.
func (f *newsFlags) query(start, end time.Time) news.Query {
	services := append([]string(nil), f.services...)
	if f.security {
		services = append(services, news.SecurityServices...)
	}
	return news.Query{Since: start, Until: end, Services: services, Category: f.category, Major: f.major}
}

func newsClient(rt *Runtime, common *Common) (*news.Client, string, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return nil, "", err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, "", err
	}
	client, err := news.New(api)
	return client, profile.Name, err
}

func newsMessages(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &newsFlags{}
	var limit int
	command := &cobra.Command{
		Use:   "messages",
		Short: "Message Center posts, the most recently changed first: the last 30 days by default.",
		Long: "Message Center posts, the most recently changed first: the last 30 days by default.\n\n" +
			"Needs ServiceMessage.Read.All, which the Azure CLI's token lacks: use a profile with your own app registration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			start, end, said, err := flags.window(rt, "30d")
			if err != nil {
				return err
			}
			client, profile, err := newsClient(rt, common)
			if err != nil {
				return err
			}
			query := flags.query(start, end)
			query.Limit = limit
			found, err := client.Messages(rt.Ctx(), query)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []any{}
			for index, message := range found {
				rows[index] = messageRow(rt, message)
				records = append(records, messageRecord(message))
			}
			if err := rt.Console.Emit(output, []string{"ID", "UPDATED", "CATEGORY", "SEVERITY", "SERVICES", "ACTION BY", "TITLE"}, rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d post(s) %s (profile %s)", len(found), said, profile)
			return nil
		},
	}
	flags.add(command)
	command.Flags().IntVarP(&limit, "limit", "n", 100, "How many to show.")
	common.AddOutput(command, true)
	return command
}

func messageRow(rt *Runtime, message news.Message) []render.Cell {
	title := message.Title
	if message.Major {
		title += " (major)"
	}
	actionBy := "-"
	if !message.ActionBy.IsZero() {
		actionBy = render.Moment(message.ActionBy)
	}
	return []render.Cell{render.Plain(message.ID), render.Plain(rt.Console.When(message.Updated)), render.Plain(message.CategoryLabel()),
		render.Coloured(message.Severity, newsSeverityColours[strings.ToLower(message.Severity)]), render.Plain(strings.Join(message.Services, ", ")),
		render.Plain(actionBy), render.Plain(title)}
}

// messageRecord is a post as -o json gives it.
func messageRecord(message news.Message) map[string]any {
	return map[string]any{"id": message.ID, "title": message.Title, "category": message.Category, "severity": message.Severity,
		"services": nonNil(message.Services), "tags": nonNil(message.Tags), "major": message.Major, "starts": render.ISO(message.Starts),
		"ends": render.ISO(message.Ends), "updated": render.ISO(message.Updated), "action_by": render.ISO(message.ActionBy), "url": message.URL()}
}

func newsMessage(rt *Runtime) *cobra.Command {
	common := &Common{}
	var asMarkdown bool
	command := &cobra.Command{
		Use:   "message ID",
		Short: "One post: what changes, for which services, by when, and all it says, as Markdown.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := newsClient(rt, common)
			if err != nil {
				return err
			}
			found, err := client.Message(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			body := ""
			if found.BodyHTML != "" {
				body = strings.TrimSpace(markdown.FromHTML(found.BodyHTML))
			}
			if asMarkdown {
				rt.Console.Println(body)
				return nil
			}
			return showMessage(rt, output, found, body)
		},
	}
	command.Flags().BoolVar(&asMarkdown, "markdown", false, "Only the post, as Markdown.")
	common.AddOutput(command, false)
	return command
}

func showMessage(rt *Runtime, output render.Output, found news.Message, body string) error {
	items := [][2]string{{"Id", found.ID}, {"Title", found.Title}, {"Category", found.CategoryLabel()}, {"Severity", found.Severity},
		{"Major change", yesNo(found.Major)}, {"Services", or(strings.Join(found.Services, ", "), "-")}, {"Tags", or(strings.Join(found.Tags, ", "), "-")},
		{"Starts", render.Moment(found.Starts)}, {"Action by", render.Moment(found.ActionBy)}, {"Updated", render.Moment(found.Updated)},
		{"Link", found.URL()}}
	if output == render.Table {
		rt.Console.Println(pairs(rt, items))
		rt.Console.Println("")
		rt.Console.Println(or(body, "(no text)"))
		return nil
	}
	var headers []string
	var row []render.Cell
	for _, item := range items {
		headers = append(headers, strings.ToUpper(item[0]))
		row = append(row, render.Plain(item[1]))
	}
	record := messageRecord(found)
	record["body"] = orNil(body)
	return rt.Console.Emit(output, append(headers, "BODY"), [][]render.Cell{append(row, render.Plain(body))}, record)
}
