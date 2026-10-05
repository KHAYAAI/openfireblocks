package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// A synced address list as the sanctions data source for this service.
//
// The file is the one policy-service reads (SANCTIONS_FILE), written by
// policy-service's cmd/ofac-sync from the official OFAC feed, so both
// services screen against the same data and the same notion of freshness.
// The format and the staleness rule are deliberately the same as
// policy-service's sanctions_source.go; if one changes, change both.
//
// Fail closed, in three places, because a screening control that quietly
// answers "clean" is worse than none:
//   - no SANCTIONS_FILE configured: every check returns an error;
//   - the file cannot be read or parsed: every check returns an error;
//   - the list is older than the deny threshold: every check returns an
//     error naming its age.
// Callers treat an error as "screening unavailable" and block.

const (
	defaultSanctionsWarnAfter = 24 * time.Hour
	defaultSanctionsDenyAfter = 72 * time.Hour
)

type sanctionsFile struct {
	FetchedAt   time.Time `json:"fetched_at"`
	Source      string    `json:"source"`
	PublishedAt string    `json:"published_at,omitempty"`
	Addresses   []string  `json:"addresses"`
}

// SanctionsList answers "is this address designated" from a synced file.
type SanctionsList struct {
	path      string
	warnAfter time.Duration
	denyAfter time.Duration
	now       func() time.Time

	mu        sync.RWMutex
	loadedAt  time.Time
	fetchedAt time.Time
	source    string
	set       map[string]struct{}
	loadErr   error
}

// SanctionsStatus is what an operator needs to judge the list's freshness.
type SanctionsStatus struct {
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetched_at"`
	Age       string    `json:"age"`
	Entries   int       `json:"entries"`
	Warning   string    `json:"warning,omitempty"`
}

// NewSanctionsListFromEnv builds a list from SANCTIONS_FILE,
// SANCTIONS_WARN_AFTER and SANCTIONS_DENY_AFTER. With no SANCTIONS_FILE it
// returns a list whose every check fails, not nil: the caller still gets a
// working object that refuses, rather than a nil dereference or a skipped
// check.
func NewSanctionsListFromEnv(getenv func(string) string) (*SanctionsList, error) {
	warn, err := envDuration(getenv, "SANCTIONS_WARN_AFTER", defaultSanctionsWarnAfter)
	if err != nil {
		return nil, err
	}
	deny, err := envDuration(getenv, "SANCTIONS_DENY_AFTER", defaultSanctionsDenyAfter)
	if err != nil {
		return nil, err
	}
	if deny < warn {
		return nil, fmt.Errorf("SANCTIONS_DENY_AFTER (%s) is shorter than SANCTIONS_WARN_AFTER (%s)", deny, warn)
	}
	l := &SanctionsList{path: strings.TrimSpace(getenv("SANCTIONS_FILE")), warnAfter: warn, denyAfter: deny, now: time.Now}
	if l.path != "" {
		l.Reload()
	}
	return l, nil
}

func envDuration(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q must be a positive duration such as 24h", key, raw)
	}
	return d, nil
}

// Reload re-reads the file. A failed reload keeps the last good list but
// records the error; Check still refuses once that list passes the deny
// age, so a broken sync cannot extend a stale list's life indefinitely.
func (l *SanctionsList) Reload() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, err := os.ReadFile(l.path)
	if err != nil {
		l.loadErr = fmt.Errorf("cannot read SANCTIONS_FILE %s: %w", l.path, err)
		return l.loadErr
	}
	var f sanctionsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		l.loadErr = fmt.Errorf("SANCTIONS_FILE %s is not valid: %w", l.path, err)
		return l.loadErr
	}
	if f.FetchedAt.IsZero() {
		l.loadErr = fmt.Errorf("SANCTIONS_FILE %s has no fetched_at, so its age cannot be known", l.path)
		return l.loadErr
	}
	set := make(map[string]struct{}, len(f.Addresses))
	for _, a := range f.Addresses {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			set[a] = struct{}{}
		}
	}
	l.set, l.fetchedAt, l.source, l.loadedAt, l.loadErr = set, f.FetchedAt, f.Source, l.now(), nil
	return nil
}

// StartReload re-reads the file on an interval until stop is closed, so a
// designation fetched by the sync sidecar takes effect without a restart.
func (l *SanctionsList) StartReload(interval time.Duration, stop <-chan struct{}, logf func(string, ...interface{})) {
	if l == nil || l.path == "" || interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := l.Reload(); err != nil {
					logf("sanctions reload failed (keeping last good list until it goes stale): %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
}

// Check reports whether address is designated. The error is non-nil
// whenever the answer cannot be trusted; never treat (false, err) as clean.
func (l *SanctionsList) Check(address string) (bool, error) {
	if l == nil || l.path == "" {
		return false, fmt.Errorf("sanctions screening not configured: set SANCTIONS_FILE to a list synced by ofac-sync")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.set == nil {
		if l.loadErr != nil {
			return false, l.loadErr
		}
		return false, fmt.Errorf("sanctions list not loaded")
	}
	if age := l.now().Sub(l.fetchedAt); age > l.denyAfter {
		return false, fmt.Errorf("sanctions list is %s old (limit %s); refusing to screen against it", age.Round(time.Minute), l.denyAfter)
	}
	_, hit := l.set[strings.ToLower(strings.TrimSpace(address))]
	return hit, nil
}

// Status reports freshness for health and dashboards.
func (l *SanctionsList) Status() SanctionsStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st := SanctionsStatus{Source: l.source, FetchedAt: l.fetchedAt, Entries: len(l.set)}
	if l.set == nil {
		st.Warning = "no list loaded"
		return st
	}
	age := l.now().Sub(l.fetchedAt)
	st.Age = age.Round(time.Minute).String()
	if age > l.denyAfter {
		st.Warning = "stale: screening refused"
	} else if age > l.warnAfter {
		st.Warning = "ageing: sync is overdue"
	}
	return st
}
