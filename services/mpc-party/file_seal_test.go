package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eddsakeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
)

func fileStore(t *testing.T) (func(string) string, string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shares")
	keyFile := filepath.Join(t.TempDir(), "share.key") // a different directory, as it should be
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SHARE_STORE_DIR": dir, "SHARE_STORE_KEY_FILE": keyFile}
	return func(k string) string { return env[k] }, dir, keyFile
}

func aShare() *KeyShare {
	return &KeyShare{Curve: CurveEd25519, EdDSA: &eddsakeygen.LocalPartySaveData{}}
}

func TestASealedShareComesBackWithItsContextAndIsNotPlaintextOnDisk(t *testing.T) {
	getenv, dir, _ := fileStore(t)
	cc := &CeremonyContext{Threshold: 1, TotalParties: 3, Curve: "ed25519", Epoch: 2, Address: "SomeAddress1111"}
	ok, err := SealKeyShareWithContext(context.Background(), getenv, 1, "cer-1", aShare(), cc)
	if err != nil || !ok {
		t.Fatalf("sealed=%v err=%v", ok, err)
	}
	blob, _ := os.ReadFile(filepath.Join(dir, "party-1", "cer-1.sealed"))
	if strings.Contains(string(blob), "SomeAddress1111") || strings.Contains(string(blob), "ed25519") || strings.Contains(string(blob), "eddsa") {
		t.Fatal("the share or its context is readable in the sealed file")
	}
	share, got, err := LoadSealedShare(context.Background(), getenv, 1, "cer-1")
	if err != nil || share.Curve != CurveEd25519 || got == nil || *got != *cc {
		t.Fatalf("round trip: %+v %+v %v", share, got, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "party-1", "cer-1.sealed")); info.Mode().Perm() != 0o600 {
		t.Errorf("sealed file mode %o, want 600", info.Mode().Perm())
	}
}

func TestATamperedSealedShareDoesNotOpen(t *testing.T) {
	getenv, dir, _ := fileStore(t)
	if _, err := SealKeyShareWithContext(context.Background(), getenv, 1, "c", aShare(), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "party-1", "c.sealed")
	b, _ := os.ReadFile(p)
	i := strings.LastIndex(string(b), `"data":"`) + len(`"data":"`) + 4
	b[i] ^= 1 // flip one hex digit of the ciphertext
	_ = os.WriteFile(p, b, 0o600)
	if _, _, err := LoadSealedShare(context.Background(), getenv, 1, "c"); err == nil {
		t.Fatal("an altered sealed share was accepted")
	}
}

// A sealed file is bound to the party and the ceremony it was made for.
func TestASealedShareCannotBePassedOffAsAnotherPartysOrCeremonys(t *testing.T) {
	getenv, dir, _ := fileStore(t)
	if _, err := SealKeyShareWithContext(context.Background(), getenv, 1, "c", aShare(), nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "party-1", "c.sealed"))
	_ = os.MkdirAll(filepath.Join(dir, "party-2"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "party-2", "c.sealed"), b, 0o600)     // as party 2's share
	_ = os.WriteFile(filepath.Join(dir, "party-1", "other.sealed"), b, 0o600) // as another ceremony's
	if _, _, err := LoadSealedShare(context.Background(), getenv, 2, "c"); err == nil {
		t.Error("party 1's share opened as party 2's")
	}
	if _, _, err := LoadSealedShare(context.Background(), getenv, 1, "other"); err == nil {
		t.Error("one ceremony's share opened as another's")
	}
}

func TestTheWrongKeyDoesNotOpenAShare(t *testing.T) {
	getenv, _, keyFile := fileStore(t)
	if _, err := SealKeyShareWithContext(context.Background(), getenv, 1, "c", aShare(), nil); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(keyFile, []byte(strings.Repeat("cd", 32)), 0o600)
	if _, _, err := LoadSealedShare(context.Background(), getenv, 1, "c"); err == nil {
		t.Fatal("a share opened with a different key")
	}
}

func TestUnsafeKeyHandlingIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	getenv, dir, keyFile := fileStore(t)
	_ = os.Chmod(keyFile, 0o644)
	if _, err := SealKeyShareWithContext(context.Background(), getenv, 1, "c", aShare(), nil); err == nil || !strings.Contains(err.Error(), "readable by other users") {
		t.Errorf("world-readable key accepted: %v", err)
	}
	_ = os.Chmod(keyFile, 0o600)

	inside := filepath.Join(dir, "share.key")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(inside, []byte(strings.Repeat("ab", 32)), 0o600)
	env := map[string]string{"SHARE_STORE_DIR": dir, "SHARE_STORE_KEY_FILE": inside}
	if _, err := SealKeyShareWithContext(context.Background(), func(k string) string { return env[k] }, 1, "c", aShare(), nil); err == nil {
		t.Error("a key stored inside the share directory was accepted")
	}
	env = map[string]string{"SHARE_STORE_DIR": dir}
	if _, err := SealKeyShareWithContext(context.Background(), func(k string) string { return env[k] }, 1, "c", aShare(), nil); err == nil {
		t.Error("a store with no key file was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "party-1")); err == nil {
		t.Error("something was written despite the refusals")
	}
}

func TestACeremonyIdCannotEscapeTheStoreDirectory(t *testing.T) {
	getenv, _, _ := fileStore(t)
	for _, id := range []string{"../../etc/passwd", "a/b", "..", ""} {
		if _, err := SealKeyShareWithContext(context.Background(), getenv, 1, id, aShare(), nil); err == nil {
			t.Errorf("ceremony id %q was accepted", id)
		}
	}
}

func TestRetiringDestroysTheFile(t *testing.T) {
	getenv, dir, _ := fileStore(t)
	_, _ = SealKeyShareWithContext(context.Background(), getenv, 1, "c", aShare(), nil)
	if ok, err := RetireSealedShare(context.Background(), getenv, 1, "c"); err != nil || !ok {
		t.Fatalf("retire: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "party-1", "c.sealed")); err == nil {
		t.Fatal("the sealed file is still there")
	}
	if ok, err := RetireSealedShare(context.Background(), getenv, 1, "c"); err != nil || !ok {
		t.Errorf("retiring twice should be harmless: %v %v", ok, err)
	}
}

func TestNothingConfiguredIsStillNotAnError(t *testing.T) {
	if ok, err := SealKeyShareWithContext(context.Background(), func(string) string { return "" }, 1, "c", aShare(), nil); ok || err != nil {
		t.Fatalf("sealed=%v err=%v", ok, err)
	}
}
