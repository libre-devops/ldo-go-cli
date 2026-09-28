// Package servicenow is ServiceNow: sign-in, the Table API, and the features built on
// them. Laid out like the Microsoft package: this shared layer (config, auth, tables,
// roles) depends on core only, and each feature (instance) on this layer.
package servicenow

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
)

// The [servicenow] config section: named instances, and how to sign in to each.
//
// Each profile is one instance and one account on it. Sign-in is OAuth by default,
// through an OAuth application registry entry on the instance: in a browser (single
// sign-on and MFA included, and headless too, by pasting back the address the browser
// lands on), or with a password once. Either way the refresh token is kept, so later
// commands sign in by themselves. basic (the password with every request) is there for
// instances that allow it.
//
// Passwords and client secrets never go in the file. They come from environment variables
// (SNOW_INSTANCE_PASSWORD and SNOW_CLIENT_SECRET unless a profile names others), or are
// asked for, hidden, when you sign in. Without a [servicenow] section, SNOW_INSTANCE_URL
// makes a profile called env.

// Section is the config file's section for ServiceNow.
const Section = "servicenow"

// The environment's own profile, and the variables it and the secrets come from.
const (
	EnvProfile      = "env"
	URLEnv          = "SNOW_INSTANCE_URL"
	UsernameEnv     = "SNOW_INSTANCE_USERNAME"
	PasswordEnv     = "SNOW_INSTANCE_PASSWORD"
	ClientIDEnv     = "SNOW_CLIENT_ID"
	ClientSecretEnv = "SNOW_CLIENT_SECRET"
	PlaceholderHost = "dev00000.service-now.com"
	// DefaultRedirectURI is where the browser is sent after an OAuth sign-in: set it as
	// the application registry entry's Redirect URL. Nothing needs to listen there; you
	// paste the address back.
	DefaultRedirectURI = "http://localhost:8765/callback"
)

// AuthMethods are oauth (a token and a kept refresh token, through an OAuth application
// registry entry) and basic (the username and password with every request).
var AuthMethods = []string{"oauth", "basic"}

// SignIns are how an oauth profile signs in when it has no kept refresh token.
var SignIns = []string{"browser", "password"}

var (
	envName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	instanceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	sectionKeys  = []string{"default_profile", "profiles"}
	profileKeys  = []string{"description", "instance", "username", "auth", "client_id", "sign_in", "redirect_uri",
		"token_cache", "password_env", "client_secret_env"}
)

// ConfigTemplate is the [servicenow] section config init writes.
var ConfigTemplate = `# ServiceNow instances. Each profile is an instance and the account you sign in with.
# Passwords and client secrets come from the environment, never this file. Without
# this section, ` + URLEnv + ` and ` + UsernameEnv + ` make a profile called "` + EnvProfile + `".
# Replace the placeholder instance, then run: ` + brand.Command + ` snow whoami
[servicenow]
default_profile = "dev"

[servicenow.profiles.dev]
description = "Personal developer instance"
instance = "https://` + PlaceholderHost + `"
# The OAuth application registry entry's client id (or ` + ClientIDEnv + `). Its secret comes
# from ` + ClientSecretEnv + `, or is asked for when you sign in and then kept.
client_id = "<client id>"
# sign_in = "browser"   the default: a link to open in any browser, where you sign in as
#                       usual (single sign-on and MFA too); paste back where it lands
# sign_in = "password"  the username and password, once (from ` + PasswordEnv + `, or asked)
# username = "admin"    needed for sign_in = "password" and auth = "basic"
# redirect_uri = "` + DefaultRedirectURI + `"   must match the entry's Redirect URL
# token_cache = "file"  where the refresh token is kept: file (the default), keychain
#                       or memory
# auth = "basic"        instead of OAuth: the password with every request, where the
#                       instance allows it
# password_env = "` + PasswordEnv + `"   another variable, for a second instance
# client_secret_env = "` + ClientSecretEnv + `"
`

// Profile is one ServiceNow instance, and the account to sign in to it with.
type Profile struct {
	Name     string
	Instance string
	// Username is empty to read SNOW_INSTANCE_USERNAME when it is needed.
	Username string
	Auth     string
	// ClientID is empty to read SNOW_CLIENT_ID when signing in.
	ClientID        string
	SignIn          string
	RedirectURI     string
	TokenCache      string
	PasswordEnv     string
	ClientSecretEnv string
	Description     string
}

// Host is the instance's host name, from its URL.
func (p Profile) Host() string {
	parsed, err := url.Parse(p.Instance)
	if err != nil {
		return p.Instance
	}
	return parsed.Host
}

// HasPlaceholder reports whether the instance is still the template's placeholder.
func (p Profile) HasPlaceholder() bool { return p.Host() == PlaceholderHost }

// RequireRealInstance is a Config error when the instance is still the template's
// placeholder.
func (p Profile) RequireRealInstance() error {
	if p.HasPlaceholder() {
		return errs.Configf("ServiceNow profile %q still has the template's placeholder instance", p.Name).
			WithHint("set its instance in the config file")
	}
	return nil
}

// Config is the parsed [servicenow] section.
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
	known := c.Names()
	listed := strings.Join(known, ", ")
	if listed == "" {
		listed = "none"
	}
	err := errs.Configf("unknown ServiceNow profile %q (configured: %s)", name, listed)
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

// InstanceURL is https://NAME.service-now.com, or another https URL, without a trailing
// slash. A bare instance name (dev12345) means https://dev12345.service-now.com.
func InstanceURL(value, where string) (string, error) {
	value = strings.TrimSpace(value)
	if instanceName.MatchString(value) {
		return "https://" + value + ".service-now.com", nil
	}
	parsed, err := url.Parse(value)
	// The password or token goes with every request, so plain http is never acceptable.
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errs.Configf("%s: instance must be an https:// URL or an instance name", where)
	}
	if strings.Trim(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errs.Configf("%s: instance must be the instance's address, with no path", where)
	}
	return "https://" + strings.ToLower(parsed.Host), nil
}

// FromFile validates the [servicenow] section; nil when the file has none.
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

// ProfileFromEnv is the env profile from SNOW_INSTANCE_URL (and friends), or false
// without it.
func ProfileFromEnv(getenv func(string) string) (Profile, bool, error) {
	address := strings.TrimSpace(getenv(URLEnv))
	if address == "" {
		return Profile{}, false, nil
	}
	instance, err := InstanceURL(address, URLEnv)
	if err != nil {
		return Profile{}, false, err
	}
	// OAuth once there is an application to sign in through; with a password to hand,
	// it signs in with that rather than a browser.
	profile := Profile{
		Name: EnvProfile, Instance: instance, Username: strings.TrimSpace(getenv(UsernameEnv)), Auth: "basic",
		SignIn: "browser", RedirectURI: DefaultRedirectURI, TokenCache: tokenstore.DefaultCache,
		PasswordEnv: PasswordEnv, ClientSecretEnv: ClientSecretEnv, Description: "from " + URLEnv,
	}
	if strings.TrimSpace(getenv(ClientIDEnv)) != "" {
		profile.Auth = "oauth"
	}
	if getenv(PasswordEnv) != "" {
		profile.SignIn = "password"
	}
	return profile, true, nil
}

// profileFields are a profile's settings as the file gives them, before they are checked.
type profileFields struct {
	instance, auth, signIn, redirect, tokenCache string
}

func parseProfile(name string, value any, path string) (Profile, error) {
	where := path + ": [" + Section + ".profiles." + name + "]"
	if err := config.CheckName(name, where); err != nil {
		return Profile{}, err
	}
	data, err := config.AsTable(value, where)
	if err != nil {
		return Profile{}, err
	}
	if err := config.RejectUnknown(data, profileKeys, where); err != nil {
		return Profile{}, err
	}
	var given profileFields
	for key, target := range map[string]*string{"instance": &given.instance, "auth": &given.auth, "sign_in": &given.signIn,
		"redirect_uri": &given.redirect, "token_cache": &given.tokenCache} {
		if *target, err = config.Text(data, key, where); err != nil {
			return Profile{}, err
		}
	}
	if err := checkProfile(given, where); err != nil {
		return Profile{}, err
	}
	return buildProfile(name, data, given, where)
}

// checkProfile is a Config error for a setting that cannot be used, or one that does not
// go with the profile's auth.
func checkProfile(given profileFields, where string) error {
	auth := or(given.auth, "oauth")
	switch {
	case given.instance == "":
		return errs.Configf("%s: instance is required", where)
	case !slices.Contains(AuthMethods, auth):
		return errs.Configf("%s: auth must be one of %s", where, strings.Join(AuthMethods, ", "))
	case given.signIn != "" && !slices.Contains(SignIns, given.signIn):
		return errs.Configf("%s: sign_in must be one of %s", where, strings.Join(SignIns, ", "))
	case given.redirect != "" && !redirectOK(given.redirect):
		return errs.Configf("%s: redirect_uri must be http://localhost... or an https:// URL", where)
	case auth == "basic" && (given.signIn != "" || given.redirect != ""):
		return errs.Configf(`%s: sign_in and redirect_uri apply to auth = "oauth"`, where)
	case given.tokenCache != "" && !slices.Contains(tokenstore.Caches, given.tokenCache):
		return errs.Configf("%s: token_cache must be one of %s", where, strings.Join(tokenstore.Caches, ", "))
	case given.tokenCache != "" && auth != "oauth":
		return errs.Configf(`%s: token_cache applies to auth = "oauth"`, where).
			WithHint("basic sign-in sends the password each time, so there is nothing to keep")
	}
	return nil
}

func buildProfile(name string, data config.Table, given profileFields, where string) (Profile, error) {
	instance, err := InstanceURL(given.instance, where)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Name: name, Instance: instance, Auth: or(given.auth, "oauth"), SignIn: or(given.signIn, "browser"),
		RedirectURI: or(given.redirect, DefaultRedirectURI), TokenCache: or(given.tokenCache, tokenstore.DefaultCache),
	}
	for key, target := range map[string]*string{"username": &profile.Username, "client_id": &profile.ClientID,
		"description": &profile.Description} {
		if *target, err = config.Text(data, key, where); err != nil {
			return Profile{}, err
		}
	}
	if profile.PasswordEnv, err = envVariable(data, "password_env", where, PasswordEnv); err != nil {
		return Profile{}, err
	}
	if profile.ClientSecretEnv, err = envVariable(data, "client_secret_env", where, ClientSecretEnv); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func redirectOK(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" && parsed.Host != "" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1")
}

// envVariable is data[key] as an environment variable's name, else fallback.
func envVariable(data config.Table, key, where, fallback string) (string, error) {
	value, err := config.Text(data, key, where)
	if err != nil {
		return "", err
	}
	if value == "" {
		return fallback, nil
	}
	if !envName.MatchString(value) {
		return "", errs.Configf("%s: %s must be an environment variable name", where, key)
	}
	return value, nil
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
