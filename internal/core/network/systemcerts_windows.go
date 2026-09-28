//go:build windows

package network

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsCertificates is the certificates in the Windows ROOT and CA stores, as PEM.
func windowsCertificates() []byte {
	var found [][]byte
	for _, name := range []string{"ROOT", "CA"} {
		storeName, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		store, err := windows.CertOpenSystemStore(0, storeName)
		if err != nil {
			continue
		}
		var context *windows.CertContext
		for {
			context, err = windows.CertEnumCertificatesInStore(store, context)
			if err != nil || context == nil {
				break
			}
			der := unsafe.Slice(context.EncodedCert, context.Length)
			found = append(found, append([]byte(nil), der...))
		}
		_ = windows.CertCloseStore(store, 0)
	}
	return encodePEM(found)
}
