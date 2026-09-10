package main

import (
	"log"
	"os"
	"sync"
	"time"

	tsslib "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// Pre-generated tss-lib pre-parameters.
//
// GeneratePreParams searches for safe primes. It is by far the most
// expensive step in a DKG ceremony and its cost is highly variable -- it is
// a randomised search, so the same machine can take five seconds on one
// attempt and ninety on the next. Calling it on the ceremony's critical
// path, as StartKeygen used to, makes every ceremony pay that variance
// while its peers sit waiting.
//
// That is not merely slow, it was the reason DKG failed outright on a real
// cluster. A party that has registered a ceremony but not yet built its
// LocalParty answers relayed protocol messages with 503
// (ErrCeremonyNotReady); peers retry for PeerReadyTimeout and then abort
// the whole ceremony. With generation on the critical path under realistic
// pod CPU limits, that budget was routinely exceeded and *every* ceremony
// aborted. Locally, where three parties share an unthrottled host and
// generation takes a few seconds, the race never lost -- which is exactly
// why this only appeared once the services ran as separate pods.
//
// So generation moves off the critical path: a small pool is filled in the
// background from process start, and a ceremony takes a ready-made set.
// The pool is deliberately tiny -- each entry is cheap to hold but
// expensive to make, and a party runs few concurrent ceremonies.
type preParamsPool struct {
	ch chan *tsslib.LocalPreParams

	// Generation timeout per attempt. A failed attempt is retried rather
	// than being fatal: it means the search did not converge in time, not
	// that anything is wrong.
	timeout time.Duration

	startOnce sync.Once
}

// preParamsGenTimeout bounds one GeneratePreParams attempt.
//
// This MUST stay below PeerReadyTimeout (tss_handlers.go). The two are a
// matched pair: it is the time a party may spend becoming ready to receive
// messages, versus the time its peers will wait for it to become ready. The
// original code had them inverted -- a party was allowed 2 minutes to
// generate pre-params while its peers gave up after 60 seconds -- so a
// party that took its allotted time was guaranteed to be abandoned. There
// is a compile-time-adjacent check on this ordering in
// TestPreParamsTimeoutIsBelowPeerReadyTimeout.
//
// Four minutes rather than ninety seconds, measured rather than guessed:
// on a cluster where each party is capped at one CPU, ninety seconds was
// not enough for the search to converge and ceremonies failed with
// "timeout or error while generating the safe primes". Safe-prime search
// is a randomised search with a heavy tail -- the same machine can take
// five seconds on one attempt and several minutes on the next -- so the
// budget has to cover the tail, not the median.
const preParamsGenTimeout = 4 * time.Minute

func newPreParamsPool(size int) *preParamsPool {
	if size < 1 {
		size = 1
	}
	return &preParamsPool{
		ch:      make(chan *tsslib.LocalPreParams, size),
		timeout: preParamsGenTimeout,
	}
}

// start begins filling the pool in the background. Safe to call more than
// once; only the first call starts the filler.
//
// TSS_PREPARAMS_POOL=0 keeps it stopped. Pre-parameters are Paillier keys,
// used only by secp256k1 ceremonies, and generating them is a CPU-bound
// safe-prime search -- so a party deployed to serve only Ed25519 chains
// (Solana) would otherwise burn a core producing something it can never
// use. The test suite sets it for the same reason: six Ed25519 parties
// each searching for safe primes starved the ECDSA tests running beside
// them of the CPU they genuinely needed, and made them time out.
//
// A party that has this off and is then asked for a secp256k1 ceremony
// still works -- get() generates inline, paying the full cost on that
// ceremony rather than having had one ready.
func (p *preParamsPool) start() {
	if os.Getenv("TSS_PREPARAMS_POOL") == "0" {
		log.Printf("pre-params pool disabled (TSS_PREPARAMS_POOL=0); secp256k1 ceremonies will generate inline")
		return
	}
	p.startOnce.Do(func() { go p.fill() })
}

func (p *preParamsPool) fill() {
	for {
		// Blocks once the pool is full, so this goroutine burns CPU only
		// while there is a slot to fill.
		pre, err := tsslib.GeneratePreParams(p.timeout)
		if err != nil {
			log.Printf("pre-params generation attempt failed, retrying: %v", err)
			continue
		}
		p.ch <- pre
	}
}

// get returns a pre-generated set if one is ready, and otherwise generates
// one inline, waiting up to timeout.
//
// The inline fallback exists so a ceremony that arrives before the pool has
// filled -- the first ceremony after a pod starts, most likely -- still
// runs rather than failing. It is the slow path, and the one the peer-side
// retry budget has to cover.
func (p *preParamsPool) get() (*tsslib.LocalPreParams, error) {
	select {
	case pre := <-p.ch:
		return pre, nil
	default:
	}

	log.Printf("pre-params pool empty, generating inline (this ceremony pays the full generation cost)")
	return tsslib.GeneratePreParams(p.timeout)
}
