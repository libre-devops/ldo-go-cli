package cli

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/detections"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/graph"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/incidents"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/pim"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// tokenRequirements are what every feature's calls need; the token checks report each.
func tokenRequirements() []microsoft.Requirement {
	return slices.Concat(featureRequirements...)
}

// featureRequirements is each feature package's requirements.
var featureRequirements = [][]microsoft.Requirement{entra.Requirements, xdr.Requirements, intune.Requirements, pim.Requirements, incidents.Requirements, detections.Requirements, graph.Requirements}

var checkColours = map[string]string{"pass": "green", "warn": "yellow", "fail": "red"}

// tokenFlags are the options every token check takes.
type tokenFlags struct {
	raw       bool
	require   []string
	strict    bool
	allClaims bool
}

func (f *tokenFlags) add(cmd *cobra.Command, raw bool) {
	if raw {
		cmd.Flags().BoolVar(&f.raw, "raw", false, "Print only the token, for piping. Still checked first.")
	}
	cmd.Flags().StringArrayVar(&f.require, "require", nil, "Scope or role the token must carry. Repeatable.")
	cmd.Flags().BoolVar(&f.strict, "strict", false, "Treat warnings as failures.")
	cmd.Flags().BoolVar(&f.allClaims, "all-claims", false, "Show every claim, not only the summary.")
}

// tokenCommand is a command that gets a token for a resource and checks it: resource is
// fixed ("graph") or, when empty, the command's argument.
func tokenCommand(rt *Runtime, use, short, long, resource string) *cobra.Command {
	common := &Common{}
	flags := &tokenFlags{}
	args := cobra.ExactArgs(1)
	if resource != "" {
		args = cobra.NoArgs
	}
	command := &cobra.Command{
		Use: use, Short: short, Long: long, Args: args,
		RunE: func(_ *cobra.Command, positional []string) error {
			target := resource
			if target == "" {
				target = positional[0]
			}
			return runToken(rt, target, common, flags)
		},
	}
	flags.add(command, true)
	common.AddOutput(command, false)
	return command
}

func runToken(rt *Runtime, resource string, common *Common, flags *tokenFlags) error {
	output, err := common.Output(rt)
	if err != nil {
		return err
	}
	profile, err := rt.Profile(common.Profile)
	if err != nil {
		return err
	}
	target, err := microsoft.ResolveResource(resource, profile.Cloud)
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
	checks := microsoft.ValidateToken(decoded, microsoft.ValidateOptions{
		Resource: &target, TenantID: profile.TenantID, Required: flags.require,
		Requirements: tokenRequirements(), Now: rt.Clock(),
	})
	ok := microsoft.Passed(checks, flags.strict)
	if flags.raw {
		if !ok {
			checksToStderr(rt, checks)
			return &ExitStatus{Code: ExitError}
		}
		rt.Console.Println(access.Token)
		return nil
	}
	heading := target.Key + " token for profile " + profile.Name + " (" + profile.Auth + ")"
	if err := tokenReport(rt, decoded, checks, heading, flags.allClaims, output, ok); err != nil {
		return err
	}
	if !ok {
		return &ExitStatus{Code: ExitError}
	}
	return nil
}

func inspectTokenCommand(rt *Runtime) *cobra.Command {
	common := &Common{}
	flags := &tokenFlags{}
	var resource, tenant string
	command := &cobra.Command{
		Use:   "inspect-token [TOKEN]",
		Short: "Decode a token you already have and check its claims. Nothing is sent anywhere.",
		Long: "Decode a token you already have and check its claims. Nothing is sent anywhere.\n\n" +
			"Omit the token or pass - to read stdin, which keeps it out of shell history.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, positional []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			value, err := tokenFromInput(rt, positional)
			if err != nil {
				return err
			}
			decoded, err := microsoft.DecodeToken(value)
			if err != nil {
				return err
			}
			opts := microsoft.ValidateOptions{TenantID: tenant, Required: flags.require,
				Requirements: tokenRequirements(), Now: rt.Clock()}
			if resource != "" {
				target, err := microsoft.ResolveResource(resource, microsoft.Public)
				if err != nil {
					return err
				}
				opts.Resource = &target
			}
			checks := microsoft.ValidateToken(decoded, opts)
			ok := microsoft.Passed(checks, flags.strict)
			if err := tokenReport(rt, decoded, checks, "token", flags.allClaims, output, ok); err != nil {
				return err
			}
			if !ok {
				return &ExitStatus{Code: ExitError}
			}
			return nil
		},
	}
	command.Flags().StringVar(&resource, "resource", "", "Expected API: graph, mde, arm, ..., or a URL.")
	command.Flags().StringVar(&tenant, "tenant", "", "Expected tenant id.")
	flags.add(command, false)
	common.AddOutput(command, false)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

func tokenFromInput(rt *Runtime, positional []string) (string, error) {
	if len(positional) == 1 && positional[0] != "-" {
		return positional[0], nil
	}
	if rt.StdinIsTerminal {
		return "", errs.Inputf("no token given").
			WithHint("pipe one in, e.g. 'pbpaste | %s entra inspect-token'", brand.Command)
	}
	value, err := rt.ReadAll()
	if err != nil {
		return "", errs.Inputf("cannot read stdin: %v", err)
	}
	if strings.TrimSpace(value) == "" {
		return "", errs.Inputf("no token on stdin")
	}
	return value, nil
}

func signOutCommand(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "sign-out",
		Short: "Forget the sign-in an interactive or device-code profile keeps (see token_cache).",
		Long: "Forget the sign-in an interactive or device-code profile keeps (see token_cache).\n\n" +
			"The refresh token is removed from the file; the next command signs in afresh. Signing out " +
			"of Entra ID itself, everywhere, is done in your account settings.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			profile, err := rt.Profile(common.Profile)
			if err != nil {
				return err
			}
			if profile.Auth == "azure-cli" {
				return errs.Configf("profile %q uses the Azure CLI's sign-in, which it keeps itself", profile.Name).
					WithHint("run 'az logout', or 'az account clear' to forget every account")
			}
			if profile.TokenCache == "memory" {
				rt.Console.Note("profile %q keeps no sign-in (token_cache = \"memory\")", profile.Name)
				return nil
			}
			forgot, err := rt.SignOut(profile)
			if err != nil {
				return err
			}
			if forgot {
				rt.Console.Note("Forgot the sign-in kept for %s (%s).", profile.Name, profile.TokenCache)
			} else {
				rt.Console.Note("No sign-in was kept for %s.", profile.Name)
			}
			return nil
		},
	}
	common.AddProfile(command)
	return command
}

// checksTable is a token's checks as a table: each one's result, name and detail.
func checksTable(rt *Runtime, checks []microsoft.Check) string {
	return rt.Console.Table([]string{"RESULT", "CHECK", "DETAIL"}, checkRows(checks))
}

func checkRows(checks []microsoft.Check) [][]render.Cell {
	rows := make([][]render.Cell, len(checks))
	for index, check := range checks {
		rows[index] = []render.Cell{render.Coloured(strings.ToUpper(check.Status), checkColours[check.Status]),
			render.Plain(check.Name), render.Plain(check.Detail)}
	}
	return rows
}

// checksToStderr is a token's checks on stderr: the table, or one record each when
// structured.
func checksToStderr(rt *Runtime, checks []microsoft.Check) {
	if rt.Console.Structured && rt.Console.Logger != nil {
		for _, check := range checks {
			text := check.Name + ": " + check.Detail
			switch check.Status {
			case "fail":
				rt.Console.Logger.Error(text)
			case "warn":
				rt.Console.Logger.Warn(text)
			default:
				rt.Console.Logger.Info(text)
			}
		}
		return
	}
	rows := checkRows(checks)
	rt.Console.Err.Write([]byte(render.FormatTable([]string{"RESULT", "CHECK", "DETAIL"}, rows, 0, rt.Console.ColourErr()) + "\n"))
}

func tokenReport(rt *Runtime, decoded microsoft.DecodedToken, checks []microsoft.Check, heading string,
	allClaims bool, output render.Output, ok bool) error {
	switch output {
	case render.JSON:
		return rt.Console.PrintJSON(map[string]any{"valid": ok, "checks": checks,
			"header": decoded.Header, "claims": decoded.Claims})
	case render.CSV, render.TSV, render.HTML:
		var rows [][]render.Cell
		for _, check := range checks {
			rows = append(rows, []render.Cell{render.Coloured(check.Status, checkColours[check.Status]),
				render.Plain(check.Name), render.Plain(check.Detail)})
		}
		return rt.Console.Emit(output, []string{"RESULT", "CHECK", "DETAIL"}, rows, nil)
	}
	appName := claimText(decoded, "app_displayname")
	rt.Console.Println(title(rt, heading))
	rt.Console.Println(pairs(rt, [][2]string{
		{"Audience", strings.Join(decoded.Audiences(), ", ")},
		{"Tenant", decoded.TenantID()},
		{"Issuer", decoded.Issuer()},
		{"Principal", decoded.Principal() + " (" + decoded.IdentityType() + ")"},
		{"Client app", strings.TrimSpace(appName + " " + decoded.AppID())},
		{"Scopes (scp)", strings.Join(decoded.Scopes(), " ")},
		{"Roles", strings.Join(decoded.Roles(), " ")},
		{"Issued", rt.Console.When(decoded.Timestamp("iat"))},
		{"Expires", rt.Console.When(decoded.ExpiresAt())},
	}))
	if allClaims {
		rt.Console.Println("")
		rt.Console.Println(pairs(rt, claimPairs(decoded)))
	}
	rt.Console.Println("")
	rt.Console.Println(checksTable(rt, checks))
	rt.Console.Note("Signature not verified: this checks the claims only.")
	return nil
}

func claimText(decoded microsoft.DecodedToken, claim string) string {
	if value, ok := decoded.Claims[claim].(string); ok {
		return value
	}
	return ""
}

func claimPairs(decoded microsoft.DecodedToken) [][2]string {
	names := make([]string, 0, len(decoded.Claims))
	for name := range decoded.Claims {
		names = append(names, name)
	}
	sort.Strings(names)
	var items [][2]string
	for _, name := range names {
		value, isText := decoded.Claims[name].(string)
		if !isText {
			encoded, _ := json.Marshal(decoded.Claims[name])
			value = string(encoded)
		}
		items = append(items, [2]string{name, value})
	}
	return items
}
