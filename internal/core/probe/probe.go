// Package probe tests the way out: one request to a URL, by the network's rules, and
// what went wrong.
//
// Probe sends an unsigned GET the way every call goes (the network package's proxy and
// certificates) and turns a failure into something a person can act on:
//
//   - the proxy wants a sign-in of its own (HTTP 407): run cntlm or Px;
//   - the proxy is not there: is it running, on that port?
//   - no way out at all: this network may need a proxy, and one may be listening locally;
//   - the certificate did not verify: a TLS-inspecting proxy, named by its certificate's
//     issuer, whose root the machine does not trust yet.
//
// The issuer comes from the certificate the server (or the proxy in the middle) presented,
// which Go hands back, unverified, with the error. It only ever describes a certificate
// for a hint; nothing here decides what is trusted.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/network"
)

// LocalProxyPorts are where cntlm and Px listen unless told otherwise, and the port next
// to it.
var LocalProxyPorts = []int{3128, 3129}

// Result is one request's outcome: whether it got an expected answer, and what to do if
// not.
type Result struct {
	URL   string
	Route network.Route
	OK    bool
	// Status is the HTTP status, or 0 when there was no answer.
	Status  int
	Detail  string
	Hint    string
	Seconds float64
}

// Target is a URL to try, and the statuses that mean it was reached.
type Target struct {
	URL    string
	Expect func(status int) bool
}

// Success is a 2xx.
func Success(status int) bool { return status >= 200 && status < 300 }

// Prober sends the probes.
type Prober struct {
	Settings network.Settings
	// Transport sends each request; nil is one built from Settings. Tests give their own.
	Transport http.RoundTripper
	Timeout   time.Duration
	// Now times each probe; time.Now when nil.
	Now func() time.Time
	// Listening is the local proxy something listens on, or ""; LocalProxyListening when
	// nil.
	Listening func() string
}

// All probes each target, workers at a time, in the order given. An error is a setting
// that cannot be used, such as a proxy address that is not one.
func (p Prober) All(ctx context.Context, targets []Target, workers int) ([]Result, error) {
	client, err := p.client()
	if err != nil {
		return nil, err
	}
	routes := make([]network.Route, len(targets))
	for index, target := range targets {
		if routes[index], err = p.Settings.RouteFor(target.URL); err != nil {
			return nil, err
		}
	}
	results := make([]Result, len(targets))
	queue := make(chan int)
	var group sync.WaitGroup
	for range max(1, min(workers, len(targets))) {
		group.Go(func() {
			for index := range queue {
				results[index] = p.probe(ctx, client, targets[index], routes[index])
			}
		})
	}
	for index := range targets {
		queue <- index
	}
	close(queue)
	group.Wait()
	return results, nil
}

// proxyRefused is the proxy's answer to CONNECT, when it was not 200.
type proxyRefused struct{ status int }

func (e *proxyRefused) Error() string {
	return fmt.Sprintf("the proxy answered CONNECT with HTTP %d", e.status)
}

func (p Prober) client() (*http.Client, error) {
	transport := p.Transport
	if transport == nil {
		built, err := p.Settings.Transport()
		if err != nil {
			return nil, err
		}
		// Go keeps only the text of a refused CONNECT; the status is what tells a proxy
		// that wants a sign-in from one that is not there.
		built.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
			if response.StatusCode != http.StatusOK {
				return &proxyRefused{status: response.StatusCode}
			}
			return nil
		}
		transport = built
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// clock is now.
func (p Prober) clock() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

func (p Prober) probe(ctx context.Context, client *http.Client, target Target, how network.Route) Result {
	started := p.clock()
	result := p.send(ctx, client, target, how)
	result.URL, result.Route = target.URL, how
	result.Seconds = math.Round(p.clock().Sub(started).Seconds()*1000) / 1000
	return result
}

func (p Prober) send(ctx context.Context, client *http.Client, target Target, how network.Route) Result {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return Result{Detail: "not a URL"}
	}
	request.Header.Set("User-Agent", brand.Command+"-network-test")
	response, err := client.Do(request)
	if err != nil {
		return p.failed(err, how)
	}
	_ = response.Body.Close()
	if response.StatusCode == http.StatusProxyAuthRequired {
		return needsProxySignIn(how)
	}
	expect := target.Expect
	if expect == nil {
		expect = Success
	}
	return Result{OK: expect(response.StatusCode), Status: response.StatusCode, Detail: fmt.Sprintf("HTTP %d", response.StatusCode)}
}

// failed is what a request that got no answer says, and what to try.
func (p Prober) failed(err error, how network.Route) Result {
	var verification *tls.CertificateVerificationError
	var refused *proxyRefused
	var operation *net.OpError
	switch {
	case errors.As(err, &verification):
		return Result{Detail: tlsDetail(verification, p.clock()), Hint: tlsHint(p.Settings.Trust())}
	case strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		// Go gives this no type of its own: a plain HTTP answer to https.
		return Result{Detail: "TLS: the server answered in plain HTTP", Hint: "check the URL is https, on the right port"}
	case isTLS(err):
		return Result{Detail: "TLS: the handshake failed", Hint: tlsHint(p.Settings.Trust())}
	case errors.As(err, &refused):
		if refused.status == http.StatusProxyAuthRequired {
			return needsProxySignIn(how)
		}
		return proxyUnreachable(how)
	case errors.As(err, &operation) && operation.Op == "proxyconnect":
		return proxyUnreachable(how)
	}
	result := Result{Detail: "no connection"}
	if timedOut(err) {
		result.Detail = "timed out"
	}
	if how.Proxy == "" {
		listening := p.Listening
		if listening == nil {
			listening = func() string { return LocalProxyListening(LocalProxyPorts) }
		}
		result.Hint = noWayOutHint(listening())
	}
	return result
}

func isTLS(err error) bool {
	var alert tls.AlertError
	var record tls.RecordHeaderError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &alert) || errors.As(err, &record) || errors.As(err, &unknown) ||
		errors.As(err, &hostname) || errors.As(err, &invalid)
}

func timedOut(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}

func proxyUnreachable(how network.Route) Result {
	return Result{Detail: "cannot reach the proxy " + how.Shown(),
		Hint: "is it running and listening there (cntlm, Px)? Check the address and port"}
}

func needsProxySignIn(how network.Route) Result {
	return Result{Status: http.StatusProxyAuthRequired, Detail: "the proxy wants a sign-in of its own", Hint: signInHint(how.Proxy)}
}

func signInHint(proxy string) string {
	if parsed, err := url.Parse(proxy); proxy != "" && err == nil && network.IsAlwaysDirect(parsed.Hostname()) {
		// Already a local proxy (cntlm, Px): it is the one failing to sign in upstream.
		return "the local proxy at " + network.Redact(proxy) + " could not sign in to the corporate proxy: check its " +
			"credentials and domain (for cntlm, 'cntlm -I -M https://graph.microsoft.com' tests them)"
	}
	return "corporate proxies often want NTLM or Kerberos: run cntlm or Px, which sign in for you, and set " +
		network.ProxyEnv + " to where it listens, e.g. 127.0.0.1:3128"
}

func noWayOutHint(local string) string {
	if local != "" {
		return "something is listening on " + local + ", like cntlm or Px: try " + network.ProxyEnv + "=" + local
	}
	return "this network may need a proxy: set HTTPS_PROXY or " + network.ProxyEnv
}

// tlsDetail is why the certificate did not verify, in the words a person searches for,
// and who issued it. Where the platform's verifier says why only in its own words (macOS
// does, for a chain no trusted root signed), the reason is read from the chain.
func tlsDetail(verification *tls.CertificateVerificationError, now time.Time) string {
	reason := "the certificate did not verify"
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	chain := verification.UnverifiedCertificates
	switch {
	case errors.As(verification.Err, &hostname):
		reason = "Hostname mismatch"
	case errors.As(verification.Err, &invalid) && invalid.Reason == x509.Expired:
		reason = "certificate has expired"
	case errors.As(verification.Err, &unknown):
		reason = unknownAuthority(chain)
	case len(chain) > 0 && now.After(chain[0].NotAfter):
		reason = "certificate has expired"
	case len(chain) > 0:
		reason = unknownAuthority(chain)
	}
	detail := "TLS: " + reason
	if len(chain) > 0 {
		if issuer := IssuerName(chain[0]); issuer != "" {
			detail += "; the certificate is issued by " + issuer
		}
	}
	return detail
}

// unknownAuthority is OpenSSL's words for a chain no trusted root signed.
func unknownAuthority(chain []*x509.Certificate) string {
	if len(chain) == 0 {
		return "unable to get local issuer certificate"
	}
	if selfSigned(chain[0]) {
		return "self-signed certificate"
	}
	if selfSigned(chain[len(chain)-1]) {
		return "self-signed certificate in certificate chain"
	}
	return "unable to get local issuer certificate"
}

func selfSigned(certificate *x509.Certificate) bool {
	return string(certificate.RawIssuer) == string(certificate.RawSubject) &&
		certificate.CheckSignatureFrom(certificate) == nil
}

func tlsHint(trust network.Trust) string {
	if trust.Explicit != "" {
		return trust.Explicit + " names " + trust.Path + ", used as it is: add the proxy's root certificate to it, or unset " +
			trust.Explicit + " to trust the OS store"
	}
	return "a TLS-inspecting proxy? Its root certificate is not in this machine's store: ask IT for it, then install " +
		"it there or name it with ca_bundle in the config file"
}

// IssuerName is a certificate's issuer as "Common Name (Organisation)", or whichever of
// them it has.
func IssuerName(certificate *x509.Certificate) string {
	common := certificate.Issuer.CommonName
	organisation := strings.Join(certificate.Issuer.Organization, ", ")
	if common != "" && organisation != "" && !strings.Contains(common, organisation) {
		return common + " (" + organisation + ")"
	}
	if common != "" {
		return common
	}
	return organisation
}

// LocalProxyListening is 127.0.0.1:PORT for the first of ports something listens on, or
// "".
func LocalProxyListening(ports []int) string {
	for _, port := range ports {
		address := fmt.Sprintf("127.0.0.1:%d", port)
		if connection, err := net.DialTimeout("tcp", address, 300*time.Millisecond); err == nil {
			_ = connection.Close()
			return address
		}
	}
	return ""
}
