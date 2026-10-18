//go:build !pkcs11

package keys

import "fmt"

// PKCS11Supported reports whether this binary was built with -tags pkcs11.
const PKCS11Supported = false

// The default build is CGO_ENABLED=0 on a distroless base, and PKCS#11
// needs cgo to load the vendor's module. Rather than make every image
// carry a C toolchain and glibc for a feature most deployments will not
// turn on, hardware signing is a separate build: -tags pkcs11 with
// CGO_ENABLED=1 (see the Dockerfile's pkcs11 target).

var errNoPKCS11 = fmt.Errorf("this binary was built without PKCS#11 support; " +
	"rebuild with CGO_ENABLED=1 go build -tags pkcs11, or use the image built with docker build --target pkcs11")

// OpenPKCS11 always fails in a build without PKCS#11 support.
func OpenPKCS11(*PKCS11Config) (KeySigner, error) { return nil, errNoPKCS11 }

// GeneratePKCS11Key always fails in a build without PKCS#11 support.
func GeneratePKCS11Key(*PKCS11Config) error { return errNoPKCS11 }
