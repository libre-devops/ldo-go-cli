// Package network decides, for corporate networks, which proxy each call goes through and
// what it trusts.
//
// Every HTTPS call this tool makes, and every az it runs, follows one set of rules:
//
//  1. Loopback and link-local addresses never use a proxy: a sign-in's redirect to
//     localhost, and the managed identity endpoint (169.254.169.254).
//  2. A host that no_proxy (the config file's) or NO_PROXY lists goes direct.
//  3. Otherwise the proxy is LDO_PROXY_ADDRESS, else the config file's proxy, else
//     HTTPS_PROXY / HTTP_PROXY / ALL_PROXY, else none.
//
// A proxy that wants a sign-in of its own (NTLM or Kerberos) is reached through a local
// one that handles it, such as cntlm or Px: LDO_PROXY_ADDRESS=127.0.0.1:3128. An address
// without a scheme is taken as http://, which is how those speak.
//
// A proxy address may carry a password. It is used as it is, but never shown: anything
// written for a person goes through Redact.
package network

import (
	"net"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// ProxyEnv names the proxy for this tool alone, winning over every other setting.
var ProxyEnv = brand.EnvVar("PROXY_ADDRESS")

// AlwaysDirect are names and addresses that are this machine, or a cloud host's metadata.
var AlwaysDirect = []string{"localhost", "127.0.0.1", "::1", "169.254.169.254"}

var directNetworks = mustNetworks("127.0.0.0/8", "169.254.0.0/16", "::1/128", "fe80::/10")

func mustNetworks(cidrs ...string) []*net.IPNet {
	var found []*net.IPNet
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		found = append(found, network)
	}
	return found
}

// Settings is what the config file says about the network; the environment adds the rest.
type Settings struct {
	Proxy    string
	NoProxy  []string
	CABundle string
	// Getenv reads the environment; nil is os.Getenv. Tests give their own.
	Getenv func(string) string
}

func (s Settings) getenv(name string) string {
	if s.Getenv != nil {
		return s.Getenv(name)
	}
	return os.Getenv(name)
}

// Route is how a call to a URL goes: through Proxy (empty for direct), and why.
type Route struct {
	Proxy string
	// Source is LDO_PROXY_ADDRESS, config, HTTPS_PROXY (or the variable it came from),
	// no_proxy, local or none.
	Source string
}

// Shown is the proxy as it may be shown: without its password.
func (r Route) Shown() string { return Redact(r.Proxy) }

// Redact is address safe to show: the password in http://user:password@host becomes ***.
// The user name stays, since it helps to see which account a proxy is given.
func Redact(address string) string {
	if address == "" {
		return address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.User == nil {
		return address
	}
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return address
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "***")
	return strings.Replace(parsed.String(), "%2A%2A%2A", "***", 1)
}

// NormaliseProxy is value as a proxy URL: 127.0.0.1:3128 becomes http://127.0.0.1:3128.
func NormaliseProxy(value, where string) (string, error) {
	text := strings.TrimSpace(value)
	if !strings.Contains(text, "://") {
		text = "http://" + text
	}
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", errs.Configf("%s: %q is not a proxy address", where, Redact(text)).
			WithHint("use host:port or http://host:port, e.g. 127.0.0.1:3128 for cntlm")
	}
	return strings.TrimRight(text, "/"), nil
}

// SplitList reads a no_proxy value: comma-separated entries.
func SplitList(value string) []string {
	var found []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			found = append(found, item)
		}
	}
	return found
}

// NoProxy is every entry that sends a host direct: the config file's, then NO_PROXY's.
func (s Settings) NoProxyEntries() []string {
	fromEnv := s.getenv("NO_PROXY")
	if fromEnv == "" {
		fromEnv = s.getenv("no_proxy")
	}
	var found []string
	for _, entry := range slices.Concat(s.NoProxy, SplitList(fromEnv)) {
		if !slices.Contains(found, entry) {
			found = append(found, entry)
		}
	}
	return found
}

// Bypassed reports whether host matches a no_proxy entry: *, a name or .suffix, a
// *.suffix, an address, or a network such as 10.0.0.0/8. A port is ignored.
func Bypassed(host string, entries []string) bool {
	host = strings.TrimRight(strings.ToLower(strings.Trim(host, "[]")), ".")
	address := net.ParseIP(host)
	for _, raw := range entries {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") && address != nil {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(address) {
				return true
			}
			continue
		}
		entry = strings.Trim(strings.TrimPrefix(withoutPort(entry), "*"), ".[]")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func withoutPort(entry string) string {
	if strings.HasPrefix(entry, "[") { // [::1]:8080
		head, _, _ := strings.Cut(entry, "]")
		return head + "]"
	}
	index := strings.LastIndex(entry, ":")
	if index < 0 || strings.Contains(entry[:index], ":") {
		return entry
	}
	if strings.Trim(entry[index+1:], "0123456789") == "" && index+1 < len(entry) {
		return entry[:index]
	}
	return entry
}

// IsAlwaysDirect reports whether host is this machine or a link-local address, such as
// the metadata endpoint.
func IsAlwaysDirect(host string) bool {
	host = strings.TrimRight(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	if address == nil {
		return false
	}
	for _, network := range directNetworks {
		if network.Contains(address) {
			return true
		}
	}
	return false
}

// Configured is the proxy the settings name for scheme, before any host is considered.
func (s Settings) Configured(scheme string) (Route, error) {
	if explicit := strings.TrimSpace(s.getenv(ProxyEnv)); explicit != "" {
		proxy, err := NormaliseProxy(explicit, ProxyEnv)
		return Route{proxy, ProxyEnv}, err
	}
	if s.Proxy != "" {
		proxy, err := NormaliseProxy(s.Proxy, "config: proxy")
		return Route{proxy, "config"}, err
	}
	for _, name := range []string{scheme + "_proxy", "all_proxy"} {
		value := s.getenv(strings.ToUpper(name))
		if value == "" {
			value = s.getenv(name)
		}
		if value != "" {
			proxy, err := NormaliseProxy(value, strings.ToUpper(name))
			return Route{proxy, strings.ToUpper(name)}, err
		}
	}
	return Route{"", "none"}, nil
}

// RouteFor is how a call to address goes, by the rules above.
func (s Settings) RouteFor(address string) (Route, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return Route{}, errs.Inputf("%q is not a URL", address)
	}
	host := parsed.Hostname()
	if IsAlwaysDirect(host) {
		return Route{"", "local"}, nil
	}
	if Bypassed(host, s.NoProxyEntries()) {
		return Route{"", "no_proxy"}, nil
	}
	return s.Configured(parsed.Scheme)
}

// SubprocessEnv is what another program (the Azure CLI) needs in its environment to
// follow the same rules: the proxy this tool would use, and the CA bundle, unless the
// environment names one already. KEY=VALUE pairs, for exec.Cmd.Env.
func (s Settings) SubprocessEnv() []string {
	var env []string
	proxy := s.Proxy
	if explicit := strings.TrimSpace(s.getenv(ProxyEnv)); explicit != "" {
		proxy = explicit
	}
	if proxy != "" {
		if normalised, err := NormaliseProxy(proxy, ProxyEnv); err == nil {
			direct := slices.Concat(AlwaysDirect, s.NoProxyEntries())
			env = append(env, "HTTPS_PROXY="+normalised, "HTTP_PROXY="+normalised,
				"NO_PROXY="+strings.Join(slices.Compact(direct), ","))
		}
	}
	if s.getenv("REQUESTS_CA_BUNDLE") == "" {
		if bundle := s.explicitBundle(); bundle != "" {
			env = append(env, "REQUESTS_CA_BUNDLE="+bundle)
		} else if s.CABundle != "" {
			env = append(env, "REQUESTS_CA_BUNDLE="+s.AzureCLIBundle())
		}
	}
	return env
}
