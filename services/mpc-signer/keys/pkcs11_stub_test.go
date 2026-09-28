//go:build !pkcs11

package keys

import (
	"strings"
	"testing"
)

// A default build handed an HSM configuration must fail, and say how to
// get a build that works -- never fall back to a software key.
func TestADefaultBuildRefusesHSMModeAndSaysWhy(t *testing.T) {
	_, err := OpenPKCS11(&PKCS11Config{Library: "/lib/x.so", TokenLabel: "t", PIN: "p", KeyLabel: "k"})
	if err == nil || !strings.Contains(err.Error(), "-tags pkcs11") {
		t.Fatalf("got %v", err)
	}
}
