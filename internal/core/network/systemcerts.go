package network

import (
	"context"
	"encoding/pem"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Where Linux distributions keep the system bundle.
var linuxBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu, Alpine
	"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL, Fedora
	"/etc/ssl/ca-bundle.pem",             // SUSE
	"/etc/ssl/cert.pem",
}

var macOSKeychains = []string{"/System/Library/Keychains/SystemRootCertificates.keychain", "/Library/Keychains/System.keychain"}

// systemCertificates is the machine's own trusted certificates as PEM: the Linux system
// bundle, the macOS system keychains, or the Windows store. nil when they cannot be read,
// since a machine whose store cannot be read still has ca_bundle. Tests replace it.
var systemCertificates = func() []byte {
	switch runtime.GOOS {
	case "windows":
		return windowsCertificates()
	case "darwin":
		return macOSCertificates()
	}
	return linuxCertificates()
}

func linuxCertificates() []byte {
	paths := linuxBundles
	if file := os.Getenv("SSL_CERT_FILE"); file != "" {
		paths = append([]string{file}, linuxBundles...)
	}
	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil && countPEM(data) > 0 {
			return data
		}
	}
	return nil
}

func macOSCertificates() []byte {
	var keychains []string
	for _, path := range macOSKeychains {
		if _, err := os.Stat(path); err == nil {
			keychains = append(keychains, path)
		}
	}
	if len(keychains) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", append([]string{"find-certificate", "-a", "-p"}, keychains...)...).Output()
	if err != nil {
		return nil
	}
	return out
}

// encodePEM is certificates, each DER, as PEM.
func encodePEM(certificates [][]byte) []byte {
	var out []byte
	for _, der := range certificates {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out
}
