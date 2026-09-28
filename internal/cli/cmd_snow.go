package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow/instance"
)

// snowRequirements are the roles each ServiceNow feature's calls need; whoami reports
// each one.
var snowRequirements = instance.Requirements

func snowCommand(rt *Runtime) *cobra.Command {
	group := newGroup("snow", "ServiceNow: sign in, who you are, the instance and its applications.")
	group.AddCommand(snowSignInCommand(rt), snowSignOutCommand(rt), snowWhoamiCommand(rt), snowTokenCommand(rt),
		snowInstanceCommand(rt), snowAppsCommand(rt, "apps", false), snowAppsCommand(rt, "plugins", true))
	return group
}

// snowProfileFlag gives cmd ServiceNow's own -p.
func snowProfileFlag(cmd *cobra.Command, profile *string) {
	cmd.Flags().StringVarP(profile, "profile", "p", "", "ServiceNow profile. Default: $"+brand.EnvVar("SNOW_PROFILE")+
		", else default_profile, else the one from the environment.")
}

func snowSignInCommand(rt *Runtime) *cobra.Command {
	var profile string
	command := &cobra.Command{
		Use:   "sign-in",
		Short: "Sign in afresh and keep the sign-in, so later commands do not ask.",
		Long: "Sign in afresh and keep the sign-in, so later commands do not ask.\n\n" +
			`With sign_in = "browser" (the default) you get a link to open in any browser, where you sign in as you do to ` +
			"the instance (single sign-on and MFA too); then paste back the address it lands on. With " +
			`sign_in = "password" you are asked for the password, unless it is in the environment.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			selected, err := rt.SnowProfile(profile)
			if err != nil {
				return err
			}
			oauth, err := rt.SnowOAuth(selected, "which sends the password each time")
			if err != nil {
				return err
			}
			if _, err := oauth.SignIn(rt.Ctx()); err != nil {
				return err
			}
			client, err := rt.SnowInstance(selected)
			if err != nil {
				return err
			}
			user, err := client.CurrentUser(rt.Ctx(), "")
			if err != nil {
				return err
			}
			rt.Console.Note("Signed in to %s as %s (%s). The sign-in is kept (%s); %s forgets it.", selected.Host(), user.UserName,
				user.Name, selected.TokenCache, brand.Suggest("snow sign-out -p "+selected.Name))
			return nil
		},
	}
	snowProfileFlag(command, &profile)
	return command
}

func snowSignOutCommand(rt *Runtime) *cobra.Command {
	var profile string
	command := &cobra.Command{
		Use:   "sign-out",
		Short: "Forget the kept sign-in, so the next command signs in afresh.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			selected, err := rt.SnowProfile(profile)
			if err != nil {
				return err
			}
			credential, err := rt.SnowCredential(selected)
			if err != nil {
				return err
			}
			oauth, ok := credential.(*servicenow.OAuth)
			if !ok {
				rt.Console.Note("profile %q uses basic sign-in, so nothing is kept", selected.Name)
				return nil
			}
			forgot, err := oauth.SignOut()
			if err != nil {
				return err
			}
			if forgot {
				rt.Console.Note("Forgot the sign-in kept for %s (%s).", selected.Name, selected.Host())
			} else {
				rt.Console.Note("No sign-in was kept for %s.", selected.Name)
			}
			return nil
		},
	}
	snowProfileFlag(command, &profile)
	return command
}

func snowWhoamiCommand(rt *Runtime) *cobra.Command {
	var common Common
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are signed in as, your roles, and which features they cover.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			selected, err := rt.SnowProfile(common.Profile)
			if err != nil {
				return err
			}
			client, err := rt.SnowInstance(selected)
			if err != nil {
				return err
			}
			user, err := client.CurrentUser(rt.Ctx(), "")
			if err != nil {
				return err
			}
			roles, err := client.Roles(rt.Ctx(), user)
			if err != nil {
				return err
			}
			return showSnowWhoami(rt, output, selected, user, roles)
		},
	}
	command.Flags().StringVarP(&common.output, "output", "o", "table", "table for people, json for scripts, csv for "+
		"spreadsheets, tsv for shell pipelines (no header, as az -o tsv), html for a page to open or share.")
	snowProfileFlag(command, &common.Profile)
	return command
}

func showSnowWhoami(rt *Runtime, output render.Output, selected servicenow.Profile, user instance.User, roles []string) error {
	covers := map[string]any{}
	for _, requirement := range snowRequirements {
		covers[requirement.Feature] = requirement.MetBy(roles)
	}
	var signIn any
	if selected.Auth == "oauth" {
		signIn = selected.SignIn
	}
	record := map[string]any{"profile": selected.Name, "instance": selected.Instance, "auth": selected.Auth, "sign_in": signIn,
		"user": user.Raw, "roles": roles, "covers": covers}
	if output != render.Table {
		return rt.Console.Emit(output, []string{"PROFILE", "INSTANCE", "USER", "NAME", "ROLES"},
			[][]render.Cell{render.Cells(selected.Name, selected.Host(), user.UserName, user.Name, strings.Join(roles, " "))}, record)
	}
	method := selected.Auth
	if selected.Auth == "oauth" {
		method = "oauth (" + selected.SignIn + ")"
	}
	shownRoles := strings.Join(roles, ", ")
	if shownRoles == "" {
		shownRoles = "(none)"
	}
	rt.Console.Println(pairs(rt, [][2]string{{"Profile", selected.Name}, {"Instance", selected.Instance}, {"Sign-in", method},
		{"User", user.UserName + " (" + user.Name + ")"}, {"Email", user.Email}, {"Roles", shownRoles}}))
	rt.Console.Println("")
	var rows [][]render.Cell
	for _, requirement := range snowRequirements {
		covered := render.Coloured("no", "yellow")
		if requirement.MetBy(roles) {
			covered = render.Coloured("yes", "green")
		}
		rows = append(rows, []render.Cell{render.Plain(requirement.Feature), covered})
	}
	rt.Console.Println(rt.Console.Table([]string{"FEATURE", "COVERED"}, rows))
	if user.LockedOut {
		rt.Console.Warn("the account is locked out")
	}
	return nil
}

func snowTokenCommand(rt *Runtime) *cobra.Command {
	var common Common
	var raw bool
	command := &cobra.Command{
		Use:   "token",
		Short: "Get an access token for an OAuth profile, and say how long it lasts.",
		Long: "Get an access token for an OAuth profile, and say how long it lasts.\n\n" +
			"The token itself is shown only with --raw. It lasts 30 minutes by default; the kept refresh token renews it " +
			"without asking.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			selected, err := rt.SnowProfile(common.Profile)
			if err != nil {
				return err
			}
			oauth, err := rt.SnowOAuth(selected, "which has no token")
			if err != nil {
				return err
			}
			access, err := oauth.GetToken(rt.Ctx(), selected.Instance, "")
			if err != nil {
				return err
			}
			if raw {
				rt.Console.Println(access.Token)
				return nil
			}
			kept := oauth.HasKeptSignIn()
			record := map[string]any{"profile": selected.Name, "instance": selected.Instance,
				"expires_on": render.ISO(access.ExpiresOn), "sign_in_kept": kept, "token_cache": selected.TokenCache}
			return rt.Console.Emit(output, []string{"PROFILE", "INSTANCE", "EXPIRES", "SIGN-IN KEPT"},
				[][]render.Cell{render.Cells(selected.Name, selected.Host(), rt.Console.When(access.ExpiresOn), yesNo(kept))}, record)
		},
	}
	command.Flags().BoolVar(&raw, "raw", false, "Print only the token, for piping.")
	command.Flags().StringVarP(&common.output, "output", "o", "table", "table for people, json for scripts, csv for "+
		"spreadsheets, tsv for shell pipelines (no header, as az -o tsv), html for a page to open or share.")
	snowProfileFlag(command, &common.Profile)
	return command
}

func snowInstanceCommand(rt *Runtime) *cobra.Command {
	var common Common
	command := &cobra.Command{
		Use:   "instance",
		Short: "Show the instance's release, and whether Security Incident Response is installed.",
		Long: "Show the instance's release, and whether Security Incident Response is installed.\n\n" +
			"Exits 3 when Security Incident Response is not installed.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			selected, err := rt.SnowProfile(common.Profile)
			if err != nil {
				return err
			}
			client, err := rt.SnowInstance(selected)
			if err != nil {
				return err
			}
			release, known, err := client.Release(rt.Ctx())
			if err != nil {
				return err
			}
			sir, err := client.SecurityIncidentResponse(rt.Ctx())
			if err != nil {
				return err
			}
			return showSnowInstance(rt, output, selected, release, known, sir)
		},
	}
	command.Flags().StringVarP(&common.output, "output", "o", "table", "table for people, json for scripts, csv for "+
		"spreadsheets, tsv for shell pipelines (no header, as az -o tsv), html for a page to open or share.")
	snowProfileFlag(command, &common.Profile)
	return command
}

func showSnowInstance(rt *Runtime, output render.Output, selected servicenow.Profile, release instance.Release, known bool, sir instance.AppStatus) error {
	var tag, family any
	label := "unknown"
	if known {
		tag, family, label = release.BuildTag, release.Family, release.Label()
	}
	record := map[string]any{"profile": selected.Name, "instance": selected.Instance, "release": tag, "family": family,
		"security_incident_response": map[string]any{"installed": sir.Installed, "version": sir.Version, "detail": sir.Detail}}
	state := render.Coloured("not installed", "yellow")
	if sir.Installed {
		state = render.Coloured(strings.TrimSpace("installed "+sir.Version), "green")
	}
	if err := rt.Console.Emit(output, []string{"INSTANCE", "RELEASE", "SECURITY INCIDENT RESPONSE"},
		[][]render.Cell{{render.Plain(selected.Host()), render.Plain(label), state}}, record); err != nil {
		return err
	}
	if !sir.Installed {
		rt.Console.Note("Security Incident Response is not installed. On a developer instance, activate it from " +
			"developer.servicenow.com (your instance > Activate Plugin), or search for it under All > System Applications > " +
			"All Available Applications.")
		return Attention
	}
	return nil
}

func snowAppsCommand(rt *Runtime, use string, hidden bool) *cobra.Command {
	var common Common
	var all bool
	command := &cobra.Command{
		Use:   use + " [SEARCH]",
		Short: "List the instance's applications (store and custom), optionally matching SEARCH.",
		Long: "List the instance's applications (store and custom), optionally matching SEARCH (part of a name or scope, " +
			"e.g. 'security').\n\nSecurity Incident Response, once installed, is listed as scope sn_si. ServiceNow closes " +
			"its plugin tables to the API, so older plugins without a scope do not show.",
		// plugins is what people search for; it lists the same applications.
		Hidden: hidden,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			selected, err := rt.SnowProfile(common.Profile)
			if err != nil {
				return err
			}
			client, err := rt.SnowInstance(selected)
			if err != nil {
				return err
			}
			search := ""
			if len(args) == 1 {
				search = args[0]
			}
			found, err := client.Applications(rt.Ctx(), search, !all)
			if err != nil {
				return err
			}
			return showSnowApps(rt, output, found)
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Include applications that are not active.")
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Usage = "ServiceNow profile. Default: $" + brand.EnvVar("SNOW_PROFILE") +
		", else default_profile, else the one from the environment."
	return command
}

func showSnowApps(rt *Runtime, output render.Output, found []instance.Application) error {
	rows := make([][]render.Cell, len(found))
	records := make([]any, len(found))
	for index, app := range found {
		state := render.Coloured("inactive", "bright_black")
		if app.Active {
			state = render.Coloured("active", "green")
		}
		rows[index] = []render.Cell{render.Plain(app.Name), render.Plain(app.Scope), render.Plain(app.Kind), state, render.Plain(app.Version)}
		records[index] = app.Raw
	}
	if err := rt.Console.Emit(output, []string{"NAME", "SCOPE", "KIND", "STATE", "VERSION"}, rows, records); err != nil {
		return err
	}
	rt.Console.Note("%d application(s)", len(found))
	return nil
}
