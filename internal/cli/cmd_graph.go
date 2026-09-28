package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/graph"
)

// Shown first for an object, when it has them; everything else follows.
var graphFirst = []string{"id", "displayName", "userPrincipalName", "mail", "appId", "deviceId",
	"operatingSystem", "accountEnabled", "createdDateTime"}

// graphCommand is the graph group.
func graphCommand(rt *Runtime) *cobra.Command {
	group := newGroup("graph", "Microsoft Graph: whoami, a token, any GET, objects by name, and hunting.")
	group.AddCommand(graphWhoami(rt),
		tokenCommand(rt, "token", "Get a Graph token and check it: which of this tool's features it covers.",
			"Get a Graph token and check it: which of this tool's features it covers.\n\n"+
				"The same as 'entra token graph'. The token itself is printed only with --raw, e.g.\n"+
				`curl -H "Authorization: Bearer $(`+brand.Command+` graph token --raw)" ...`, "graph"),
		graphGet(rt))
	for _, kind := range [][2]string{{"user", "user"}, {"device", "device"}, {"group", "group"},
		{"app", "app registration"}, {"sp", "service principal"}} {
		group.AddCommand(graphLookup(rt, kind[0], kind[1]))
	}
	group.AddCommand(graphHunt(rt))
	return group
}

// graphClient is a Graph client for a profile, by name.
func graphClient(rt *Runtime, name string) (*graph.Client, microsoft.Profile, error) {
	profile, err := rt.Profile(name)
	if err != nil {
		return nil, profile, err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, profile, err
	}
	client, err := graph.New(api)
	return client, profile, err
}

func graphWhoami(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Who the profile's Graph token is for, what it may do, and when it expires.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, profile, err := graphClient(rt, common.Profile)
			if err != nil {
				return err
			}
			target, err := microsoft.ResolveResource("graph", profile.Cloud)
			if err != nil {
				return err
			}
			tokens, err := rt.Tokens(profile)
			if err != nil {
				return err
			}
			access, err := tokens.GetToken(rt.Ctx(), target.URL, profile.TenantID)
			if err != nil {
				return err
			}
			decoded, err := microsoft.DecodeToken(access.Token)
			if err != nil {
				return err
			}
			return showWhoami(rt, client, profile, decoded, output)
		},
	}
	common.AddOutput(command, false)
	return command
}

func showWhoami(rt *Runtime, client *graph.Client, profile microsoft.Profile, decoded microsoft.DecodedToken,
	output render.Output) error {
	kind := decoded.IdentityType()
	var detail fields.Object
	var err error
	what := "app"
	if kind == "user" {
		what = "user"
		detail, err = client.Me(rt.Ctx())
	} else {
		detail, err = client.ServicePrincipal(rt.Ctx(), decoded.AppID())
	}
	if err != nil {
		if errs.As(err) == nil || errs.As(err).Kind != errs.API {
			return err
		}
		rt.Console.Warn("could not read the %s: %v", what, err)
	}
	permissions := decoded.Roles()
	if kind == "user" {
		permissions = decoded.Scopes()
	}
	if output == render.Table {
		rt.Console.Println(pairs(rt, whoamiPairs(rt, profile, decoded, detail)))
		return nil
	}
	row := render.Cells(profile.Name, kind, decoded.Principal(), decoded.TenantID(),
		rt.Console.When(decoded.ExpiresAt()), strings.Join(permissions, " "))
	return rt.Console.Emit(output, []string{"PROFILE", "KIND", "PRINCIPAL", "TENANT", "EXPIRES", "PERMISSIONS"},
		[][]render.Cell{row}, whoamiRecord(profile, decoded, detail))
}

func whoamiRecord(profile microsoft.Profile, decoded microsoft.DecodedToken, detail fields.Object) map[string]any {
	var object any
	if detail != nil {
		object = detail
	}
	return map[string]any{
		"profile": profile.Name, "tenant_id": decoded.TenantID(), "kind": decoded.IdentityType(),
		"principal": decoded.Principal(), "client_app_id": decoded.AppID(),
		"client_app": decoded.Claims["app_displayname"], "expires_at": render.ISO(decoded.ExpiresAt()),
		"scopes": nonNil(decoded.Scopes()), "roles": nonNil(decoded.Roles()), "object": object,
	}
}

// nonNil is values, or an empty list, so JSON has [] rather than null.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// whoamiPairs is who, then the user or app Graph knows them as, then the token's facts.
func whoamiPairs(rt *Runtime, profile microsoft.Profile, decoded microsoft.DecodedToken, detail fields.Object) [][2]string {
	user := decoded.IdentityType() == "user"
	kind := decoded.IdentityType() + " (application)"
	if user {
		kind = "user (delegated)"
	}
	rows := [][2]string{
		{"Profile", profile.Name + " (" + profile.Auth + ")"},
		{"Signed in as", dash(decoded.Principal())},
		{"Kind", kind},
	}
	if len(detail) > 0 {
		rows = append(rows, [2]string{"Name", dash(fields.Text(detail, "displayName"))},
			[2]string{"Object id", dash(fields.Text(detail, "id"))})
		if job := fields.Text(detail, "jobTitle"); job != "" {
			rows = append(rows, [2]string{"Job title", job})
		}
	}
	permissions, label := decoded.Roles(), "Roles"
	if user {
		permissions, label = decoded.Scopes(), "Scopes"
	}
	sorted := slices.Clone(permissions)
	sort.Strings(sorted)
	shown := strings.Join(sorted, ", ")
	if shown == "" {
		shown = "(none)"
	}
	return append(rows,
		[2]string{"Tenant", decoded.TenantID()},
		[2]string{"Client app", dash(claimText(decoded, "app_displayname")) + " (" + decoded.AppID() + ")"},
		[2]string{"Expires", rt.Console.When(decoded.ExpiresAt())},
		[2]string{label, shown},
	)
}

func dash(text string) string {
	if text == "" {
		return "-"
	}
	return text
}

type graphGetFlags struct {
	selectFields, filter, search, orderby, expand string
	top, limit                                    int
	count, eventual, all, beta                    bool
}

func (f *graphGetFlags) params() map[string]string {
	found := map[string]string{"$select": f.selectFields, "$filter": f.filter, "$search": quotedSearch(f.search),
		"$orderby": f.orderby, "$expand": f.expand}
	if f.top > 0 {
		found["$top"] = strconv.Itoa(f.top)
	}
	if f.count {
		found["$count"] = "true"
	}
	return found
}

func graphGet(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &graphGetFlags{}
	command := &cobra.Command{
		Use:   "get PATH",
		Short: "GET anything from Graph, paged. Collections come back as rows, objects as fields.",
		Long: "GET anything from Graph, paged. Collections come back as rows, objects as fields.\n\n" +
			"PATH is a Graph path (users, me/memberOf, beta/...) or a full Graph URL. Without --all or " +
			"--limit, one page is shown, and a note says when there are more.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positive(cmd, "top", flags.top); err != nil {
				return err
			}
			if err := positive(cmd, "limit", flags.limit); err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := graphClient(rt, common.Profile)
			if err != nil {
				return err
			}
			return runGraphGet(rt, client, args[0], flags, output)
		},
	}
	options := command.Flags()
	options.StringVar(&flags.selectFields, "select", "", "Properties to return, e.g. id,displayName ($select).")
	options.StringVar(&flags.filter, "filter", "", "An OData filter ($filter).")
	options.StringVar(&flags.search, "search", "", `A $search, e.g. "displayName:ana" (sets ConsistencyLevel).`)
	options.StringVar(&flags.orderby, "orderby", "", "$orderby.")
	options.StringVar(&flags.expand, "expand", "", "$expand.")
	options.IntVar(&flags.top, "top", 0, "Page size ($top).")
	options.BoolVar(&flags.count, "count", false, "Ask for the total count (sets ConsistencyLevel).")
	options.BoolVar(&flags.eventual, "eventual", false, "Send ConsistencyLevel: eventual, for advanced queries.")
	options.BoolVar(&flags.all, "all", false, "Follow every page.")
	options.IntVarP(&flags.limit, "limit", "n", 0, "Stop after this many items.")
	options.BoolVar(&flags.beta, "beta", false, "Use Graph's beta endpoint.")
	common.AddOutput(command, true)
	return command
}

func runGraphGet(rt *Runtime, client *graph.Client, path string, flags *graphGetFlags, output render.Output) error {
	params := graph.Params()
	for key, value := range flags.params() {
		if value != "" {
			params.Set(key, value)
		}
	}
	advanced := flags.eventual || flags.count || flags.search != ""
	data, err := client.Get(rt.Ctx(), path, params, flags.beta, advanced)
	if err != nil {
		return err
	}
	if _, isList := data["value"].([]any); !isList {
		return showGraphObject(rt, data, output)
	}
	page, err := client.Page(rt.Ctx(), path, params,
		graph.PageOptions{Beta: flags.beta, Eventual: advanced, Limit: flags.limit, All: flags.all})
	if err != nil {
		return err
	}
	var columns []string
	if flags.selectFields != "" {
		for _, name := range strings.Split(flags.selectFields, ",") {
			columns = append(columns, strings.TrimSpace(name))
		}
	}
	if err := showGraphItems(rt, page.Items, output, columns); err != nil {
		return err
	}
	note := fmt.Sprintf("%d item(s)", len(page.Items))
	if page.Count != nil {
		note += fmt.Sprintf(" of %d", *page.Count)
	}
	if page.More {
		note += "; more exist (--all, or --limit N)"
	}
	rt.Console.Note("%s", note)
	return nil
}

func graphLookup(rt *Runtime, kind, what string) *cobra.Command {
	common := &Common{}
	var selectFields string
	command := &cobra.Command{
		Use:   "get-" + kind + " NAME_OR_ID",
		Short: "One " + what + ", by name or id, with every property Graph returns.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := graphClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Lookup(rt.Ctx(), kind, args[0], selectFields)
			switch {
			case err != nil:
				return err
			case len(found) == 0:
				return graph.NotFound(kind, args[0])
			case len(found) == 1:
				return showGraphObject(rt, found[0], output)
			case kind != "device":
				var ids []string
				for _, item := range found {
					ids = append(ids, fields.Text(item, "id"))
				}
				return errs.Ambiguousf("%d %ss are named '%s'", len(found), what, args[0]).
					WithHint("use an id instead: %s", strings.Join(ids, ", "))
			}
			// Stale registrations keep a device's name, so every match is shown.
			if err := showGraphItems(rt, found, output, nil); err != nil {
				return err
			}
			rt.Console.Warn("%d devices are named '%s'", len(found), args[0])
			return nil
		},
	}
	command.Flags().StringVar(&selectFields, "select", "", "Properties to return, e.g. id,displayName ($select).")
	common.AddOutput(command, true)
	return command
}

func graphHunt(rt *Runtime) *cobra.Command {
	common := &Common{}
	var file, timespan string
	command := &cobra.Command{
		Use:   "hunt [QUERY]",
		Short: "Advanced Hunting (KQL) over the whole Defender XDR schema, through Graph.",
		Long: "Advanced Hunting (KQL) over the whole Defender XDR schema, through Graph.\n\n" +
			"Email, identity, cloud app and alert tables as well as the device ones. Needs " +
			"ThreatHunting.Read.All: an interactive or device-code profile whose app has it. Omit the " +
			"query, or pass -, to read it from stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			text, err := readQuery(rt, query, file)
			if err != nil {
				return err
			}
			return runGraphHunt(rt, common, text, timespan)
		},
	}
	command.Flags().StringVar(&file, "file", "", "Read the query from a file.")
	command.Flags().StringVar(&timespan, "timespan", "", "How far back the data goes, e.g. 7d. Default: 30 days.")
	common.AddOutput(command, true)
	return command
}

// runGraphHunt is shared by graph hunt and xdr hunt, which defaults to Graph.
func runGraphHunt(rt *Runtime, common *Common, text, timespan string) error {
	output, err := common.Output(rt)
	if err != nil {
		return err
	}
	span, err := optionalDuration("--timespan", timespan)
	if err != nil {
		return err
	}
	client, _, err := graphClient(rt, common.Profile)
	if err != nil {
		return err
	}
	result, err := client.Hunt(rt.Ctx(), text, time.Duration(span))
	if err != nil {
		return err
	}
	if err := rt.Console.Query(result, output); err != nil {
		return err
	}
	rt.Console.Note("%d row(s)", len(result.Rows))
	return nil
}

// Rendering ------------------------------------------------------------------------------

func withoutOData(data fields.Object) (fields.Object, []string) {
	record := fields.Object{}
	var keys []string
	for key, value := range data {
		if !strings.HasPrefix(key, "@odata") {
			record[key] = value
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return record, keys
}

func showGraphObject(rt *Runtime, data fields.Object, output render.Output) error {
	record, keys := withoutOData(data)
	if output != render.Table {
		row := make([]render.Cell, len(keys))
		for index, key := range keys {
			row[index] = render.Plain(graphCell(record[key]))
		}
		return rt.Console.Emit(output, keys, [][]render.Cell{row}, record)
	}
	var ordered []string
	for _, key := range graphFirst {
		if _, ok := record[key]; ok {
			ordered = append(ordered, key)
		}
	}
	for _, key := range keys {
		if !slices.Contains(graphFirst, key) {
			ordered = append(ordered, key)
		}
	}
	var items [][2]string
	for _, key := range ordered {
		items = append(items, [2]string{key, graphCell(record[key])})
	}
	rt.Console.Println(pairs(rt, items))
	return nil
}

func showGraphItems(rt *Runtime, items []fields.Object, output render.Output, columns []string) error {
	if output == render.JSON {
		if items == nil {
			items = []fields.Object{}
		}
		return rt.Console.PrintJSON(items)
	}
	if columns == nil {
		seen := map[string]bool{}
		for _, item := range items {
			for key := range item {
				if !strings.HasPrefix(key, "@odata") && !seen[key] {
					seen[key] = true
					columns = append(columns, key)
				}
			}
		}
		sort.Strings(columns)
		if output == render.Table {
			// A table of every property is too wide to read: the familiar ones, or eight.
			var familiar []string
			for _, key := range graphFirst {
				if seen[key] {
					familiar = append(familiar, key)
				}
			}
			if len(familiar) > 0 {
				columns = familiar
			} else if len(columns) > 8 {
				columns = columns[:8]
			}
		}
	}
	headers := make([]string, len(columns))
	for index, column := range columns {
		headers[index] = strings.ToUpper(column)
	}
	rows := make([][]render.Cell, len(items))
	for index, item := range items {
		rows[index] = make([]render.Cell, len(columns))
		for at, column := range columns {
			rows[index][at] = render.Plain(graphCell(item[column]))
		}
	}
	return rt.Console.Emit(output, headers, rows, items)
}

// graphCell is a JSON value as one cell: text as it is, numbers and booleans as JSON
// writes them, anything else as JSON.
func graphCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// quotedSearch is a $search in the double quotes Graph wants, added when missing.
func quotedSearch(search string) string {
	text := strings.TrimSpace(search)
	if text == "" || strings.HasPrefix(text, `"`) {
		return text
	}
	return `"` + text + `"`
}
