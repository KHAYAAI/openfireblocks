package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"
)

// Re-reading the sanctions list without restarting.
//
// The evaluator loads its screening list once, at startup, which was
// correct while the list was compiled into the binary and is wrong now
// that a CronJob rewrites it daily. Without this, a synced file would sit
// on disk being perfectly current while the running process screened
// against whatever it read when it booted -- and the staleness check in
// sanctions_source.go would eventually refuse every transaction because
// of a file that had in fact been updated hours ago.
//
// So: a goroutine re-reads the file on an interval and swaps the
// evaluator. Swapping the whole evaluator rather than mutating the store
// in place is deliberate. The prepared Rego query holds its data, so
// there is no supported way to change the list under a live query; and a
// half-swapped evaluator would screen against a mixture of two lists,
// which is a state no operator could reason about afterwards.
//
// Rebuilding costs a Rego prepare, which is milliseconds and happens
// hourly. That is not a hot path.

// liveEvaluator holds the current evaluator and allows it to be replaced.
type liveEvaluator struct {
	mu      sync.RWMutex
	current *evaluator
}

func newLiveEvaluator(e *evaluator) *liveEvaluator {
	return &liveEvaluator{current: e}
}

func (l *liveEvaluator) get() *evaluator {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.current
}

func (l *liveEvaluator) set(e *evaluator) {
	l.mu.Lock()
	l.current = e
	l.mu.Unlock()
}

// reloadInterval is how often the file is re-read.
//
// Zero disables reloading, which is the right behaviour for the embedded
// list: there is no file to re-read, and a goroutine waking hourly to
// rebuild an identical evaluator is pure noise in the logs.
func reloadInterval(getenv func(string) string) (time.Duration, error) {
	return durationFromEnv(getenv, "SANCTIONS_RELOAD_INTERVAL", time.Hour)
}

// watchSanctions re-reads the list until ctx is done.
//
// A failed reload is logged and the previous evaluator is kept. That is
// the safe direction: the file is being rewritten by another process, so
// a read can legitimately catch it mid-rename or mid-write, and throwing
// away working screening over a transient read would turn a race into an
// outage. If the failure is not transient, the list Claude already holds
// ages past its threshold and the service refuses on its own -- which is
// the behaviour we want and it arrives without this function having to
// decide anything.
func watchSanctions(ctx context.Context, live *liveEvaluator, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := newEvaluator(ctx)
			if err != nil {
				// Kept, not replaced. See the comment above.
				log.Printf("sanctions reload failed, keeping the list already loaded: %v", err)
				continue
			}
			previous := live.get()
			live.set(next)

			// Logged only when something changed, so a daily sync that
			// finds no new designations does not fill the log with
			// identical lines an operator learns to skip.
			if previous == nil ||
				len(previous.sanctions.list.Addresses) != len(next.sanctions.list.Addresses) ||
				!previous.sanctions.list.FetchedAt.Equal(next.sanctions.list.FetchedAt) {
				log.Printf("sanctions list reloaded: %d addresses, fetched %s",
					len(next.sanctions.list.Addresses),
					next.sanctions.list.FetchedAt.UTC().Format(time.RFC3339))
			}
		}
	}
}

// startSanctionsWatcher wires the watcher up, or explains why it did not.
func startSanctionsWatcher(ctx context.Context, live *liveEvaluator) {
	interval, err := reloadInterval(os.Getenv)
	if err != nil {
		// Fatal rather than defaulted: someone set this on purpose, and
		// silently reloading hourly instead of at the cadence they asked
		// for is the kind of difference that only shows up in an audit.
		log.Fatalf("SANCTIONS_RELOAD_INTERVAL: %v", err)
	}
	if live.get().sanctions.embedded {
		log.Print("sanctions list is the build-time fallback; not watching for changes. " +
			"Set SANCTIONS_FILE and run cmd/ofac-sync to screen against a current list.")
		return
	}
	log.Printf("watching the sanctions list, reloading every %s", interval)
	go watchSanctions(ctx, live, interval)
}
