package cli

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/pim"
)

// Each command covers three areas: Azure resource roles (ARM), Entra roles and PIM for
// Groups (Graph). An area that cannot be read (no P2 licence, a token without the scope)
// is reported as a warning and the others still show; the command fails only when every
// area it was asked for fails.

// pimCommand is the pim group.
func pimCommand(rt *Runtime) *cobra.Command {
	group := newGroup("pim", "Privileged Identity Management: eligible and active roles, requests, approvals.")
	group.AddCommand(pimEligible(rt), pimActive(rt), pimRequests(rt), pimApprovals(rt), pimSettings(rt))
	return group
}

// pimFlags choose the areas, and someone else's roles.
type pimFlags struct {
	azure, entra, groups bool
	user                 string
	subscriptions        []string
}

func (f *pimFlags) add(cmd *cobra.Command, others bool) {
	cmd.Flags().BoolVar(&f.azure, "azure", false, "Azure resource roles.")
	cmd.Flags().BoolVar(&f.entra, "entra", false, "Entra (directory) roles.")
	cmd.Flags().BoolVar(&f.groups, "groups", false, "PIM for Groups.")
	if others {
		cmd.Flags().StringVarP(&f.user, "user", "u", "", "Someone else's (UPN, name or object id). Default: the signed-in user.")
		cmd.Flags().StringArrayVarP(&f.subscriptions, "subscription", "s", nil, "With --user: subscriptions to search for Azure roles. Default: all in the tenant.")
	}
}

func (f *pimFlags) areas() []string {
	var chosen []string
	for index, wanted := range []bool{f.azure, f.entra, f.groups} {
		if wanted {
			chosen = append(chosen, pim.Areas[index])
		}
	}
	if len(chosen) == 0 {
		return pim.Areas
	}
	return chosen
}

// pimSession is what every PIM command reads through: the profile, both clients, and the
// principal and scopes a --user asks about.
type pimSession struct {
	profile   microsoft.Profile
	azure     *pim.AzureClient
	graph     *pim.GraphClient
	principal string
	scopes    []string
}

func openPIM(rt *Runtime, common *Common, flags *pimFlags) (*pimSession, error) {
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return nil, err
	}
	api, err := rt.API(profile)
	if err != nil {
		return nil, err
	}
	session := &pimSession{profile: profile}
	if session.azure, err = pim.NewAzure(api); err != nil {
		return nil, err
	}
	if session.graph, err = pim.NewGraph(api); err != nil {
		return nil, err
	}
	if flags.user == "" {
		return session, nil
	}
	if session.principal, err = pimPrincipal(rt, api, flags.user); err != nil {
		return nil, err
	}
	if flags.azure || (!flags.entra && !flags.groups) {
		client, err := azureClient(rt, profile)
		if err != nil {
			return nil, err
		}
		ids, err := subscriptionIDs(rt, client, profile, flags.subscriptions)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			session.scopes = append(session.scopes, "/subscriptions/"+id)
		}
	}
	return session, nil
}

func pimPrincipal(rt *Runtime, api microsoft.API, user string) (string, error) {
	if util.IsGUID(user) {
		return strings.ToLower(strings.TrimSpace(user)), nil
	}
	client, err := entra.New(api)
	if err != nil {
		return "", err
	}
	found, err := client.FindPrincipal(rt.Ctx(), user)
	return found.ID, err
}

// gather runs each area's call; it warns about the ones that fail, and fails only if all
// do.
func gather[T any](rt *Runtime, areas []string, calls map[string]func() ([]T, error)) ([]T, error) {
	var found []T
	failed := 0
	for _, area := range areas {
		items, err := calls[area]()
		if err == nil {
			found = append(found, items...)
			continue
		}
		problem := errs.As(err)
		if problem == nil {
			return nil, err
		}
		failed++
		rt.Console.Warn("%s: %s", area, problem.Message)
		if hint := or(pimHint(area, problem.Error()), problem.Hint); hint != "" {
			rt.Console.Note("hint: %s", hint)
		}
	}
	if failed > 0 && failed == len(areas) {
		return nil, &ExitStatus{Code: ExitError}
	}
	return found, nil
}

func pimHint(area, text string) string {
	switch {
	case strings.Contains(text, "AadPremiumLicenseRequired") || strings.Contains(text, "TenantNotOnboarded"):
		return "PIM needs Microsoft Entra ID P2 or ID Governance in the tenant"
	case area != "azure" && (strings.Contains(text, "PermissionScopeNotGranted") || strings.Contains(text, "HTTP 403")):
		return `these views need a token with the PIM scopes; the Azure CLI's never has them. Use a profile with auth = "interactive" ` +
			`or "device-code" (see ` + brand.Docs("authentication") + ")"
	}
	return ""
}

var isoDuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// pimDuration is an ISO 8601 duration as people say it: PT8H is 8h, P1DT2H is 1d 2h.
func pimDuration(value string) string {
	match := isoDuration.FindStringSubmatch(strings.TrimSpace(value))
	if value == "" {
		return "-"
	}
	if match == nil {
		return value
	}
	var parts []string
	for index, unit := range []string{"d", "h", "m", "s"} {
		if match[index+1] != "" {
			parts = append(parts, match[index+1]+unit)
		}
	}
	if len(parts) == 0 {
		return value
	}
	return strings.Join(parts, " ")
}

func assignmentRows(rt *Runtime, items []pim.Assignment, active bool) [][]render.Cell {
	rows := make([][]render.Cell, len(items))
	for index, item := range items {
		ends := render.Plain(rt.Console.When(item.Ends))
		if item.Permanent() && active {
			ends = render.Coloured("permanent", "yellow")
		}
		row := []render.Cell{render.Plain(item.Area), render.Plain(item.Role), render.Plain(item.Scope), render.Plain(or(item.MemberType, "-"))}
		if active {
			kind := render.Plain(or(item.AssignmentType, "-"))
			if item.Activated() {
				kind = render.Coloured("activated", "green")
			}
			row = append(row, kind)
		}
		rows[index] = append(row, render.Plain(rt.Console.When(item.Starts)), ends)
	}
	return rows
}

func assignmentRecords(items []pim.Assignment) []any {
	records := []any{}
	for _, item := range items {
		records = append(records, map[string]any{"area": item.Area, "role": item.Role, "scope": item.Scope, "member_type": item.MemberType,
			"assignment_type": item.AssignmentType, "permanent": item.Permanent(), "raw": item.Raw})
	}
	return records
}

func pimEligible(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &pimFlags{}
	command := &cobra.Command{
		Use:   "eligible",
		Short: "List the roles you (or --user) can activate through PIM.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			session, err := openPIM(rt, common, flags)
			if err != nil {
				return err
			}
			ctx := rt.Ctx()
			items, err := gather(rt, flags.areas(), map[string]func() ([]pim.Assignment, error){
				"azure": func() ([]pim.Assignment, error) {
					return session.azure.Eligible(ctx, session.principal, session.scopes)
				},
				"entra":  func() ([]pim.Assignment, error) { return session.graph.RoleEligible(ctx, session.principal) },
				"groups": func() ([]pim.Assignment, error) { return session.graph.GroupEligible(ctx, session.principal) },
			})
			if err != nil {
				return err
			}
			if err := rt.Console.Emit(output, []string{"AREA", "ROLE", "SCOPE", "MEMBERSHIP", "FROM", "UNTIL"}, assignmentRows(rt, items, false),
				assignmentRecords(items)); err != nil {
				return err
			}
			rt.Console.Note("%d eligible role(s)", len(items))
			return nil
		},
	}
	flags.add(command, true)
	common.AddOutput(command, true)
	return command
}

func pimActive(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &pimFlags{}
	var permanentOnly bool
	command := &cobra.Command{
		Use:   "active",
		Short: "List the roles you (or --user) hold now: activated through PIM, or permanent.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			session, err := openPIM(rt, common, flags)
			if err != nil {
				return err
			}
			ctx := rt.Ctx()
			items, err := gather(rt, flags.areas(), map[string]func() ([]pim.Assignment, error){
				"azure":  func() ([]pim.Assignment, error) { return session.azure.Active(ctx, session.principal, session.scopes) },
				"entra":  func() ([]pim.Assignment, error) { return session.graph.RoleActive(ctx, session.principal) },
				"groups": func() ([]pim.Assignment, error) { return session.graph.GroupActive(ctx, session.principal) },
			})
			if err != nil {
				return err
			}
			standing := 0
			var kept []pim.Assignment
			for _, item := range items {
				if item.Permanent() {
					standing++
				}
				if !permanentOnly || item.Permanent() {
					kept = append(kept, item)
				}
			}
			if err := rt.Console.Emit(output, []string{"AREA", "ROLE", "SCOPE", "MEMBERSHIP", "TYPE", "FROM", "UNTIL"}, assignmentRows(rt, kept, true),
				assignmentRecords(kept)); err != nil {
				return err
			}
			if permanentOnly {
				standing = len(kept)
			}
			rt.Console.Note("%d active role(s), %d of them permanent", len(kept), standing)
			return nil
		},
	}
	flags.add(command, true)
	command.Flags().BoolVar(&permanentOnly, "permanent-only", false, "Only standing access: roles with no end date.")
	common.AddOutput(command, true)
	return command
}

var requestHeaders = []string{"AREA", "CREATED", "ACTION", "STATUS", "ROLE", "SCOPE", "FOR", "JUSTIFICATION"}

func requestRows(rt *Runtime, items []pim.Request) [][]render.Cell {
	colours := map[string]string{"pendingapproval": "yellow", "denied": "red", "provisioned": "green"}
	rows := make([][]render.Cell, len(items))
	for index, item := range items {
		span := rt.Console.When(item.Ends)
		if item.Duration != "" {
			span = pimDuration(item.Duration)
		}
		rows[index] = []render.Cell{render.Plain(item.Area), render.Plain(rt.Console.When(item.Created)), render.Plain(item.Action),
			render.Coloured(item.Status, colours[strings.ToLower(item.Status)]), render.Plain(item.Role), render.Plain(item.Scope),
			render.Plain(span), render.Plain(item.Justification)}
	}
	return rows
}

func requestRecords(items []pim.Request) []any {
	records := []any{}
	for _, item := range items {
		records = append(records, map[string]any{"area": item.Area, "status": item.Status, "role": item.Role, "raw": item.Raw})
	}
	return records
}

func pimRequests(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &pimFlags{}
	var pending bool
	command := &cobra.Command{
		Use:   "requests",
		Short: "List PIM requests you (or --user) made, newest first, and where each has got to.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			session, err := openPIM(rt, common, flags)
			if err != nil {
				return err
			}
			ctx := rt.Ctx()
			items, err := gather(rt, flags.areas(), map[string]func() ([]pim.Request, error){
				"azure": func() ([]pim.Request, error) {
					return session.azure.Requests(ctx, false, session.principal, session.scopes)
				},
				"entra":  func() ([]pim.Request, error) { return session.graph.RoleRequests(ctx, false, session.principal) },
				"groups": func() ([]pim.Request, error) { return session.graph.GroupRequests(ctx, false, session.principal) },
			})
			if err != nil {
				return err
			}
			var kept []pim.Request
			for _, item := range items {
				if !pending || item.Pending() {
					kept = append(kept, item)
				}
			}
			if err := rt.Console.Emit(output, requestHeaders, requestRows(rt, kept), requestRecords(kept)); err != nil {
				return err
			}
			rt.Console.Note("%d request(s)", len(kept))
			return nil
		},
	}
	flags.add(command, true)
	command.Flags().BoolVar(&pending, "pending", false, "Only requests still waiting.")
	common.AddOutput(command, true)
	return command
}

func pimApprovals(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &pimFlags{}
	command := &cobra.Command{
		Use:   "approvals",
		Short: "List PIM requests waiting for you to approve them.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			session, err := openPIM(rt, common, flags)
			if err != nil {
				return err
			}
			ctx := rt.Ctx()
			items, err := gather(rt, flags.areas(), map[string]func() ([]pim.Request, error){
				"azure":  func() ([]pim.Request, error) { return session.azure.Requests(ctx, true, "", nil) },
				"entra":  func() ([]pim.Request, error) { return session.graph.RoleRequests(ctx, true, "") },
				"groups": func() ([]pim.Request, error) { return session.graph.GroupRequests(ctx, true, "") },
			})
			if err != nil {
				return err
			}
			var kept []pim.Request
			for _, item := range items {
				if item.Pending() {
					kept = append(kept, item)
				}
			}
			if err := rt.Console.Emit(output, requestHeaders, requestRows(rt, kept), requestRecords(kept)); err != nil {
				return err
			}
			rt.Console.Note("%d request(s) waiting for you; approve them in the portal or with your usual tooling (%s only reads)", len(kept), brand.Command)
			return nil
		},
	}
	flags.add(command, false)
	common.AddOutput(command, true)
	return command
}

func pimSettings(rt *Runtime) *cobra.Command {
	common := &Common{}
	var scope, group string
	var owner bool
	command := &cobra.Command{
		Use:   "settings [ROLE]",
		Short: "Show what activating a role takes: duration, MFA, justification, approval, approvers.",
		Long: "Show what activating a role takes: duration, MFA, justification, approval, approvers.\n\n" +
			"ROLE is a role name, e.g. Owner or 'Global Administrator'. An Azure role needs --scope; an Entra role needs " +
			"only its name; a group needs --group.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 && group == "" {
				return Usagef("ROLE", "name a role, or pass --group")
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			found, err := readSettings(rt, common, args, scope, group, owner)
			if err != nil {
				return err
			}
			return showSettings(rt, output, found)
		},
	}
	command.Flags().StringVar(&scope, "scope", "", "An Azure scope, e.g. /subscriptions/SUBSCRIPTION_ID, for an Azure role.")
	command.Flags().StringVar(&group, "group", "", "A PIM for Groups group (name or object id).")
	command.Flags().BoolVar(&owner, "owner", false, "With --group: the owner settings.")
	common.AddOutput(command, false)
	return command
}

func readSettings(rt *Runtime, common *Common, args []string, scope, group string, owner bool) (pim.Settings, error) {
	session, err := openPIM(rt, common, &pimFlags{})
	if err != nil {
		return pim.Settings{}, err
	}
	area := "entra"
	var found pim.Settings
	switch {
	case group != "":
		area = "groups"
		groupID := group
		if !util.IsGUID(group) {
			api, err := rt.API(session.profile)
			if err != nil {
				return pim.Settings{}, err
			}
			client, err := entra.New(api)
			if err != nil {
				return pim.Settings{}, err
			}
			resolved, err := client.GetGroup(rt.Ctx(), group)
			if err != nil {
				return pim.Settings{}, err
			}
			groupID = resolved.ID
		}
		access := "member"
		if owner {
			access = "owner"
		}
		found, err = session.graph.GroupSettings(rt.Ctx(), groupID, access)
	case scope != "":
		area = "azure"
		found, err = session.azure.Settings(rt.Ctx(), args[0], scope)
	default:
		found, err = session.graph.RoleSettings(rt.Ctx(), args[0])
	}
	if problem := errs.As(err); problem != nil {
		// The same error, of the same kind, with what to do about it in this area.
		copied := *problem
		copied.Hint = or(pimHint(area, problem.Error()), problem.Hint)
		return pim.Settings{}, &copied
	}
	return found, err
}

func yesNo(value bool) string { return render.YesNo(&value) }

func showSettings(rt *Runtime, output render.Output, found pim.Settings) error {
	items := [][2]string{
		{"Role", found.Role}, {"Area", found.Area}, {"Scope", found.Scope}, {"Longest activation", pimDuration(found.MaxActivation)},
		{"Needs MFA", yesNo(found.RequiresMFA)}, {"Needs justification", yesNo(found.RequiresJustification)},
		{"Needs a ticket", yesNo(found.RequiresTicket)}, {"Needs approval", yesNo(found.RequiresApproval)},
		{"Approvers", strings.Join(found.Approvers, ", ")}, {"Authentication context", found.AuthenticationContext},
		{"Eligible assignments", pimDuration(found.EligibleExpiry)}, {"Active assignments", pimDuration(found.ActiveExpiry)},
	}
	if output == render.Table {
		rt.Console.Println(pairs(rt, items))
		return nil
	}
	headers := make([]string, len(items))
	row := make([]render.Cell, len(items))
	for index, item := range items {
		headers[index], row[index] = item[0], render.Plain(item[1])
	}
	rules := []fields.Object{}
	rules = append(rules, found.Rules...)
	return rt.Console.Emit(output, headers, [][]render.Cell{row}, map[string]any{"role": found.Role, "area": found.Area, "scope": found.Scope,
		"max_activation": orNil(found.MaxActivation), "requires_mfa": found.RequiresMFA, "requires_justification": found.RequiresJustification,
		"requires_ticket": found.RequiresTicket, "requires_approval": found.RequiresApproval, "approvers": nonNil(found.Approvers),
		"authentication_context": orNil(found.AuthenticationContext), "eligible_expiry": orNil(found.EligibleExpiry),
		"active_expiry": orNil(found.ActiveExpiry), "rules": rules})
}
