package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/loganalytics"
)

// logsCommand is the logs group.
func logsCommand(rt *Runtime) *cobra.Command {
	group := newGroup("logs", "Log Analytics and Sentinel workspaces: run KQL, and see which tables are receiving data.")
	group.AddCommand(logsQuery(rt), logsIngestion(rt))
	return group
}

const workspaceHelp = "The workspace: its Workspace ID (the GUID on its Overview page), its resource id " +
	"(/subscriptions/.../workspaces/NAME), or its name. A resource id or a name is looked up in Resource Manager, which " +
	"needs Reader on it. Default: the profile's workspace."

// workspaceID is the Workspace ID to ask: --workspace's, else the profile's. A resource
// id or a name is looked up, and what it named is noted, so it is plain which workspace
// was read.
func workspaceID(rt *Runtime, profile microsoft.Profile, given string) (string, error) {
	named := given
	if named == "" {
		named = profile.Workspace
	}
	if named == "" {
		named = profile.WorkspaceID
	}
	if named == "" {
		return "", errs.Configf("no workspace given").WithHint("pass --workspace, or set workspace on the profile")
	}
	ref, err := microsoft.WorkspaceRefOf(named)
	if err != nil {
		return "", err
	}
	if ref.Kind == microsoft.WorkspaceID {
		return ref.Value, nil
	}
	client, err := azureClient(rt, profile)
	if err != nil {
		return "", err
	}
	found, err := client.FindWorkspace(rt.Ctx(), ref, nil)
	if err != nil {
		return "", err
	}
	rt.Console.Note("workspace %s in %s, from its %s: Workspace ID %s", found.Name, or(found.ResourceGroup, "-"), ref.Kind, found.WorkspaceID)
	return found.WorkspaceID, nil
}

func logsClient(rt *Runtime, profile microsoft.Profile) (*loganalytics.Client, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	return loganalytics.New(api)
}

func logsQuery(rt *Runtime) *cobra.Command {
	common := &Common{}
	var file, workspace, timespan string
	command := &cobra.Command{
		Use:   "query [QUERY]",
		Short: "Run a KQL query against a Log Analytics (or Sentinel) workspace.",
		Long:  "Run a KQL query against a Log Analytics (or Sentinel) workspace. Omit the query, or pass -, to read it from stdin.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			kql, err := readQuery(rt, query, file)
			if err != nil {
				return err
			}
			span, err := optionalDuration("--timespan", timespan)
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
			id, err := workspaceID(rt, profile, workspace)
			if err != nil {
				return err
			}
			client, err := logsClient(rt, profile)
			if err != nil {
				return err
			}
			result, err := client.Query(rt.Ctx(), id, kql, time.Duration(span))
			if err != nil {
				return err
			}
			if err := rt.Console.Query(result, output); err != nil {
				return err
			}
			rt.Console.Note("%d row(s)", len(result.Rows))
			return nil
		},
	}
	command.Flags().StringVar(&file, "file", "", "Read the query from a file.")
	command.Flags().StringVarP(&workspace, "workspace", "w", "", workspaceHelp)
	command.Flags().StringVar(&timespan, "timespan", "", "Bound the query to this long ago, e.g. 24h, 7d.")
	common.AddOutput(command, true)
	return command
}

func logsIngestion(rt *Runtime) *cobra.Command {
	common := &Common{}
	var workspace, window, quietAfter string
	command := &cobra.Command{
		Use:   "ingestion",
		Short: "Which tables the workspace is receiving: when each last got data, and how much.",
		Long: "Which tables the workspace is receiving: when each last got data, and how much.\n\n" +
			"Read from the Usage table, so it is cheap and accurate to the hour; a table that got nothing in the whole " +
			"window is not listed, since nothing says it exists. Quiet tables come first. Exits 3 when any table has been " +
			"quiet for longer than --quiet-after.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			span, err := Duration("--window", window)
			if err != nil {
				return err
			}
			after, err := Duration("--quiet-after", quietAfter)
			if err != nil {
				return err
			}
			query, err := loganalytics.IngestionQuery(span)
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
			id, err := workspaceID(rt, profile, workspace)
			if err != nil {
				return err
			}
			client, err := logsClient(rt, profile)
			if err != nil {
				return err
			}
			result, err := client.Query(rt.Ctx(), id, query, 0)
			if err != nil {
				return err
			}
			return showIngestion(rt, output, result, id, span, after)
		},
	}
	command.Flags().StringVarP(&workspace, "workspace", "w", "", workspaceHelp)
	command.Flags().StringVar(&window, "window", "30d", "How far back to look, e.g. 7d, 90d.")
	command.Flags().StringVar(&quietAfter, "quiet-after", "24h", "Flag a table that received nothing for this long.")
	common.AddOutput(command, true)
	return command
}

func showIngestion(rt *Runtime, output render.Output, result render.QueryResult, id string, span, after time.Duration) error {
	now := rt.Clock()
	tables := loganalytics.ByQuietest(loganalytics.ReadIngestion(result), now, after)
	rows := make([][]render.Cell, len(tables))
	records := []any{}
	quiet := 0
	var total, billable float64
	for index, table := range tables {
		silence, known := table.QuietFor(now)
		shown := "-"
		var seconds any
		if known {
			shown = util.FormatDuration(silence)
			seconds = int64(silence / time.Second)
		}
		cell := render.Plain(shown)
		if table.Quiet(now, after) {
			cell = render.Coloured(shown, "red")
			quiet++
		}
		total += table.Gigabytes
		billable += table.BillableGigabytes
		rows[index] = []render.Cell{render.Plain(table.Table), render.Plain(render.When(table.LastData, now)), cell,
			render.Plain(util.Grouped(table.Gigabytes, 3)), render.Plain(util.Grouped(table.BillableGigabytes, 3)),
			render.Plain(strings.Join(table.Solutions, ", "))}
		records = append(records, map[string]any{"table": table.Table, "last_data": render.ISO(table.LastData), "quiet_seconds": seconds,
			"quiet": table.Quiet(now, after), "gigabytes": round6(table.Gigabytes), "billable_gigabytes": round6(table.BillableGigabytes),
			"solutions": nonNil(table.Solutions)})
	}
	if err := rt.Console.Emit(output, []string{"TABLE", "LAST DATA", "QUIET FOR", "GB", "BILLABLE GB", "SOLUTIONS"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%d table(s) received data in the last %s: %s GB, %s GB billable (workspace %s)", len(tables), util.FormatSpan(span),
		util.Grouped(total, 2), util.Grouped(billable, 2), id)
	for _, warning := range result.Warnings {
		rt.Console.Warn("%s", warning)
	}
	if quiet > 0 {
		rt.Console.Warn("%d table(s) quiet for over %s", quiet, util.FormatSpan(after))
		return Attention
	}
	return nil
}

func round6(value float64) float64 {
	const scale = 1e6
	rounded := value * scale
	if rounded < 0 {
		return float64(int64(rounded-0.5)) / scale
	}
	return float64(int64(rounded+0.5)) / scale
}
