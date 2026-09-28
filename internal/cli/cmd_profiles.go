package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/azcli"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
)

// ProfileRows are a vendor's rows and records for the profiles listing; each vendor's
// runtime adds its own.
var ProfileRows = []func(rt *Runtime) ([][]render.Cell, []any, error){microsoftProfiles, servicenowProfiles}

// profilesCommand is profiles: every vendor's profiles, which is active, and whether each
// can sign in.
func profilesCommand(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "profiles",
		Short: "List configured profiles, which is active, and whether each can sign in.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			if _, err := rt.ConfigFile(); err != nil {
				return err
			}
			var rows [][]render.Cell
			records := []any{}
			for _, vendor := range ProfileRows {
				vendorRows, vendorRecords, err := vendor(rt)
				if err != nil {
					return err
				}
				rows = append(rows, vendorRows...)
				records = append(records, vendorRecords...)
			}
			if err := rt.Console.Emit(output, []string{"VENDOR", "", "PROFILE", "TARGET", "AUTH", "SIGNED IN", "DESCRIPTION"}, rows, records); err != nil {
				return err
			}
			if len(rows) == 0 {
				rt.Console.Warn("no profiles configured; %s writes a template", brand.Suggest("config init"))
			}
			return nil
		},
	}
	common.AddOutput(command, true)
	command.Flags().Lookup("profile").Hidden = true
	return command
}

type profileState struct {
	isDefault bool
	active    bool
	signedIn  *bool
}

func microsoftProfiles(rt *Runtime) ([][]render.Cell, []any, error) {
	ms, err := rt.OptionalMicrosoft()
	if err != nil || ms == nil {
		return nil, nil, err
	}
	accounts, azErr := rt.Az().Accounts(rt.Ctx())
	if azErr != nil {
		rt.Console.Warn("cannot read Azure CLI accounts: %v", azErr)
	}
	var profiles []microsoft.Profile
	for _, name := range ms.Names() {
		profiles = append(profiles, ms.Profiles[name])
	}
	var active *microsoft.Profile
	for _, account := range accounts {
		if account.IsDefault {
			if found, ok := azcli.MatchProfile(profiles, account); ok {
				active = &found
			}
		}
	}
	var rows [][]render.Cell
	var records []any
	var placeholders []string
	for _, profile := range profiles {
		state := profileState{isDefault: profile.Name == ms.DefaultProfile, active: active != nil && active.Name == profile.Name}
		if azErr == nil {
			_, found := azcli.FindAccount(accounts, profile)
			state.signedIn = &found
		}
		rows = append(rows, microsoftRow(profile, state))
		records = append(records, microsoftRecord(profile, state))
		if profile.HasPlaceholderIDs() {
			placeholders = append(placeholders, profile.Name)
		}
	}
	if len(placeholders) > 0 {
		rt.Console.Warn("placeholder ids in %s; edit %s", strings.Join(placeholders, ", "), ms.Path)
	}
	return rows, records, nil
}

func microsoftRow(profile microsoft.Profile, state profileState) []render.Cell {
	target := "tenant " + profile.TenantID
	if profile.SubscriptionID != "" {
		target = "subscription " + profile.SubscriptionID
	}
	if profile.Cloud.Name != "public" {
		target += " (" + profile.Cloud.Name + ")"
	}
	marker := render.Plain(" ")
	if state.active {
		marker = render.Coloured("*", "green")
	}
	name := profile.Name
	if state.isDefault {
		name += " (default)"
	}
	return []render.Cell{render.Plain("microsoft"), marker, render.Plain(name), render.Plain(target),
		render.Plain(profile.Auth), signedInCell(profile.Auth, state.signedIn), render.Plain(profile.Description)}
}

func signedInCell(auth string, signedIn *bool) render.Cell {
	// Only azure-cli profiles depend on an az session; the others bring their own.
	switch {
	case auth != "azure-cli":
		return render.Plain("n/a")
	case signedIn == nil:
		return render.Plain("?")
	case *signedIn:
		return render.Coloured("yes", "green")
	}
	return render.Coloured("no", "yellow")
}

func microsoftRecord(profile microsoft.Profile, state profileState) map[string]any {
	var subscription any
	if profile.SubscriptionID != "" {
		subscription = profile.SubscriptionID
	}
	var signedIn any
	if state.signedIn != nil {
		signedIn = *state.signedIn
	}
	return map[string]any{
		"vendor": "microsoft", "name": profile.Name, "kind": profile.Kind(), "tenant_id": profile.TenantID,
		"subscription_id": subscription, "cloud": profile.Cloud.Name, "auth": profile.Auth,
		"description": profile.Description, "default": state.isDefault, "active": state.active, "signed_in": signedIn,
	}
}

func servicenowProfiles(rt *Runtime) ([][]render.Cell, []any, error) {
	config, err := rt.SnowConfig()
	if err != nil {
		return nil, nil, err
	}
	profiles, err := rt.SnowProfiles()
	if err != nil {
		return nil, nil, err
	}
	defaultName := ""
	if config != nil {
		defaultName = config.DefaultProfile
	}
	var rows [][]render.Cell
	var records []any
	for _, profile := range profiles {
		signedIn := snowSignedIn(rt, profile)
		method := profile.Auth
		var signIn any
		if profile.Auth == "oauth" {
			method, signIn = "oauth ("+profile.SignIn+")", profile.SignIn
		}
		name := profile.Name
		if profile.Name == defaultName {
			name += " (default)"
		}
		var signedInRecord any
		cell := render.Plain("?")
		if signedIn != nil {
			signedInRecord = *signedIn
			cell = render.Coloured("no", "yellow")
			if *signedIn {
				cell = render.Coloured("yes", "green")
			}
		}
		records = append(records, map[string]any{"vendor": "servicenow", "name": profile.Name, "instance": profile.Instance,
			"auth": profile.Auth, "sign_in": signIn, "description": profile.Description, "default": profile.Name == defaultName,
			"signed_in": signedInRecord})
		rows = append(rows, []render.Cell{render.Plain("servicenow"), render.Plain(" "), render.Plain(name), render.Plain(profile.Host()),
			render.Plain(method), cell, render.Plain(profile.Description)})
	}
	return rows, records, nil
}

// snowSignedIn is whether a command could sign in now, without asking: a kept sign-in,
// or a password. nil when it is not known.
func snowSignedIn(rt *Runtime, profile servicenow.Profile) *bool {
	yes, no := true, false
	switch {
	case profile.Auth == "basic":
		found := rt.Env(profile.PasswordEnv) != ""
		return &found
	case profile.TokenCache == "keychain":
		return nil // reading the keychain can prompt; not worth it for a listing
	}
	credential, err := rt.SnowCredential(profile)
	if err != nil {
		return &no
	}
	if oauth, ok := credential.(*servicenow.OAuth); ok && oauth.HasKeptSignIn() {
		return &yes
	}
	return &no
}
