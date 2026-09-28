package microsoft

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
)

// Microsoft profiles are the [microsoft] section of the config file. A profile is a
// tenant, optionally pinned to one subscription, in one cloud, with one way of getting
// tokens. Tenant and subscription ids describe an environment, not code, so they live in
// the config file; a client secret never does, and is read from the environment.

// Section is the config file's section for Microsoft.
const Section = "microsoft"

// PlaceholderID is the template's all-zero id, still to be replaced.
const PlaceholderID = "00000000-0000-0000-0000-000000000000"

// AuthMethods are how a profile gets tokens.
var AuthMethods = []string{"azure-cli", "interactive", "device-code", "client-secret", "workload-identity", "managed-identity"}

// DelegatedAuth are the methods whose sign-in comes with a refresh token this tool keeps.
var DelegatedAuth = []string{"interactive", "device-code"}

// needsClientID are the methods that need a client_id. A delegated sign-in without one
// uses the Azure CLI's own public client, as Microsoft's Go SDK does: pure Go, with no
// az needed.
var needsClientID = []string{"client-secret", "workload-identity"}

var sectionKeys = []string{"default_profile", "profiles"}
var profileKeys = []string{"description", "tenant_id", "subscription_id", "cloud", "mde_url", "auth",
	"client_id", "workspace", "workspace_id", "token_cache"}

// ConfigTemplate is the section config init writes.
var ConfigTemplate = fmt.Sprintf(`# Microsoft profiles. Each is a tenant, optionally pinned to a subscription.
# Replace the placeholder ids, then run: %[1]s profiles
[microsoft]
# Used when neither --profile nor %[2]s is given.
default_profile = "prod-tenant"

[microsoft.profiles.prod]
description = "Production subscription"
tenant_id = "%[3]s"
subscription_id = "%[3]s"

[microsoft.profiles.dev]
description = "Development subscription"
tenant_id = "%[3]s"
subscription_id = "%[3]s"

[microsoft.profiles.prod-tenant]
description = "Production tenant"
tenant_id = "%[3]s"

[microsoft.profiles.test-tenant]
description = "Test tenant"
tenant_id = "%[3]s"
# A regional Defender endpoint can be set per profile:
# mde_url = "https://api-eu.securitycenter.microsoft.com"

# Other optional profile keys:
#
# cloud = "public"          public (the default), usgov or china; match it with 'az cloud set'
# workspace = "law-soc"     the Log Analytics workspace 'logs' uses by default: its name,
#                           its resource id, or its Workspace ID (the GUID on its Overview page)
#
# auth picks how the profile gets tokens. The default is your Azure CLI sign-in.
# auth = "device-code"         signs you in with a code to enter, pure Go, no az needed: for
#                              SSH, WSL and headless use. client_id is your own public client
#                              app, for delegated scopes the Azure CLI lacks (PIM); without
#                              it, the Azure CLI's own public client is used
# auth = "interactive"         the same, in a browser
# auth = "client-secret"       needs client_id; the secret is read from AZURE_CLIENT_SECRET
# auth = "workload-identity"   needs client_id; the federated token is read from
#                              AZURE_FEDERATED_TOKEN_FILE, or requested from GitHub Actions
# auth = "managed-identity"    client_id is optional and picks a user-assigned identity
# client_id = "<app or identity client id>"
#
# For interactive and device-code, token_cache says where the sign-in is kept between
# commands, so you are not asked to sign in for each one:
# token_cache = "file"         the default: a plaintext file only your account can read
#                              (0600), which works headless
# token_cache = "memory"       nowhere; each command signs in afresh
`, brand.Command, brand.ProfileEnv, PlaceholderID)

// Profile is one Azure context: a tenant, optionally pinned to a subscription.
type Profile struct {
	Name           string
	TenantID       string
	SubscriptionID string
	Description    string
	Cloud          Cloud
	// MDEURL is a regional Defender endpoint; the token is still for the cloud's Defender.
	MDEURL   string
	Auth     string
	ClientID string
	// Workspace is the Log Analytics workspace by any of its names; WorkspaceID the older
	// key, the Workspace ID alone. At most one of them is set.
	Workspace   string
	WorkspaceID string
	TokenCache  string
}

// Kind is "subscription" when pinned to a subscription, otherwise "tenant".
func (p Profile) Kind() string {
	if p.SubscriptionID != "" {
		return "subscription"
	}
	return "tenant"
}

// HasPlaceholderIDs reports whether the profile still carries the template's zero ids.
func (p Profile) HasPlaceholderIDs() bool {
	return p.TenantID == PlaceholderID || p.SubscriptionID == PlaceholderID
}

// RequireRealIDs is a Config error while the profile still carries placeholder ids.
func (p Profile) RequireRealIDs() error {
	if p.HasPlaceholderIDs() {
		return errs.Configf("profile %q still has placeholder ids", p.Name).
			WithHint("set its tenant_id (and subscription_id) in the config file")
	}
	return nil
}

// Delegated reports whether the profile signs a person in itself (interactive or device
// code), rather than through the Azure CLI or as an application.
func (p Profile) Delegated() bool { return slices.Contains(DelegatedAuth, p.Auth) }

// Config is the parsed [microsoft] section.
// Config is the [microsoft] section: its profiles by name, and the default one.
type Config struct {
	Path           string
	Profiles       map[string]Profile
	DefaultProfile string
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

// Get is the named profile, or a Config error listing the known names.
func (c *Config) Get(name string) (Profile, error) {
	profile, ok := c.Profiles[name]
	if !ok {
		known := strings.Join(c.Names(), ", ")
		if known == "" {
			known = "none"
		}
		return Profile{}, errs.Configf("unknown Microsoft profile %q (configured: %s)", name, known).
			WithHint("edit %s", c.Path)
	}
	return profile, nil
}

// FromFile validates the [microsoft] section. Unknown keys are errors, so typos surface.
func FromFile(file *config.File) (*Config, error) {
	where := file.Path + ": [" + Section + "]"
	section, err := file.Section(Section)
	if err != nil {
		return nil, err
	}
	if section == nil {
		return nil, errs.Configf("%s has no [%s] section", file.Path, Section).
			WithHint("add [microsoft.profiles.<name>] tables; %s writes a template", brand.Suggest("config init"))
	}
	if err := config.RejectUnknown(section, sectionKeys, where); err != nil {
		return nil, err
	}
	tables, ok := section["profiles"].(map[string]any)
	if !ok || len(tables) == 0 {
		return nil, errs.Configf("%s: define at least one [microsoft.profiles.<name>] table", where)
	}
	parsed := &Config{Path: file.Path, Profiles: map[string]Profile{}}
	for name, value := range tables {
		profile, err := parseProfile(name, value, file.Path)
		if err != nil {
			return nil, err
		}
		parsed.Profiles[name] = profile
	}
	if value, present := section["default_profile"]; present {
		name, isText := value.(string)
		if _, known := parsed.Profiles[name]; !isText || !known {
			return nil, errs.Configf("%s: default_profile %v is not a configured profile", where, value)
		}
		parsed.DefaultProfile = name
	}
	return parsed, nil
}

func parseProfile(name string, value any, path string) (Profile, error) {
	where := path + ": [microsoft.profiles." + name + "]"
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
	profile := Profile{Name: name, Cloud: Public, Auth: "azure-cli", TokenCache: "file"}
	if err := readIDs(&profile, data, where); err != nil {
		return Profile{}, err
	}
	if err := readAuth(&profile, data, where); err != nil {
		return Profile{}, err
	}
	if err := readRest(&profile, data, where); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func readIDs(profile *Profile, data config.Table, where string) error {
	var err error
	if profile.TenantID, err = config.GUID(data, "tenant_id", where); err != nil {
		return err
	}
	if profile.TenantID == "" {
		return errs.Configf("%s: tenant_id is required", where)
	}
	profile.SubscriptionID, err = config.GUID(data, "subscription_id", where)
	return err
}

func readAuth(profile *Profile, data config.Table, where string) error {
	auth, err := config.Text(data, "auth", where)
	if err != nil {
		return err
	}
	if auth != "" {
		if !slices.Contains(AuthMethods, auth) {
			return errs.Configf("%s: auth must be one of %s", where, strings.Join(AuthMethods, ", "))
		}
		profile.Auth = auth
	}
	if profile.ClientID, err = config.GUID(data, "client_id", where); err != nil {
		return err
	}
	if slices.Contains(needsClientID, profile.Auth) && profile.ClientID == "" {
		return errs.Configf("%s: auth = %q needs a client_id", where, profile.Auth)
	}
	cache, err := config.Text(data, "token_cache", where)
	if err != nil || cache == "" {
		return err
	}
	if !slices.Contains(tokenstore.Caches, cache) {
		return errs.Configf("%s: token_cache must be one of %s", where, strings.Join(tokenstore.Caches, ", "))
	}
	if !profile.Delegated() {
		return errs.Configf(`%s: token_cache applies to auth = "interactive" or "device-code"`, where).
			WithHint("the Azure CLI keeps its own sign-in, and the other methods have none to keep")
	}
	profile.TokenCache = cache
	return nil
}

func readRest(profile *Profile, data config.Table, where string) error {
	var err error
	if profile.Description, err = config.Text(data, "description", where); err != nil {
		return err
	}
	cloudName, err := config.Text(data, "cloud", where)
	if err != nil {
		return err
	}
	if cloudName != "" {
		if profile.Cloud, err = GetCloud(cloudName); err != nil {
			return errs.Configf("%s: cloud must be one of %s", where, strings.Join(CloudNames(), ", "))
		}
	}
	if profile.MDEURL, err = config.HTTPSURL(data, "mde_url", where); err != nil {
		return err
	}
	return readWorkspace(profile, data, where)
}

func readWorkspace(profile *Profile, data config.Table, where string) error {
	workspace, err := config.Text(data, "workspace", where)
	if err != nil {
		return err
	}
	if workspace != "" {
		if _, both := data["workspace_id"]; both {
			return errs.Configf("%s: set workspace or workspace_id, not both", where)
		}
		ref, err := WorkspaceRefOf(workspace)
		if err != nil {
			return errs.Configf("%s: workspace: %v", where, err).WithHint("%s", errs.HintOf(err))
		}
		profile.Workspace = ref.Value
	}
	if text, isText := data["workspace_id"].(string); isText && LooksLikeResourceID(text) {
		return errs.Configf("%s: workspace_id is the workspace's Workspace ID (a GUID); that is its resource id (an ARM id)", where).
			WithHint("set it as workspace instead, which takes the resource id, the name or the Workspace ID")
	}
	profile.WorkspaceID, err = config.GUID(data, "workspace_id", where)
	return err
}
