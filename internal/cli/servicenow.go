package cli

import (
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow/instance"
)

// The run's ServiceNow state: profiles, sign-ins and clients, made as they are needed.

// snowState is each profile's credential and Table API client, made once in a run.
type snowState struct {
	credentials map[string]servicenow.Credential
	tables      map[string]*servicenow.Tables
}

// SnowConfig is the [servicenow] section, or nil without a config file or section.
func (r *Runtime) SnowConfig() (*servicenow.Config, error) {
	file, err := r.OptionalConfigFile()
	if err != nil || file == nil {
		return nil, err
	}
	return servicenow.FromFile(file)
}

// SnowProfiles are every profile: the configured ones, and env when SNOW_INSTANCE_URL is
// set.
func (r *Runtime) SnowProfiles() ([]servicenow.Profile, error) {
	config, err := r.SnowConfig()
	if err != nil {
		return nil, err
	}
	var found []servicenow.Profile
	if config != nil {
		for _, name := range config.Names() {
			found = append(found, config.Profiles[name])
		}
	}
	env, fromEnv, err := servicenow.ProfileFromEnv(r.Env)
	if err != nil {
		return nil, err
	}
	if fromEnv && (config == nil || config.Profiles[servicenow.EnvProfile].Name == "") {
		found = append(found, env)
	}
	return found, nil
}

// SnowProfile is the named profile (from -p or LDO_SNOW_PROFILE), else default_profile,
// else the env one, else the only one there is.
func (r *Runtime) SnowProfile(name string) (servicenow.Profile, error) {
	if name == "" {
		name = r.Env(brand.EnvVar("SNOW_PROFILE"))
	}
	selected, err := r.chooseSnowProfile(name)
	if err != nil {
		return servicenow.Profile{}, err
	}
	return selected, selected.RequireRealInstance()
}

func (r *Runtime) chooseSnowProfile(name string) (servicenow.Profile, error) {
	config, err := r.SnowConfig()
	if err != nil {
		return servicenow.Profile{}, err
	}
	env, fromEnv, err := servicenow.ProfileFromEnv(r.Env)
	if err != nil {
		return servicenow.Profile{}, err
	}
	switch {
	case name != "" && config != nil && config.Profiles[name].Name != "":
		return config.Profiles[name], nil
	case name == servicenow.EnvProfile && fromEnv:
		return env, nil
	case name != "" && config != nil:
		return config.Get(name) // an error, listing the profiles there are
	case name != "":
		return servicenow.Profile{}, errs.Configf("unknown ServiceNow profile %q", name).WithHint("%s", snowSetupHint())
	case config != nil && config.DefaultProfile != "":
		return config.Get(config.DefaultProfile)
	case fromEnv:
		return env, nil
	case config != nil && len(config.Profiles) == 1:
		return config.Get(config.Names()[0])
	}
	return servicenow.Profile{}, errs.Configf("no ServiceNow profile selected").WithHint("%s", snowSetupHint())
}

// SnowCredential is the profile's credential, made once in a run and reused.
func (r *Runtime) SnowCredential(profile servicenow.Profile) (servicenow.Credential, error) {
	if credential, ok := r.snow.credentials[profile.Name]; ok {
		return credential, nil
	}
	client, err := r.HTTP()
	if err != nil {
		return nil, err
	}
	options := servicenow.CredentialOptions{
		Getenv: r.Env, Store: r.TokenStore, HTTPClient: client, Now: r.Clock, Notify: r.Console.Notify,
		SignInHint: "run " + brand.Suggest("snow sign-in -p "+profile.Name), HasBrowser: r.hasBrowser, OpenBrowser: r.openBrowser,
	}
	if r.interactive() {
		options.Ask = r.ask
	}
	credential, err := servicenow.CredentialFor(profile, options)
	if err != nil {
		return nil, err
	}
	if r.snow.credentials == nil {
		r.snow.credentials = map[string]servicenow.Credential{}
	}
	r.snow.credentials[profile.Name] = credential
	return credential, nil
}

// SnowTables is a Table API client for the profile, signed in by its credential.
func (r *Runtime) SnowTables(profile servicenow.Profile) (*servicenow.Tables, error) {
	if tables, ok := r.snow.tables[profile.Name]; ok {
		return tables, nil
	}
	credential, err := r.SnowCredential(profile)
	if err != nil {
		return nil, err
	}
	client, err := r.HTTP()
	if err != nil {
		return nil, err
	}
	tables, err := servicenow.NewTables(profile.Instance, credential.Source(), credential.Scheme(), client)
	if err != nil {
		return nil, err
	}
	if r.snow.tables == nil {
		r.snow.tables = map[string]*servicenow.Tables{}
	}
	r.snow.tables[profile.Name] = tables
	return tables, nil
}

// SnowOAuth is the profile's OAuth credential, or a Config error for a basic profile,
// which has no token.
func (r *Runtime) SnowOAuth(profile servicenow.Profile, doing string) (*servicenow.OAuth, error) {
	credential, err := r.SnowCredential(profile)
	if err != nil {
		return nil, err
	}
	oauth, ok := credential.(*servicenow.OAuth)
	if !ok {
		return nil, errs.Configf("ServiceNow profile %q uses basic sign-in, %s", profile.Name, doing).WithHint("%s", snowOAuthHint(profile))
	}
	return oauth, nil
}

// SnowInstance is an instance client for the profile.
func (r *Runtime) SnowInstance(profile servicenow.Profile) (*instance.Client, error) {
	tables, err := r.SnowTables(profile)
	if err != nil {
		return nil, err
	}
	return &instance.Client{Tables: tables}, nil
}

// snowOAuthHint is how to move a profile from basic sign-in to OAuth.
func snowOAuthHint(profile servicenow.Profile) string {
	if profile.Name == servicenow.EnvProfile {
		return "set " + servicenow.ClientIDEnv + " to the application registry entry's client id (and " +
			servicenow.ClientSecretEnv + ", or let sign-in ask for it)"
	}
	return `set auth = "oauth" and a client_id on the profile (see ` + brand.Docs("servicenow") + ")"
}

func snowSetupHint() string {
	return "set " + servicenow.URLEnv + ", or add a [servicenow] profile (" + brand.Suggest("config init") + " writes a template)"
}
