package network

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/certfake"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestRoutesFollowTheRulesInOrder(t *testing.T) {
	settings := Settings{Proxy: "cfg.proxy:8080", NoProxy: []string{".corp.example"},
		Getenv: env(map[string]string{"HTTPS_PROXY": "http://env.proxy:3128", "NO_PROXY": "10.0.0.0/8,direct.example:443"})}
	cases := map[string]Route{
		"https://localhost:8400/":             {"", "local"},
		"http://169.254.169.254/metadata":     {"", "local"},
		"https://web01.corp.example/":         {"", "no_proxy"},
		"https://10.1.2.3/":                   {"", "no_proxy"},
		"https://direct.example/":             {"", "no_proxy"},
		"https://graph.microsoft.com/v1.0/me": {"http://cfg.proxy:8080", "config"},
	}
	for address, want := range cases {
		if got, err := settings.RouteFor(address); err != nil || got != want {
			t.Fatalf("%s: got %+v, %v", address, got, err)
		}
	}
	settings.Getenv = env(map[string]string{"LDO_PROXY_ADDRESS": "127.0.0.1:3128", "HTTPS_PROXY": "http://env.proxy:3128"})
	if got, _ := settings.RouteFor("https://graph.microsoft.com"); got.Source != ProxyEnv || got.Proxy != "http://127.0.0.1:3128" {
		t.Fatalf("explicit: %+v", got)
	}
	settings = Settings{Getenv: env(map[string]string{"https_proxy": "env.proxy:3128"})}
	if got, _ := settings.RouteFor("https://graph.microsoft.com"); got.Source != "HTTPS_PROXY" {
		t.Fatalf("environment: %+v", got)
	}
	settings = Settings{Getenv: env(nil)}
	if got, _ := settings.RouteFor("https://graph.microsoft.com"); got.Source != "none" || got.Proxy != "" {
		t.Fatalf("none: %+v", got)
	}
}

func TestProxyPasswordsAreNeverShown(t *testing.T) {
	if got := Redact("http://alice:s3cret@proxy.corp.example:8080"); got != "http://alice:***@proxy.corp.example:8080" {
		t.Fatal(got)
	}
	if Redact("http://proxy:8080") != "http://proxy:8080" || Redact("") != "" {
		t.Fatal("redact without a password")
	}
	route := Route{"http://alice:s3cret@proxy:8080", "config"}
	if strings.Contains(route.Shown(), "s3cret") {
		t.Fatal(route.Shown())
	}
	if _, err := NormaliseProxy("http://alice:s3cret@", "config"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error %v", err)
	}
}

func TestBypassEntries(t *testing.T) {
	entries := []string{"*.corp.example", "[::1]:8080", "10.0.0.0/8", "not-a/network"}
	for host, want := range map[string]bool{"a.corp.example": true, "corp.example": true, "::1": true,
		"10.9.9.9": true, "11.0.0.1": false, "example.com": false} {
		if Bypassed(host, entries) != want {
			t.Fatalf("%s: want %v", host, want)
		}
	}
	if !Bypassed("anything", []string{"*"}) {
		t.Fatal("*")
	}
}

func TestTheAzureCLIGetsTheSameProxyAndBundle(t *testing.T) {
	settings := Settings{Proxy: "127.0.0.1:3128", NoProxy: []string{".corp.example"}, CABundle: "/etc/ca.pem", Getenv: env(nil)}
	got := settings.SubprocessEnv()
	for _, want := range []string{"HTTPS_PROXY=http://127.0.0.1:3128", "REQUESTS_CA_BUNDLE=/etc/ca.pem"} {
		if !slices.Contains(got, want) {
			t.Fatalf("%v lacks %s", got, want)
		}
	}
	if !strings.Contains(strings.Join(got, " "), "NO_PROXY=localhost,127.0.0.1,::1,169.254.169.254,.corp.example") {
		t.Fatal(got)
	}
	settings.Getenv = env(map[string]string{"REQUESTS_CA_BUNDLE": "/mine.pem"})
	settings.Proxy = ""
	if got := settings.SubprocessEnv(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestTheCertPoolTakesTheConfigBundle(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	os.WriteFile(bad, []byte("not a certificate"), 0o600)
	if _, err := (Settings{CABundle: bad, Getenv: env(nil)}).CertPool(); err == nil {
		t.Fatal("a bundle without certificates was taken")
	}
	if _, err := (Settings{CABundle: filepath.Join(dir, "none.pem"), Getenv: env(nil)}).CertPool(); err == nil {
		t.Fatal("a missing bundle was taken")
	}
	if _, err := (Settings{Getenv: env(nil)}).Transport(); err != nil {
		t.Fatal(err)
	}
}

func TestTrustSaysWhichBundleAndHowManyItHolds(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "corp.pem")
	os.WriteFile(bundle, append(certfake.NewAuthority(t, "Corp Root", "").PEM(), certfake.NewAuthority(t, "Other", "").PEM()...), 0o600)
	if got := (Settings{CABundle: bundle, Getenv: env(nil)}).Trust(); got != (Trust{Path: bundle, Certificates: 2}) {
		t.Errorf("%+v", got)
	}
	explicit := Settings{CABundle: bundle, Getenv: env(map[string]string{"REQUESTS_CA_BUNDLE": "/missing.pem"})}
	if got := explicit.Trust(); got != (Trust{Explicit: "REQUESTS_CA_BUNDLE", Path: "/missing.pem"}) {
		t.Errorf("%+v", got)
	}
	if got := (Settings{Getenv: env(map[string]string{"CURL_CA_BUNDLE": "/curl.pem"})}).Trust(); got != (Trust{Explicit: "CURL_CA_BUNDLE", Path: "/curl.pem"}) {
		t.Errorf("%+v", got)
	}
	if got := (Settings{Getenv: env(nil)}).Trust(); got != (Trust{}) {
		t.Errorf("%+v", got)
	}
	if _, err := (Settings{CABundle: bundle, Getenv: env(nil)}).CertPool(); err != nil {
		t.Fatal(err)
	}
}

func TestTheAzureCLIGetsThisMachinesCertificatesWithTheConfigBundle(t *testing.T) {
	folder := t.TempDir()
	machine := certfake.NewAuthority(t, "Machine Root", "").PEM()
	corporate := certfake.NewAuthority(t, "Corp Inspection", "").PEM()
	bundle := filepath.Join(folder, "corp.pem")
	_ = os.WriteFile(bundle, corporate, 0o600)
	cache := filepath.Join(folder, "cache")
	originalCertificates, originalFolder := systemCertificates, cacheFolder
	systemCertificates, cacheFolder = func() []byte { return machine }, func() (string, error) { return cache, nil }
	defer func() { systemCertificates, cacheFolder = originalCertificates, originalFolder }()
	settings := Settings{CABundle: bundle, Getenv: env(nil)}
	path := settings.AzureCLIBundle()
	data, err := os.ReadFile(path)
	if err != nil || filepath.Dir(path) != cache || countPEM(data) != 2 || !strings.Contains(string(data), string(corporate)) {
		t.Fatalf("%s: %d certificates, %v", path, countPEM(data), err)
	}
	if again := settings.AzureCLIBundle(); again != path {
		t.Error("the bundle was written again:", again)
	}
	if got := settings.SubprocessEnv(); !slices.Contains(got, "REQUESTS_CA_BUNDLE="+path) {
		t.Error(got)
	}
	// Where the cache cannot be written, the Azure CLI is given ca_bundle alone.
	cacheFolder = func() (string, error) { return filepath.Join(bundle, "not-a-folder"), nil }
	if got := settings.AzureCLIBundle(); got != bundle {
		t.Error(got)
	}
	cacheFolder = func() (string, error) { return "", os.ErrNotExist }
	if got := settings.AzureCLIBundle(); got != bundle {
		t.Error(got)
	}
	if got := (Settings{CABundle: filepath.Join(folder, "missing.pem")}).AzureCLIBundle(); got != filepath.Join(folder, "missing.pem") {
		t.Error(got)
	}
}

func TestTheLinuxSystemBundleIsFound(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bundle.pem")
	_ = os.WriteFile(file, certfake.NewAuthority(t, "Root", "").PEM(), 0o600)
	t.Setenv("SSL_CERT_FILE", file)
	if got := countPEM(linuxCertificates()); got != 1 {
		t.Error(got)
	}
	if got := countPEM(encodePEM([][]byte{certfake.NewAuthority(t, "A", "").Certificate.Raw})); got != 1 {
		t.Error(got)
	}
	_ = macOSCertificates()
	_ = windowsCertificates()
}
