package nitroattest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// These files are a copy of services/mpc-party/nitroattest (each Go service
// builds from its own directory, so the module cannot be shared with a
// replace directive). The orchestrator and the parties must apply the same
// policy, so a drift between the copies fails here.
func TestCopyMatchesMPCParty(t *testing.T) {
	src := filepath.Join("..", "..", "..", "mpc-party", "nitroattest")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("services/mpc-party is not checked out alongside: %v", err)
	}
	for _, f := range []string{"verify.go", "cert.go"} {
		want, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from services/mpc-party/nitroattest/%s; copy it across", f, f)
		}
	}
}
