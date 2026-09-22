package main

import (
	"testing"
	"time"
)

// The two timeouts are a matched pair describing opposite ends of the same
// startup race:
//
//	preParamsGenTimeout -- how long a party may spend becoming ready to
//	                       receive relayed protocol messages.
//	PeerReadyTimeout    -- how long that party's peers will wait for it.
//
// If the first exceeds the second, a party that legitimately uses its full
// allowance is abandoned by its peers and the ceremony aborts. That is
// precisely what happened on a real cluster: generation was allowed 2
// minutes while peers gave up after 60 seconds, so under realistic pod CPU
// limits every DKG ceremony failed with "ceremony registered but not yet
// ready to receive messages".
//
// Nothing in the type system relates these two constants, and they live in
// different files, so this test is the only thing keeping them ordered.
func TestPreParamsTimeoutIsBelowPeerReadyTimeout(t *testing.T) {
	if preParamsGenTimeout >= PeerReadyTimeout {
		t.Fatalf(
			"preParamsGenTimeout (%s) must be strictly less than PeerReadyTimeout (%s): "+
				"a party is allowed to take longer to become ready than its peers will wait for it, "+
				"so any ceremony where generation runs long will abort",
			preParamsGenTimeout, PeerReadyTimeout)
	}

	// Not just ordered but with real headroom: a party becoming ready at
	// the last possible instant still has to complete the rest of the
	// protocol, and the peer's retry loop polls only every retryInterval.
	const minHeadroom = 30 * time.Second
	if PeerReadyTimeout-preParamsGenTimeout < minHeadroom {
		t.Fatalf(
			"PeerReadyTimeout (%s) leaves only %s over preParamsGenTimeout (%s); want at least %s",
			PeerReadyTimeout, PeerReadyTimeout-preParamsGenTimeout, preParamsGenTimeout, minHeadroom)
	}
}

// A ceremony that arrives before the pool has filled must still run, using
// the inline fallback, rather than failing.
func TestPreParamsPoolFallsBackWhenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("generates safe primes; slow by construction")
	}

	// Deliberately not started, so the pool is empty and get() has to take
	// the inline path.
	p := newPreParamsPool(1)

	pre, err := p.get()
	if err != nil {
		t.Fatalf("get() on an empty pool should generate inline, got error: %v", err)
	}
	if pre == nil {
		t.Fatal("get() returned nil pre-params and no error")
	}
	if !pre.ValidateWithProof() {
		t.Fatal("inline-generated pre-params failed tss-lib's own validation")
	}
}

// The pool serves pre-generated parameters without blocking, which is the
// entire point: a warm pool takes a ceremony off the slow path.
func TestPreParamsPoolServesWarmEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("generates safe primes; slow by construction")
	}

	p := newPreParamsPool(1)
	p.start()

	// Wait for the background filler to produce one.
	deadline := time.After(5 * time.Minute)
	for len(p.ch) == 0 {
		select {
		case <-deadline:
			t.Fatal("background filler produced no pre-params within 5 minutes")
		case <-time.After(250 * time.Millisecond):
		}
	}

	start := time.Now()
	pre, err := p.get()
	if err != nil {
		t.Fatalf("get() from a warm pool: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("get() from a warm pool took %s; it should be immediate", elapsed)
	}
	if !pre.ValidateWithProof() {
		t.Fatal("pooled pre-params failed tss-lib's own validation")
	}
}
