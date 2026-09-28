package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/network"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/certfake"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// one probes a single target with settings, no local proxy listening.
func one(t *testing.T, settings network.Settings, target Target) Result {
	t.Helper()
	if settings.Getenv == nil {
		settings.Getenv = env(nil)
	}
	prober := Prober{Settings: settings, Timeout: 5 * time.Second, Listening: func() string { return "" }}
	results, err := prober.All(context.Background(), []Target{target}, 8)
	if err != nil {
		t.Fatal(err)
	}
	return results[0]
}

func answering(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && r.Header.Get("User-Agent") != "ldo-go-network-test" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if status == http.StatusFound {
			http.Redirect(w, r, "/elsewhere", status)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func closedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func TestAnAnswerIsCheckedAgainstWhatIsExpected(t *testing.T) {
	got := one(t, network.Settings{}, Target{URL: answering(t, 200).URL})
	if !got.OK || got.Status != 200 || got.Detail != "HTTP 200" || got.Route.Source != "local" {
		t.Errorf("%+v", got)
	}
	unauthorised := answering(t, 401).URL
	if got := one(t, network.Settings{}, Target{URL: unauthorised}); got.OK {
		t.Errorf("%+v", got)
	}
	expect := func(status int) bool { return Success(status) || status == 401 }
	if got := one(t, network.Settings{}, Target{URL: unauthorised, Expect: expect}); !got.OK || got.Hint != "" {
		t.Errorf("%+v", got)
	}
	// A redirect is an answer, not followed.
	if got := one(t, network.Settings{}, Target{URL: answering(t, 302).URL}); got.Status != 302 || got.OK {
		t.Errorf("%+v", got)
	}
}

func TestTheSecondsAreTimed(t *testing.T) {
	start := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	calls := 0
	clock := func() time.Time {
		calls++
		return start.Add(time.Duration(calls-1) * 1500 * time.Millisecond)
	}
	prober := Prober{Settings: network.Settings{Getenv: env(nil)}, Now: clock}
	results, err := prober.All(context.Background(), []Target{{URL: answering(t, 204).URL}}, 1)
	if err != nil || results[0].Seconds != 1.5 {
		t.Errorf("%+v %v", results, err)
	}
}

func TestAProxyThatWantsASignInSaysToRunOne(t *testing.T) {
	proxy := answering(t, http.StatusProxyAuthRequired)
	settings := network.Settings{Proxy: proxy.URL}
	for _, target := range []string{"https://graph.corp.example/v1.0/", "http://graph.corp.example/"} {
		got := one(t, settings, Target{URL: target})
		if got.OK || got.Status != 407 || got.Detail != "the proxy wants a sign-in of its own" || got.Route.Source != "config" {
			t.Errorf("%s: %+v", target, got)
		}
		// The proxy is local, so it is cntlm or Px that could not sign in upstream.
		if !strings.HasPrefix(got.Hint, "the local proxy at "+proxy.URL+" could not sign in") {
			t.Errorf("%s: %s", target, got.Hint)
		}
	}
	if hint := signInHint("http://proxy.corp.example:8080"); !strings.Contains(hint, "run cntlm or Px") ||
		!strings.Contains(hint, network.ProxyEnv) {
		t.Error(hint)
	}
	if got := one(t, network.Settings{}, Target{URL: proxy.URL}); got.Status != 407 || !strings.Contains(got.Hint, "run cntlm") {
		t.Errorf("%+v", got)
	}
}

func TestAProxyThatIsNotThereOrRefusesSaysSo(t *testing.T) {
	for name, proxy := range map[string]string{
		"refused": answering(t, http.StatusForbidden).URL,
		"absent":  "http://user:secret@" + closedPort(t),
	} {
		got := one(t, network.Settings{Proxy: proxy}, Target{URL: "https://graph.corp.example/"})
		if got.OK || !strings.HasPrefix(got.Detail, "cannot reach the proxy http://") || !strings.Contains(got.Hint, "cntlm, Px") {
			t.Errorf("%s: %+v", name, got)
		}
		if strings.Contains(got.Detail, "secret") {
			t.Errorf("%s: the password was shown: %s", name, got.Detail)
		}
	}
}

func TestNoWayOutSuggestsALocalProxy(t *testing.T) {
	target := Target{URL: "http://" + closedPort(t) + "/"}
	prober := Prober{Settings: network.Settings{Getenv: env(nil)}, Listening: func() string { return "127.0.0.1:3128" }}
	results, _ := prober.All(context.Background(), []Target{target}, 1)
	if got := results[0]; got.Detail != "no connection" || got.Hint != "something is listening on 127.0.0.1:3128, like cntlm or Px: try LDO_PROXY_ADDRESS=127.0.0.1:3128" {
		t.Errorf("%+v", got)
	}
	if got := one(t, network.Settings{}, target); got.Hint != "this network may need a proxy: set HTTPS_PROXY or LDO_PROXY_ADDRESS" {
		t.Errorf("%+v", got)
	}
	timedOut := Prober{Settings: network.Settings{Getenv: env(nil)}, Listening: func() string { return "" }}.
		failed(&url.Error{Op: "Get", URL: "https://graph.corp.example/", Err: context.DeadlineExceeded}, network.Route{Source: "none"})
	if timedOut.Detail != "timed out" {
		t.Errorf("%+v", timedOut)
	}
}

func TestLocalProxyListeningFindsTheFirstPortAnswering(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	_, closed, _ := net.SplitHostPort(closedPort(t))
	var closedNumber int
	for _, digit := range closed {
		closedNumber = closedNumber*10 + int(digit-'0')
	}
	if got := LocalProxyListening([]int{closedNumber, port}); got != listener.Addr().String() {
		t.Error(got)
	}
	if got := LocalProxyListening([]int{closedNumber}); got != "" {
		t.Error(got)
	}
}

// quiet drops a test server's own complaints about the handshakes meant to fail.
var quiet = log.New(io.Discard, "", 0)

// tlsServer serves certificate on 127.0.0.1.
func tlsServer(t *testing.T, certificate tls.Certificate) string {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	server.Config.ErrorLog = quiet
	server.StartTLS()
	t.Cleanup(server.Close)
	return server.URL
}

func bundle(t *testing.T, authority *certfake.Authority) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(path, authority.PEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestACertificateThatDoesNotVerifyNamesItsIssuer(t *testing.T) {
	inspection := certfake.NewAuthority(t, "Inspection Root", "Corp Example")
	later := time.Now().Add(24 * time.Hour)
	leaf := inspection.Leaf(t, later, "127.0.0.1")
	got := one(t, network.Settings{}, Target{URL: tlsServer(t, leaf)})
	if got.OK || got.Detail != "TLS: self-signed certificate in certificate chain; the certificate is issued by Inspection Root (Corp Example)" {
		t.Errorf("%+v", got)
	}
	if !strings.HasPrefix(got.Hint, "a TLS-inspecting proxy?") {
		t.Error(got.Hint)
	}
	alone := leaf
	alone.Certificate = alone.Certificate[:1]
	if got := one(t, network.Settings{}, Target{URL: tlsServer(t, alone)}); got.Detail != "TLS: unable to get local issuer certificate; the certificate is issued by Inspection Root (Corp Example)" {
		t.Errorf("%+v", got)
	}
	// httptest's own certificate signs itself.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = quiet
	server.StartTLS()
	defer server.Close()
	if got := one(t, network.Settings{}, Target{URL: server.URL}); got.Detail != "TLS: self-signed certificate; the certificate is issued by Acme Co" {
		t.Errorf("%+v", got)
	}
}

func TestACertificateFromATrustedRootCanStillFail(t *testing.T) {
	authority := certfake.NewAuthority(t, "Corp Root", "")
	settings := network.Settings{CABundle: bundle(t, authority)}
	expired := authority.Leaf(t, time.Now().Add(-time.Hour), "127.0.0.1")
	if got := one(t, settings, Target{URL: tlsServer(t, expired)}); got.Detail != "TLS: certificate has expired; the certificate is issued by Corp Root" {
		t.Errorf("%+v", got)
	}
	elsewhere := authority.Leaf(t, time.Now().Add(time.Hour), "web01.corp.example")
	if got := one(t, settings, Target{URL: tlsServer(t, elsewhere)}); got.Detail != "TLS: Hostname mismatch; the certificate is issued by Corp Root" {
		t.Errorf("%+v", got)
	}
	good := authority.Leaf(t, time.Now().Add(time.Hour), "127.0.0.1")
	if got := one(t, settings, Target{URL: tlsServer(t, good)}); !got.OK {
		t.Errorf("%+v", got)
	}
	// A bundle named in the environment is used as it is, so the hint says to add to it.
	explicit := network.Settings{Getenv: env(map[string]string{"LDO_CA_BUNDLE": bundle(t, certfake.NewAuthority(t, "Other", ""))})}
	got := one(t, explicit, Target{URL: tlsServer(t, good)})
	if !strings.HasPrefix(got.Hint, "LDO_CA_BUNDLE names ") || !strings.HasSuffix(got.Hint, "unset LDO_CA_BUNDLE to trust the OS store") {
		t.Errorf("%+v", got)
	}
}

func TestAServerNotSpeakingTLSFailsTheHandshake(t *testing.T) {
	plain := strings.Replace(answering(t, 200).URL, "http://", "https://", 1)
	if got := one(t, network.Settings{}, Target{URL: plain}); got.Detail != "TLS: the server answered in plain HTTP" {
		t.Errorf("%+v", got)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			// Read the client's hello first: closing with it unread resets the connection on
			// Windows before the client reads the answer.
			_, _ = connection.Read(make([]byte, 4096))
			_, _ = connection.Write([]byte("\x00\x01\x02\x03\x04\x05\x06\x07"))
			_ = connection.Close()
		}
	}()
	got := one(t, network.Settings{}, Target{URL: "https://" + listener.Addr().String() + "/"})
	if got.Detail != "TLS: the handshake failed" || !strings.HasPrefix(got.Hint, "a TLS-inspecting proxy?") {
		t.Errorf("%+v", got)
	}
}

func TestIssuerName(t *testing.T) {
	for _, test := range []struct{ common, organisation, want string }{
		{"Inspection Root", "Corp Example", "Inspection Root (Corp Example)"},
		{"Corp Example Root", "Corp Example", "Corp Example Root"},
		{"", "Corp Example", "Corp Example"},
		{"Root", "", "Root"},
	} {
		authority := certfake.NewAuthority(t, test.common, test.organisation)
		if got := IssuerName(authority.Certificate); got != test.want {
			t.Errorf("%q", got)
		}
	}
}

func TestAllKeepsTheOrderAndRefusesABadProxy(t *testing.T) {
	var targets []Target
	for _, status := range []int{200, 404, 204, 500, 201} {
		targets = append(targets, Target{URL: answering(t, status).URL})
	}
	results, err := Prober{Settings: network.Settings{Getenv: env(nil)}}.All(context.Background(), targets, 2)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{200, 404, 204, 500, 201} {
		if results[index].Status != want || results[index].URL != targets[index].URL {
			t.Errorf("%d: %+v", index, results[index])
		}
	}
	if results, _ := (Prober{}).All(context.Background(), nil, 8); len(results) != 0 {
		t.Error(results)
	}
	bad := Prober{Settings: network.Settings{Proxy: "ftp://proxy.corp.example", Getenv: env(nil)}}
	if _, err := bad.All(context.Background(), []Target{{URL: "https://graph.corp.example/"}}, 1); err == nil {
		t.Error("a bad proxy was taken")
	}
	missing := Prober{Settings: network.Settings{CABundle: "/nowhere.pem", Getenv: env(nil)}}
	if _, err := missing.All(context.Background(), targets, 1); err == nil {
		t.Error("a missing bundle was taken")
	}
}

// A platform that says why only in its own words gets the reason from the chain.
func TestAnUntypedVerificationErrorIsReadFromTheChain(t *testing.T) {
	authority := certfake.NewAuthority(t, "Inspection Root", "")
	now := time.Now()
	current := authority.Leaf(t, now.Add(time.Hour), "127.0.0.1")
	expired := authority.Leaf(t, now.Add(-time.Hour), "127.0.0.1")
	chain := func(certificate tls.Certificate) []*x509.Certificate {
		return []*x509.Certificate{certificate.Leaf, authority.Certificate}
	}
	for _, test := range []struct {
		chain []*x509.Certificate
		want  string
	}{
		{chain(current), "TLS: self-signed certificate in certificate chain; the certificate is issued by Inspection Root"},
		{chain(current)[:1], "TLS: unable to get local issuer certificate; the certificate is issued by Inspection Root"},
		{chain(expired), "TLS: certificate has expired; the certificate is issued by Inspection Root"},
		{nil, "TLS: the certificate did not verify"},
	} {
		verification := &tls.CertificateVerificationError{UnverifiedCertificates: test.chain, Err: errors.New("x509: not trusted")}
		if got := tlsDetail(verification, now); got != test.want {
			t.Errorf("%q", got)
		}
	}
}
