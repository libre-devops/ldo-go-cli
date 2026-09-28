// Package cli is the ldo-go command: it parses, calls a client, and renders. No logic
// lives here; each command's work is in the vendor packages.
package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/network"
	"github.com/libre-devops/ldo-go-cli/internal/core/poll"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/azcli"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/identity"
)

// Sections are the vendor sections the config file may hold; anything else at its top
// level is a typo.
var Sections = []string{microsoft.Section, "servicenow", "atlassian"}

// Runtime is what every command of one run shares. Tests build one with fakes: an HTTP
// client whose transport answers for the services, an az runner, an environment, a clock.
type Runtime struct {
	ConfigPath string
	Console    *render.Console
	// Getenv reads the environment; os.Getenv when nil.
	Getenv func(string) string
	// HTTPClient sends every API call; built from the config's network settings when nil.
	HTTPClient *http.Client
	// AzRunner runs az, and Runner the other tools; process.Exec when nil.
	AzRunner process.Runner
	Runner   process.Runner
	// LookPath finds a tool on PATH; exec.LookPath when nil.
	LookPath process.LookPath
	// Credential makes a profile's token provider; identity.For when nil.
	Credential func(microsoft.Profile) (auth.TokenProvider, error)
	// TokenStore keeps delegated sign-ins; the profile's token_cache opens one when nil.
	TokenStore tokenstore.Store
	Now        func() time.Time
	// Sleep waits between a watch's passes, ending early on Ctrl-C; poll.Sleep when nil.
	Sleep func(ctx context.Context, wait time.Duration) error
	Stdin io.Reader
	// StdinIsTerminal says whether a person, rather than a pipe, is on stdin.
	StdinIsTerminal bool
	// Ask asks the person a question, hiding what they type when hide is set; a prompt on
	// the terminal when nil. Interactive says whether someone is there to answer: stdin
	// and stderr are terminals, when nil.
	Ask         func(question string, hide bool) (string, error)
	Interactive func() bool
	// HasBrowser says whether a browser can be opened here, and OpenBrowser opens one;
	// webbrowser's when nil.
	HasBrowser  func() bool
	OpenBrowser func(link string) error
	// Context is cancelled on Ctrl-C.
	Context context.Context

	mu      sync.Mutex
	file    *config.File
	loaded  bool
	fileErr error
	ms      *microsoft.Config
	tokens  map[string]auth.TokenProvider
	client  *http.Client
	snow    snowState
}

// NewRuntime is a runtime for a real run: the process's stdio and environment.
func NewRuntime() *Runtime {
	return &Runtime{Console: render.Stdio(), Stdin: os.Stdin, StdinIsTerminal: isTerminal(os.Stdin), Context: context.Background()}
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Env is one environment variable.
func (r *Runtime) Env(name string) string {
	if r.Getenv != nil {
		return r.Getenv(name)
	}
	return os.Getenv(name)
}

// Clock is now.
func (r *Runtime) Clock() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// PollClock is the clock and sleep a watch runs on.
func (r *Runtime) PollClock() poll.Clock {
	sleep := r.Sleep
	if sleep == nil {
		sleep = poll.Sleep
	}
	return poll.Clock{Now: r.Clock, Sleep: sleep}
}

// Ctx is the run's context.
func (r *Runtime) Ctx() context.Context {
	if r.Context == nil {
		return context.Background()
	}
	return r.Context
}

// ConfigFile is the config file, loaded once: a ConfigNotFound error when it is absent.
func (r *Runtime) ConfigFile() (*config.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.loaded {
		r.file, r.fileErr = config.Load(r.ConfigPath, Sections)
		r.loaded = true
	}
	return r.file, r.fileErr
}

// OptionalConfigFile is the config file, or nil when there is none (other errors still
// are errors).
func (r *Runtime) OptionalConfigFile() (*config.File, error) {
	file, err := r.ConfigFile()
	if errs.Is(err, errs.ConfigNotFound) {
		return nil, nil
	}
	return file, err
}

// ConfigPathInUse is the config file this run reads.
func (r *Runtime) ConfigPathInUse() string {
	if r.ConfigPath != "" {
		return r.ConfigPath
	}
	return config.DefaultPath()
}

// Network is what the config file says about the network, with the environment for the
// rest. A config file with a mistake in it is reported by the command that reads it;
// until then the network keeps its defaults, so a command that needs no config runs.
func (r *Runtime) Network() network.Settings {
	settings := network.Settings{Getenv: r.Getenv}
	if file, err := r.OptionalConfigFile(); err == nil && file != nil {
		settings.Proxy, settings.NoProxy, settings.CABundle = file.Proxy, file.NoProxy, file.CABundle
	}
	return settings
}

// HTTP is the client every API call goes through.
func (r *Runtime) HTTP() (*http.Client, error) {
	if r.HTTPClient != nil {
		return r.HTTPClient, nil
	}
	// The settings are read first: reading the config file takes the lock too.
	settings := r.Network()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		transport, err := settings.Transport()
		if err != nil {
			return nil, err
		}
		r.client = &http.Client{Transport: transport}
	}
	return r.client, nil
}

// Az is the Azure CLI, on this run's network.
func (r *Runtime) Az() *azcli.CLI {
	cli := azcli.New(r.AzRunner, r.Network().SubprocessEnv)
	cli.Command.LookPath = r.LookPath
	return cli
}

// Microsoft is the [microsoft] section, parsed once.
func (r *Runtime) Microsoft() (*microsoft.Config, error) {
	file, err := r.ConfigFile()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ms == nil {
		if r.ms, err = microsoft.FromFile(file); err != nil {
			return nil, err
		}
	}
	return r.ms, nil
}

// OptionalMicrosoft is the [microsoft] section, or nil with no config file or section.
func (r *Runtime) OptionalMicrosoft() (*microsoft.Config, error) {
	file, err := r.OptionalConfigFile()
	if err != nil || file == nil {
		return nil, err
	}
	if section, err := file.Section(microsoft.Section); err != nil || section == nil {
		return nil, err
	}
	return r.Microsoft()
}

// Profile is the profile to act on: name (from --profile or LDO_PROFILE) wins, then the
// section's default_profile, then the Azure CLI's active account as an unnamed profile.
func (r *Runtime) Profile(name string) (microsoft.Profile, error) {
	if name == "" {
		name = r.Env(brand.ProfileEnv)
	}
	var profile microsoft.Profile
	if name != "" {
		ms, err := r.Microsoft()
		if err != nil {
			return profile, err
		}
		if profile, err = ms.Get(name); err != nil {
			return profile, err
		}
	} else {
		ms, err := r.OptionalMicrosoft()
		if err != nil {
			return profile, err
		}
		if ms == nil || ms.DefaultProfile == "" {
			return r.activeAccountProfile()
		}
		profile = ms.Profiles[ms.DefaultProfile]
	}
	return profile, profile.RequireRealIDs()
}

func (r *Runtime) activeAccountProfile() (microsoft.Profile, error) {
	account, found, err := r.Az().Current(r.Ctx())
	if err != nil {
		return microsoft.Profile{}, err
	}
	if !found {
		return microsoft.Profile{}, errs.Commandf("no profile selected and the Azure CLI is not signed in").
			WithHint("pass --profile, set default_profile, or run %s", brand.Suggest("az use <profile>"))
	}
	profile := microsoft.Profile{Name: "az-active", TenantID: account.TenantID,
		Description: "the Azure CLI's active account", Cloud: microsoft.Public, Auth: "azure-cli", TokenCache: "file"}
	if !account.TenantLevel() {
		profile.SubscriptionID = account.ID
	}
	return profile, nil
}

// Tokens is the profile's credential, wrapped in a cache every client of the run shares.
func (r *Runtime) Tokens(profile microsoft.Profile) (auth.TokenProvider, error) {
	r.mu.Lock()
	if provider, ok := r.tokens[profile.Name]; ok {
		r.mu.Unlock()
		return provider, nil
	}
	r.mu.Unlock()
	var credential auth.TokenProvider
	var err error
	if r.Credential != nil {
		credential, err = r.Credential(profile)
	} else {
		client, httpErr := r.HTTP()
		if httpErr != nil {
			return nil, httpErr
		}
		credential, err = identity.For(profile, identity.Options{
			HTTPClient: client, Getenv: r.Getenv, Store: r.TokenStore,
			Notify: r.Console.Notify, Now: r.Now,
		})
	}
	if err != nil {
		return nil, err
	}
	provider := auth.NewCaching(credential, r.Now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tokens == nil {
		r.tokens = map[string]auth.TokenProvider{}
	}
	r.tokens[profile.Name] = provider
	return provider, nil
}

// API is the tokens, tenant, cloud and HTTP client a Microsoft feature client needs.
func (r *Runtime) API(profile microsoft.Profile) (microsoft.API, error) {
	tokens, err := r.Tokens(profile)
	if err != nil {
		return microsoft.API{}, err
	}
	client, err := r.HTTP()
	if err != nil {
		return microsoft.API{}, err
	}
	return microsoft.ForProfile(profile, tokens, client), nil
}

// ReadAll is everything on stdin.
func (r *Runtime) ReadAll() (string, error) {
	if r.Stdin == nil {
		return "", nil
	}
	data, err := io.ReadAll(r.Stdin)
	return strings.TrimRight(string(data), "\r\n"), err
}

// SignOut forgets the sign-in a delegated profile keeps: false when none was kept.
func (r *Runtime) SignOut(profile microsoft.Profile) (bool, error) {
	client, err := r.HTTP()
	if err != nil {
		return false, err
	}
	credential, err := identity.For(profile, identity.Options{HTTPClient: client, Getenv: r.Getenv,
		Store: r.TokenStore, Notify: r.Console.Notify, Now: r.Now})
	if err != nil {
		return false, err
	}
	signOut, ok := credential.(interface {
		SignOut(context.Context) (int, error)
	})
	if !ok {
		return false, nil
	}
	forgot, err := signOut.SignOut(r.Ctx())
	return forgot > 0, err
}
