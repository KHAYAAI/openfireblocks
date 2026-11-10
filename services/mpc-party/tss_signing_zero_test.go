package main

import (
	"strings"
	"testing"
	"time"
)

// tss-lib drops leading zero bytes of an Ed25519 message, so such a message
// must be refused, not signed into a signature no verifier accepts. The
// refusal must also release the key's lock, or the next request on the key
// hangs; the secp256k1 length check had exactly that bug and is fixed the
// same way, though only the Ed25519 path is exercised here.
func TestSigningRefusalsReleaseTheKeyAndZeroLedEd25519IsRefused(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	t.Setenv("TSS_PREPARAMS_POOL", "0")
	key := runKeygenOverHTTP(t, CurveEd25519)
	mgr := key.managers[1]
	err := mgr.StartSigning("zero-led", key.ceremonyID, []byte{0x00, 0x01, 0x02}, []int{1, 2})
	if err == nil || !strings.Contains(err.Error(), "zero byte") {
		t.Fatalf("a zero-led Ed25519 message was accepted: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- mgr.StartSigning("after-refusal", key.ceremonyID, []byte("fine"), []int{1, 2, 3}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "threshold+1") {
			t.Fatalf("unexpected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the key's lock was left held by the refusal")
	}
}
