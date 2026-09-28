// Package certfake makes certificates for tests: a certificate authority, such as a
// TLS-inspecting proxy's, and the certificates it signs for a host. Nothing is read from
// or written to the machine's store.
package certfake

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// Authority is a certificate authority that signs certificates.
type Authority struct {
	Certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

// NewAuthority is a certificate authority named commonName, of organisation (when given).
func NewAuthority(t testing.TB, commonName, organisation string) *Authority {
	t.Helper()
	key := newKey(t)
	name := pkix.Name{CommonName: commonName}
	if organisation != "" {
		name.Organization = []string{organisation}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: name,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	return &Authority{Certificate: sign(t, template, template, &key.PublicKey, key), key: key}
}

// PEM is the authority's certificate as PEM, as a CA bundle holds it.
func (a *Authority) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.Certificate.Raw})
}

// Pool is a certificate pool holding only this authority.
func (a *Authority) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(a.Certificate)
	return pool
}

// Leaf is a certificate for hosts (names or IP addresses) signed by the authority, valid
// until notAfter.
func (a *Authority) Leaf(t testing.TB, notAfter time.Time, hosts ...string) tls.Certificate {
	t.Helper()
	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: hosts[0]},
		NotBefore: notAfter.Add(-48 * time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	leaf := sign(t, template, a.Certificate, &key.PublicKey, a.key)
	return tls.Certificate{Certificate: [][]byte{leaf.Raw, a.Certificate.Raw}, PrivateKey: key, Leaf: leaf}
}

func newKey(t testing.TB) *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func sign(t testing.TB, template, parent *x509.Certificate, public any, signer *ecdsa.PrivateKey) *x509.Certificate {
	der, err := x509.CreateCertificate(rand.Reader, template, parent, public, signer)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
