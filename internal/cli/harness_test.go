package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/azfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/clockfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// The tenant and subscription the test config's profiles are for.
const (
	testTenant       = "11111111-1111-1111-1111-111111111111"
	testSubscription = "22222222-2222-2222-2222-222222222222"
	otherTenant      = "33333333-3333-3333-3333-333333333333"
)

// testNow is the fake clock every test runs at.
var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// testConfig is a config file with a delegated and an Azure CLI profile.
var testConfig = `
[microsoft]
default_profile = "dev"

[microsoft.profiles.dev]
tenant_id = "` + testTenant + `"
subscription_id = "` + testSubscription + `"
auth = "azure-cli"
description = "Development"

[microsoft.profiles.other]
tenant_id = "` + otherTenant + `"
auth = "device-code"
token_cache = "memory"
`

// harness is one run's fakes: HTTP answered by routes, az by a fake, tokens by a fake
// provider, and stdout and stderr captured.
type harness struct {
	t         *testing.T
	rt        *Runtime
	out, err  *bytes.Buffer
	transport *httpfake.Transport
	tokens    *tokenfake.Provider
	az        *azfake.Runner
	clock     *clockfake.Clock
	stdin     string
	env       map[string]string
}

// newHarness is a harness whose HTTP calls go to handler, with the test config.
func newHarness(t *testing.T, handler httpfake.Handler) *harness {
	t.Helper()
	off := false
	colour.Use(&off)
	// Times show in local time: every test runs in UTC, wherever it runs.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { colour.Use(nil); time.Local = local })
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if handler == nil {
		handler = httpfake.Routes()
	}
	client, transport := httpfake.Client(handler)
	h := &harness{t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, transport: transport,
		tokens: &tokenfake.Provider{Claims: userClaims(testTenant)}, az: azfake.SignedIn(
			azfake.Account(testSubscription, testTenant, "Dev", "ana@corp.example", true))}
	env := map[string]string{}
	h.clock = clockfake.New(testNow)
	h.env = env
	h.rt = &Runtime{
		ConfigPath: path, HTTPClient: client, AzRunner: h.az, LookPath: azfake.LookPath,
		Getenv:     func(name string) string { return env[name] },
		Credential: func(microsoft.Profile) (auth.TokenProvider, error) { return h.tokens, nil },
		TokenStore: tokenstore.NewMemory(),
		Now:        h.clock.Now,
		Sleep:      h.clock.Sleep,
	}
	return h
}

// config replaces the config file's text.
func (h *harness) config(text string) {
	h.t.Helper()
	if err := os.WriteFile(h.rt.ConfigPath, []byte(text), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// run runs a command line, and is its exit code; its output is in out and err.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.out.Reset()
	h.err.Reset()
	h.rt.Console = &render.Console{Out: h.out, Err: h.err, Width: 0, Now: h.rt.Now}
	h.rt.Stdin = strings.NewReader(h.stdin)
	h.rt.StdinIsTerminal = h.stdin == ""
	// Each run starts afresh, as a new process would.
	h.rt.loaded, h.rt.file, h.rt.fileErr, h.rt.ms, h.rt.tokens, h.rt.snow = false, nil, nil, nil, nil, snowState{}
	return Execute(h.rt, args)
}

// ok runs a command line that must succeed, and is its stdout.
func (h *harness) ok(args ...string) string {
	h.t.Helper()
	if code := h.run(args...); code != 0 {
		h.t.Fatalf("%v exited %d\nstdout:\n%s\nstderr:\n%s", args, code, h.out, h.err)
	}
	return h.out.String()
}

// fails runs a command line that must exit code, and is its stderr.
func (h *harness) fails(code int, args ...string) string {
	h.t.Helper()
	if got := h.run(args...); got != code {
		h.t.Fatalf("%v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, got, code, h.out, h.err)
	}
	return h.err.String()
}

// userClaims are a delegated Graph token's claims in tenant, valid at testNow.
func userClaims(tenant string) map[string]any {
	return map[string]any{
		"aud": "https://graph.microsoft.com", "tid": tenant, "iss": "https://sts.windows.net/" + tenant + "/",
		"upn": "ana@corp.example", "appid": "04b07795-8ddb-461a-bbee-02f9e1bf7b46",
		"app_displayname": "Microsoft Azure CLI", "scp": "User.Read.All Directory.Read.All",
		"iat": float64(testNow.Add(-5 * time.Minute).Unix()), "exp": float64(testNow.Add(time.Hour).Unix()),
		"idtyp": "user",
	}
}

// appClaims are an application Graph token's claims.
func appClaims(tenant string, roles ...any) map[string]any {
	return map[string]any{
		"aud": "00000003-0000-0000-c000-000000000000", "tid": tenant,
		"iss": "https://sts.windows.net/" + tenant + "/", "appid": "44444444-4444-4444-4444-444444444444",
		"roles": roles, "exp": float64(testNow.Add(time.Hour).Unix()), "idtyp": "app",
	}
}

// contains fails the test unless text holds every one of wanted.
func contains(t *testing.T, text string, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

// usageError is the message of a usage error, without the colour or wrapping a terminal
// might add.
func usageError(stderr string) string {
	return strings.Join(strings.Fields(colour.Strip(stderr)), " ")
}
