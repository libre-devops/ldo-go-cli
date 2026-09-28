//go:build !windows

package network

// windowsCertificates is nothing, away from Windows.
func windowsCertificates() []byte { return nil }
