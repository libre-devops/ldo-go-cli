package cli

import (
	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/atlassian/confluence"
	"github.com/libre-devops/ldo-go-cli/internal/atlassian/jira"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// The run's Atlassian state: profiles, and the Jira and Confluence clients over them.

var atlassianSetupHint = "export " + atlassian.SiteEnv + " and " + atlassian.EmailEnv + " (and the token), or add an " +
	"[atlassian] section: " + brand.Suggest("config init") + " writes one to fill in"

// AtlassianConfig is the [atlassian] section, or nil without a config file or section.
func (r *Runtime) AtlassianConfig() (*atlassian.Config, error) {
	file, err := r.OptionalConfigFile()
	if err != nil || file == nil {
		return nil, err
	}
	return atlassian.FromFile(file)
}

// AtlassianProfile is the named profile (from -p or LDO_ATLASSIAN_PROFILE), else
// default_profile, else the env one, else the only one there is.
func (r *Runtime) AtlassianProfile(name string) (atlassian.Profile, error) {
	if name == "" {
		name = r.Env(brand.EnvVar("ATLASSIAN_PROFILE"))
	}
	config, err := r.AtlassianConfig()
	if err != nil {
		return atlassian.Profile{}, err
	}
	env, fromEnv, err := atlassian.ProfileFromEnv(r.Env)
	if err != nil {
		return atlassian.Profile{}, err
	}
	switch {
	case name == atlassian.EnvProfile && fromEnv && (config == nil || config.Profiles[name].Name == ""):
		return env, nil
	case name != "" && config == nil:
		return atlassian.Profile{}, errs.Configf("unknown Atlassian profile %q", name).WithHint("%s", atlassianSetupHint)
	case name != "":
		return config.Get(name)
	case config != nil && config.DefaultProfile != "":
		return config.Get(config.DefaultProfile)
	case fromEnv:
		return env, nil
	case config != nil && len(config.Profiles) == 1:
		return config.Get(config.Names()[0])
	}
	return atlassian.Profile{}, errs.Configf("no Atlassian profile selected").WithHint("%s", atlassianSetupHint)
}

// Jira is a Jira client for the profile.
func (r *Runtime) Jira(profile atlassian.Profile) (*jira.Client, error) {
	client, err := r.HTTP()
	if err != nil {
		return nil, err
	}
	return jira.New(profile, r.Env, client)
}

// Confluence is a Confluence client for the profile.
func (r *Runtime) Confluence(profile atlassian.Profile) (*confluence.Client, error) {
	client, err := r.HTTP()
	if err != nil {
		return nil, err
	}
	return confluence.New(profile, r.Env, client)
}
