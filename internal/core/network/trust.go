package network

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// BundleEnv names a CA bundle to trust exactly, instead of the system's roots.
var BundleEnv = brand.EnvVar("CA_BUNDLE")

// explicitBundle is the bundle the environment says to trust exactly, if any:
// LDO_CA_BUNDLE, then REQUESTS_CA_BUNDLE and CURL_CA_BUNDLE (which a Python tool's, or
// curl's, users often set already).
func (s Settings) explicitBundle() string {
	_, bundle := s.explicitSource()
	return bundle
}

// explicitSource is the variable naming a bundle to trust exactly, and the bundle.
func (s Settings) explicitSource() (string, string) {
	for _, name := range []string{BundleEnv, "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"} {
		if bundle := s.getenv(name); bundle != "" {
			return name, bundle
		}
	}
	return "", ""
}

// Trust is which certificates calls verify against, as a person is shown it.
type Trust struct {
	// Explicit is the variable naming a bundle that is used as it is, or "" when calls
	// verify against this machine's store.
	Explicit string
	// Path is that bundle, else the config file's ca_bundle added to the store, else "".
	Path string
	// Certificates is how many certificates Path holds (0 when it cannot be read).
	Certificates int
}

// Trust is which certificates calls verify against.
func (s Settings) Trust() Trust {
	name, bundle := s.explicitSource()
	if name == "" {
		bundle = s.CABundle
	}
	found := Trust{Explicit: name, Path: bundle}
	if bundle != "" {
		if data, err := os.ReadFile(bundle); err == nil {
			found.Certificates = countPEM(data)
		}
	}
	return found
}

// countPEM is how many certificates PEM data holds.
func countPEM(data []byte) int {
	count := 0
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return count
		}
		if block.Type == "CERTIFICATE" {
			count++
		}
	}
}

// CertPool is what every call verifies against: an explicit bundle alone when the
// environment names one; else the system's roots (where IT installs a TLS-inspecting
// proxy's root), with the config file's ca_bundle added.
func (s Settings) CertPool() (*x509.CertPool, error) {
	if bundle := s.explicitBundle(); bundle != "" {
		pool := x509.NewCertPool()
		if err := appendPEM(pool, bundle); err != nil {
			return nil, err
		}
		return pool, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if s.CABundle != "" {
		if err := appendPEM(pool, s.CABundle); err != nil {
			return nil, err
		}
	}
	return pool, nil
}

func appendPEM(pool *x509.CertPool, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errs.Configf("cannot read the CA bundle %s: %v", path, err)
	}
	if !pool.AppendCertsFromPEM(data) {
		return errs.Configf("the CA bundle %s holds no PEM certificates", path)
	}
	return nil
}

// cacheFolder is where the Azure CLI's bundle is written; tests replace it.
var cacheFolder = func() (string, error) {
	base, err := os.UserCacheDir()
	return filepath.Join(base, brand.ConfigDir), err
}

// AzureCLIBundle is the bundle the Azure CLI is handed with a config file's ca_bundle:
// this machine's certificates and ca_bundle's together, in one file in the cache folder
// (~/.cache/ldo on Linux), since a Python tool given REQUESTS_CA_BUNDLE trusts that file
// alone. It is ca_bundle itself when the combined file cannot be written.
func (s Settings) AzureCLIBundle() string {
	extra, err := os.ReadFile(s.CABundle)
	if err != nil {
		return s.CABundle
	}
	combined := append(append(systemCertificates(), '\n'), extra...)
	folder, err := cacheFolder()
	if err != nil {
		return s.CABundle
	}
	sum := sha256.Sum256(combined)
	path := filepath.Join(folder, fmt.Sprintf("%s-ca-bundle-%x.pem", brand.Command, sum[:8]))
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(combined)) {
		return path
	}
	if err := writeBundle(folder, path, combined); err != nil {
		slog.Warn("the Azure CLI is given ca_bundle alone: " + err.Error())
		return s.CABundle
	}
	return path
}

// writeBundle writes a bundle beside its final name, then renames it into place, so a
// second command reading it meanwhile never sees half of one.
func writeBundle(folder, path string, data []byte) error {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(folder, ".ca-bundle-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// Transport is an HTTP transport that follows these rules: the proxy RouteFor gives each
// request, and CertPool's certificates. It never follows the environment on its own.
func (s Settings) Transport() (*http.Transport, error) {
	pool, err := s.CertPool()
	if err != nil {
		return nil, err
	}
	return &http.Transport{
		Proxy: func(request *http.Request) (*url.URL, error) {
			how, err := s.RouteFor(request.URL.String())
			if err != nil || how.Proxy == "" {
				return nil, err
			}
			return url.Parse(how.Proxy)
		},
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}, nil
}
