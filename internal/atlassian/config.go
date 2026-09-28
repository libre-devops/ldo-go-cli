// Package atlassian is Atlassian Cloud: the shared layer Jira and Confluence build on.
// Its config section ([atlassian]: sites and the account to read each as), the API token
// credential, and the client. It depends only on core.
package atlassian

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// The [atlassian] config section: Atlassian Cloud sites, for Jira and Confluence.
//
// Each profile is one site and the account on it. Jira and Confluence share a site, and
// both read with the account's API token, sent with its email as HTTP Basic: the token
// reads whatever that account can, and nothing more. The token never goes in the file: it
// comes from an environment variable (JIRA_TOKEN unless a profile names another). Without
// an [atlassian] section, JIRA_INSTANCE, JIRA_EMAIL and JIRA_TOKEN make a profile called
// env.

// Section is the config file's section for Atlassian.
const Section = "atlassian"

// The environment's own profile, and the variables it comes from.
const (
	EnvProfile      = "env"
	SiteEnv         = "JIRA_INSTANCE"
	EmailEnv        = "JIRA_EMAIL"
	TokenEnv        = "JIRA_TOKEN"
	PlaceholderSite = "https://your-site.atlassian.net"
)

var (
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	siteName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	email       = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	sectionKeys = []string{"default_profile", "profiles"}
	profileKeys = []string{"description", "site", "email", "token_env"}
)

// ConfigTemplate is the [atlassian] section config init writes.
var ConfigTemplate = `# Atlassian Cloud sites, for Jira and Confluence. Each profile is a site and the account
# you read it as, with an API token (https://id.atlassian.com/manage-profile/security/
# api-tokens) from the environment, never this file. Without this section, ` + SiteEnv + `,
# ` + EmailEnv + ` and ` + TokenEnv + ` make a profile called "` + EnvProfile + `".
# Replace the placeholder site, then run: ` + brand.Command + ` jira whoami
[atlassian]
default_profile = "work"

[atlassian.profiles.work]
description = "Our Atlassian site"
site = "` + PlaceholderSite + `"
email = "you@example.com"
# token_env = "` + TokenEnv + `"   another variable, for a second site
`

// Profile is one Atlassian Cloud site, and the account to read it as.
type Profile struct {
	Name        string
	Site        string
	Email       string
	TokenEnv    string
	Description string
}

// Host is the site's host name, from its URL.
func (p Profile) Host() string {
	parsed, err := url.Parse(p.Site)
	if err != nil {
		return p.Site
	}
	return parsed.Host
}

// RequireRealSite is a Config error when the site is still the template's placeholder.
func (p Profile) RequireRealSite() error {
	if p.Site == PlaceholderSite {
		return errs.Configf("Atlassian profile %q still has the template's placeholder site", p.Name).
			WithHint("set its site in the config file")
	}
	return nil
}

// Config is the parsed [atlassian] section.
type Config struct {
	Path           string
	Profiles       map[string]Profile
	DefaultProfile string
}

// Get is the profile called name, or a Config error listing those there are.
func (c *Config) Get(name string) (Profile, error) {
	if profile, ok := c.Profiles[name]; ok {
		return profile, nil
	}
	listed := strings.Join(c.Names(), ", ")
	if listed == "" {
		listed = "none"
	}
	err := errs.Configf("unknown Atlassian profile %q (configured: %s)", name, listed)
	if c.Path != "" {
		err = err.WithHint("edit %s", c.Path)
	}
	return Profile{}, err
}

// Names are the profiles' names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SiteURL is https://NAME.atlassian.net, or another https URL, without a trailing slash.
// A bare site name (contoso) means https://contoso.atlassian.net.
func SiteURL(value, where string) (string, error) {
	value = strings.TrimSpace(value)
	if siteName.MatchString(value) {
		return "https://" + value + ".atlassian.net", nil
	}
	parsed, err := url.Parse(value)
	// The token goes with every request, so plain http is never acceptable.
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errs.Configf("%s: site must be an https:// URL or a site name", where)
	}
	if strings.Trim(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errs.Configf("%s: site must be the site's address, with no path", where)
	}
	return "https://" + strings.ToLower(parsed.Host), nil
}

// FromFile validates the [atlassian] section; nil when the file has none.
func FromFile(file *config.File) (*Config, error) {
	section, err := file.Section(Section)
	if err != nil || section == nil {
		return nil, err
	}
	where := file.Path + ": [" + Section + "]"
	if err := config.RejectUnknown(section, sectionKeys, where); err != nil {
		return nil, err
	}
	parsed := &Config{Path: file.Path, Profiles: map[string]Profile{}}
	if raw, present := section["profiles"]; present {
		profiles, err := config.AsTable(raw, where+".profiles")
		if err != nil {
			return nil, err
		}
		for name, value := range profiles {
			if parsed.Profiles[name], err = parseProfile(name, value, file.Path); err != nil {
				return nil, err
			}
		}
	}
	if parsed.DefaultProfile, err = config.Text(section, "default_profile", where); err != nil {
		return nil, err
	}
	if _, ok := parsed.Profiles[parsed.DefaultProfile]; parsed.DefaultProfile != "" && !ok {
		return nil, errs.Configf("%s: default_profile %q is not a configured profile", where, parsed.DefaultProfile)
	}
	return parsed, nil
}

// ProfileFromEnv is the env profile from JIRA_INSTANCE and JIRA_EMAIL, or false without a
// site.
func ProfileFromEnv(getenv func(string) string) (Profile, bool, error) {
	site := strings.TrimSpace(getenv(SiteEnv))
	if site == "" {
		return Profile{}, false, nil
	}
	address := strings.TrimSpace(getenv(EmailEnv))
	if address == "" {
		return Profile{}, false, errs.Configf("%s is set but %s is not", SiteEnv, EmailEnv).
			WithHint("set %s to the Atlassian account's email, which goes with its token", EmailEnv)
	}
	siteURL, err := SiteURL(site, SiteEnv)
	if err != nil {
		return Profile{}, false, err
	}
	checked, err := checkEmail(address, EmailEnv)
	if err != nil {
		return Profile{}, false, err
	}
	return Profile{Name: EnvProfile, Site: siteURL, Email: checked, TokenEnv: TokenEnv, Description: "from the environment"}, true, nil
}

func parseProfile(name string, value any, path string) (Profile, error) {
	where := path + ": [" + Section + ".profiles." + name + "]"
	if err := config.CheckName(name, where); err != nil {
		return Profile{}, err
	}
	body, err := config.AsTable(value, where)
	if err != nil {
		return Profile{}, err
	}
	if err := config.RejectUnknown(body, profileKeys, where); err != nil {
		return Profile{}, err
	}
	given := map[string]string{}
	for _, key := range profileKeys {
		if given[key], err = config.Text(body, key, where); err != nil {
			return Profile{}, err
		}
	}
	if given["site"] == "" || given["email"] == "" {
		return Profile{}, errs.Configf("%s: needs a site and an email", where)
	}
	tokenEnv := given["token_env"]
	if tokenEnv == "" {
		tokenEnv = TokenEnv
	}
	if !envName.MatchString(tokenEnv) {
		return Profile{}, errs.Configf("%s: token_env must be an environment variable's name", where)
	}
	site, err := SiteURL(given["site"], where)
	if err != nil {
		return Profile{}, err
	}
	address, err := checkEmail(given["email"], where)
	if err != nil {
		return Profile{}, err
	}
	return Profile{Name: name, Site: site, Email: address, TokenEnv: tokenEnv, Description: given["description"]}, nil
}

func checkEmail(value, where string) (string, error) {
	value = strings.TrimSpace(value)
	if !email.MatchString(value) {
		return "", errs.Configf("%s: '%s' is not an email address", where, value)
	}
	return value, nil
}
