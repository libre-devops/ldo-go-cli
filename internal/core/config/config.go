// Package config reads the config file: one TOML file, with a section per vendor.
//
// Its path is, in order: an explicit path, the LDO_CONFIG environment variable, then the
// platform config directory (~/.config/ldo/config.toml on Linux and macOS, under
// %APPDATA% on Windows). It lives outside any repository because it describes
// environments, not code, and it never holds a secret.
//
// The top level holds what every vendor shares (the network: proxy, no_proxy and
// ca_bundle). Each vendor layer reads and validates its own section ([microsoft], ...)
// with the field readers here, so every section rejects unknown keys and bad values the
// same way.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// Header is the top of the file config init writes: the shared settings, commented out.
var Header = fmt.Sprintf(`# %s (%s) configuration. Each vendor has its own section;
# every key is described in %s
#
# Behind a corporate proxy? Every HTTPS call, and the Azure CLI, go the same way. HTTPS_PROXY
# and NO_PROXY work as usual; these win over them (%s wins over both).
# proxy = "127.0.0.1:3128"                  # e.g. cntlm or Px, for a proxy that wants NTLM
# no_proxy = "localhost,.corp.example"      # hosts that go direct
#
# Certificates: the system's roots (where IT installs a TLS-inspecting proxy's root) are
# trusted. ca_bundle adds more; %s or REQUESTS_CA_BUNDLE names a bundle to use exactly
# instead.
# ca_bundle = "~/certs/proxy-ca.pem"
`, brand.DisplayName, brand.Command, brand.Docs("configuration"),
	brand.EnvVar("PROXY_ADDRESS"), brand.EnvVar("CA_BUNDLE"))

// NetworkKeys are the top-level settings every vendor shares.
var NetworkKeys = []string{"ca_bundle", "proxy", "no_proxy"}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Table is a TOML table, as decoded.
type Table = map[string]any

// File is the parsed file: the shared settings, plus each vendor's raw section.
type File struct {
	Path     string
	Data     Table
	CABundle string
	Proxy    string
	NoProxy  []string
}

// Section is a vendor's section, or nil when the file has none.
func (f *File) Section(name string) (Table, error) {
	value, present := f.Data[name]
	if !present {
		return nil, nil
	}
	section, ok := value.(map[string]any)
	if !ok {
		return nil, errs.Configf("%s: [%s] must be a table", f.Path, name)
	}
	return section, nil
}

// DefaultPath is where the config file lives when no explicit path is given.
func DefaultPath() string {
	if override := os.Getenv(brand.ConfigEnv); override != "" {
		return ExpandHome(override)
	}
	return filepath.Join(configBase(), brand.ConfigDir, "config.toml")
}

func configBase() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return appData
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// ExpandHome is path with a leading ~ made the home folder.
func ExpandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// Load reads and parses the config file: a ConfigNotFound error when it is absent.
//
// sections names the vendor sections that may appear; anything else at the top level is
// then an error, so a typo fails loudly. nil skips that check.
func Load(path string, sections []string) (*File, error) {
	if path == "" {
		path = DefaultPath()
	}
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errs.ConfigNotFoundf("config file not found: %s", path).
			WithHint("create one with %s", brand.Suggest("config init"))
	}
	if err != nil {
		return nil, errs.Configf("cannot read %s: %v", path, err)
	}
	data := Table{}
	if _, err := toml.Decode(string(text), &data); err != nil {
		return nil, errs.Configf("%s is not valid TOML: %v", path, err)
	}
	return Parse(data, path, sections)
}

// Parse validates already-decoded TOML's shared settings.
func Parse(data Table, path string, sections []string) (*File, error) {
	if sections != nil {
		allowed := slices.Concat(NetworkKeys, sections)
		if err := RejectUnknown(data, allowed, path); err != nil {
			return nil, err
		}
	}
	file := &File{Path: path, Data: data}
	caBundle, err := Text(data, "ca_bundle", path)
	if err != nil {
		return nil, errs.Configf("%s: ca_bundle must be a path string", path)
	}
	if caBundle != "" {
		file.CABundle = ExpandHome(caBundle)
	}
	if file.Proxy, err = Text(data, "proxy", path); err != nil {
		return nil, errs.Configf(`%s: proxy must be a string, e.g. "127.0.0.1:3128"`, path)
	}
	if file.NoProxy, err = List(data, "no_proxy", path); err != nil {
		return nil, err
	}
	return file, nil
}

// CheckName checks a profile or instance name: lowercase, so it is easy to type and
// complete.
func CheckName(name, where string) error {
	if !namePattern.MatchString(name) {
		return errs.Configf("%s: use lowercase letters, digits, '-' and '_' in names", where)
	}
	return nil
}

// AsTable is value when it is a TOML table, else a Config error naming where.
func AsTable(value any, where string) (Table, error) {
	table, ok := value.(map[string]any)
	if !ok {
		return nil, errs.Configf("%s: expected a table", where)
	}
	return table, nil
}

// GUID is data[key] as a lowercase GUID, empty when absent, or a Config error.
func GUID(data Table, key, where string) (string, error) {
	value, present := data[key]
	if !present {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || !util.IsGUID(text) {
		return "", errs.Configf("%s: %s must be a GUID, got %v", where, key, value)
	}
	return strings.ToLower(strings.TrimSpace(text)), nil
}

// Text is data[key] as a trimmed string, empty when absent, or a Config error.
func Text(data Table, key, where string) (string, error) {
	value, present := data[key]
	if !present {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", errs.Configf("%s: %s must be a string", where, key)
	}
	return strings.TrimSpace(text), nil
}

// HTTPSURL is data[key] as an https URL without a trailing slash, empty when absent, or
// a Config error. Tokens go with every request, so plain http is never acceptable.
func HTTPSURL(data Table, key, where string) (string, error) {
	value, err := Text(data, key, where)
	if err != nil || value == "" {
		return value, err
	}
	if !strings.HasPrefix(value, "https://") {
		return "", errs.Configf("%s: %s must be an https:// URL", where, key)
	}
	return strings.TrimRight(value, "/"), nil
}

// List is data[key] as a list of strings: a TOML array of strings, or one string of
// comma-separated values. Empty when absent.
func List(data Table, key, where string) ([]string, error) {
	value, present := data[key]
	if !present {
		return nil, nil
	}
	var parts []string
	switch v := value.(type) {
	case string:
		parts = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, errs.Configf("%s: %s must hold strings", where, key)
			}
			parts = append(parts, text)
		}
	default:
		return nil, errs.Configf("%s: %s must be a string or a list of strings", where, key)
	}
	var found []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			found = append(found, part)
		}
	}
	return found, nil
}

// RejectUnknown is a Config error naming any key in data outside allowed: most often a
// typo.
func RejectUnknown(data Table, allowed []string, where string) error {
	var unknown []string
	for key := range data {
		if !slices.Contains(allowed, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	known := slices.Clone(allowed)
	slices.Sort(known)
	return errs.Configf("%s: unknown key(s) %s (allowed: %s)", where,
		strings.Join(unknown, ", "), strings.Join(known, ", "))
}
