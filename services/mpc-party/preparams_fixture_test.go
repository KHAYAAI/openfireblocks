package main

import (
	"os"
	"sync"
	"testing"
	"time"

	tsslib "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// Safe-prime generation is a randomised search with a heavy tail. Left to
// each test, it made the secp256k1 ceremonies race their own deadlines: every
// party a test built started a background search that never stopped (so by
// the fifth heavy test a dozen leftovers were competing for the same cores),
// and each ceremony then paid for an inline search of its own under a fixed
// 2-5 minute deadline. On a loaded runner that timed out at random, in
// whichever test happened to draw the slow search, and passed on a re-run.
//
// So tests do not search on the ceremony's clock. TestMain turns the
// background searches off for the whole test binary, and the secp256k1 tests
// hand their parties sets from this cache, which is filled once per run,
// before any ceremony starts and outside any test deadline.
//
// These sets are Paillier keys that several ceremonies and parties reuse.
// That is fine for a test and wrong for anything else, which is why this
// file is _test.go only: production still generates a fresh set per
// ceremony (preparams.go).
func TestMain(m *testing.M) {
	os.Setenv("TSS_PREPARAMS_POOL", "0")
	os.Exit(m.Run())
}

// One distinct set per party of the largest committee a test builds.
const testPreParamsSets = 3

var (
	testPreParamsOnce sync.Once
	testPreParamsVals []*tsslib.LocalPreParams
	testPreParamsErr  error
)

func cachedPreParams(t testing.TB) []*tsslib.LocalPreParams {
	t.Helper()
	testPreParamsOnce.Do(func() {
		sets := make([]*tsslib.LocalPreParams, testPreParamsSets)
		errs := make([]error, testPreParamsSets)
		var wg sync.WaitGroup
		for i := range sets {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// A failed attempt means the search did not converge in
				// time, not that anything is wrong: try again.
				for attempt := 0; attempt < 5; attempt++ {
					sets[i], errs[i] = tsslib.GeneratePreParams(4 * time.Minute)
					if errs[i] == nil {
						return
					}
				}
			}(i)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				testPreParamsErr = err
				return
			}
		}
		testPreParamsVals = sets
	})
	if testPreParamsErr != nil {
		t.Fatalf("could not generate the shared test pre-parameters: %v", testPreParamsErr)
	}
	return testPreParamsVals
}

// seedPreParams gives each manager its own cached set, so a secp256k1
// ceremony takes a ready-made one instead of searching. The pool holds a few
// copies so a manager that runs more than one ceremony in a test still finds
// one waiting.
func seedPreParams(t testing.TB, mgrs ...*TSSPartyManager) {
	t.Helper()
	if len(mgrs) > testPreParamsSets {
		t.Fatalf("seedPreParams: %d managers but only %d distinct sets", len(mgrs), testPreParamsSets)
	}
	sets := cachedPreParams(t)
	for i, mgr := range mgrs {
		pool := &preParamsPool{ch: make(chan *tsslib.LocalPreParams, 8), timeout: preParamsGenTimeout}
		for k := 0; k < cap(pool.ch); k++ {
			pool.ch <- sets[i]
		}
		mgr.preParams = pool
	}
}
