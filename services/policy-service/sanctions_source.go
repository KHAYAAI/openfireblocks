package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Where the sanctions list comes from, and what happens when it goes
// stale.
//
// Until now the list was embedded in the binary at build time. That is
// fine for a fixture and wrong for a control: OFAC designates new
// addresses continuously, and an embedded list is exactly as current as
// the last time somebody rebuilt and redeployed this service. A platform
// that screens against a six-month-old list is not screening.
//
// So the list is now a file this service re-reads, refreshed by
// cmd/ofac-sync (a CronJob in the chart), and the embedded copy becomes
// the fallback for a deployment that has not wired the sync up yet.
//
// # The staleness decision
//
// The uncomfortable one, and worth stating rather than burying. A stale
// list has two failure modes and they point opposite ways:
//
//   - Keep screening against it, and a transfer to an address designated
//     last week goes through. That is a compliance breach, and the
//     institution finds out from its regulator.
//   - Refuse to screen, and every transaction is denied. That is an
//     outage, caused by a feed this platform does not control.
//
// The resolution here is a grace period. Below warnAfter the list is
// served normally. Between warnAfter and denyAfter it is served but every
// decision carries a warning, so an operator sees it in the response and
// on the dashboard before it becomes a problem. Past denyAfter the
// service fails closed and says exactly why.
//
// Both are configurable, because the right numbers depend on how much a
// deployment moves and what its regulator expects, and a vendor guessing
// at that for a bank is how you end up with a control that gets disabled.
// The defaults assume a daily sync: a day of tolerance, then two.

const (
	defaultWarnAfter = 24 * time.Hour
	defaultDenyAfter = 72 * time.Hour
)

// sanctionsList is the on-disk format cmd/ofac-sync writes.
//
// FetchedAt is the whole point of the file having a format at all. A bare
// array of addresses cannot answer "how old is this", and a control that
// cannot report its own freshness is one nobody can rely on.
type sanctionsList struct {
	// When this list was successfully fetched from the source.
	FetchedAt time.Time `json:"fetched_at"`
	// Where it came from, for the audit trail.
	Source string `json:"source"`
	// Publication date reported by the source itself, when it gives one.
	// Distinct from FetchedAt: a feed can be reachable and stale.
	PublishedAt string `json:"published_at,omitempty"`
	// Designated addresses, lowercased for comparison.
	Addresses []string `json:"addresses"`
	// Free text, present in the embedded fallback to say what it is.
	Comment string `json:"_comment,omitempty"`
}

// sanctionsSource holds the loaded list and answers questions about its
// age.
type sanctionsSource struct {
	list      sanctionsList
	path      string // empty when the embedded fallback is in use
	warnAfter time.Duration
	denyAfter time.Duration
	// True when this is the build-time fallback rather than a synced file.
	// Reported separately from staleness: an embedded list is not merely
	// old, it is not connected to anything, and those want different
	// words in an operator's ear.
	embedded bool
}

func durationFromEnv(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a duration (try 24h): %w", key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", key, d)
	}
	return d, nil
}

// loadSanctionsSource reads the list SANCTIONS_FILE names, or falls back
// to the embedded copy.
//
// A configured file that cannot be read is fatal rather than a silent
// fallback. A deployment that set SANCTIONS_FILE has decided its
// screening comes from the feed; quietly serving a build-time list
// instead would leave it screening against something years old while
// believing otherwise, which is the failure this whole file exists to
// prevent.
func loadSanctionsSource(getenv func(string) string, embeddedJSON []byte) (*sanctionsSource, error) {
	warnAfter, err := durationFromEnv(getenv, "SANCTIONS_WARN_AFTER", defaultWarnAfter)
	if err != nil {
		return nil, err
	}
	denyAfter, err := durationFromEnv(getenv, "SANCTIONS_DENY_AFTER", defaultDenyAfter)
	if err != nil {
		return nil, err
	}
	if denyAfter < warnAfter {
		return nil, fmt.Errorf(
			"SANCTIONS_DENY_AFTER (%s) is shorter than SANCTIONS_WARN_AFTER (%s); "+
				"the warning would never be seen before the denial", denyAfter, warnAfter)
	}

	path := strings.TrimSpace(getenv("SANCTIONS_FILE"))
	if path == "" {
		var list sanctionsList
		if err := json.Unmarshal(embeddedJSON, &list); err != nil {
			return nil, fmt.Errorf("the embedded sanctions list is malformed: %w", err)
		}
		return &sanctionsSource{
			list:      normaliseList(list),
			warnAfter: warnAfter,
			denyAfter: denyAfter,
			embedded:  true,
		}, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("SANCTIONS_FILE=%s could not be read: %w", path, err)
	}
	var list sanctionsList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("the sanctions list at %s is malformed: %w", path, err)
	}
	if list.FetchedAt.IsZero() {
		return nil, fmt.Errorf(
			"the sanctions list at %s carries no fetched_at, so its age cannot be established; "+
				"it was not written by cmd/ofac-sync", path)
	}
	if len(list.Addresses) == 0 {
		// An empty list screens nothing while looking like it works. A
		// sync that produced one has failed and should not be served.
		return nil, fmt.Errorf("the sanctions list at %s is empty; refusing to screen against nothing", path)
	}
	return &sanctionsSource{
		list:      normaliseList(list),
		path:      path,
		warnAfter: warnAfter,
		denyAfter: denyAfter,
	}, nil
}

// normaliseList lowercases every address once, at load.
//
// Screening is a comparison, and comparisons on mixed-case hex are the
// kind of bug that silently passes a designated address through. Doing it
// here means every consumer gets it right without remembering to.
func normaliseList(l sanctionsList) sanctionsList {
	out := make([]string, 0, len(l.Addresses))
	for _, a := range l.Addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" {
			out = append(out, a)
		}
	}
	l.Addresses = out
	return l
}

// age is how old the loaded list is.
func (s *sanctionsSource) age(now time.Time) time.Duration {
	return now.Sub(s.list.FetchedAt)
}

// status reports whether the list is usable, and what to tell an operator.
//
// Three outcomes: ok, stale-but-serving, and refusing. The middle one is
// the reason this returns a warning string rather than a bool -- an
// operator needs to see it coming.
type sanctionsStatus struct {
	OK      bool   `json:"ok"`
	Age     string `json:"age"`
	Warning string `json:"warning,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (s *sanctionsSource) status(now time.Time) sanctionsStatus {
	if s.embedded {
		// Not stale: disconnected. An embedded list has no feed behind it,
		// so its age is meaningless and saying "3 days old" would imply a
		// sync that is not happening.
		return sanctionsStatus{
			OK:  true,
			Age: "n/a",
			Warning: "Screening against the list built into this binary. It is a fixture, not a feed, " +
				"and it does not change until this service is rebuilt. Set SANCTIONS_FILE and run " +
				"cmd/ofac-sync before screening anything that matters.",
		}
	}

	age := s.age(now)
	switch {
	case age >= s.denyAfter:
		return sanctionsStatus{
			OK:  false,
			Age: age.Truncate(time.Minute).String(),
			Reason: fmt.Sprintf(
				"the sanctions list was last fetched %s ago, past the %s limit. Screening against it "+
					"would not catch anything designated since. Fix the OFAC sync (cmd/ofac-sync) or "+
					"raise SANCTIONS_DENY_AFTER deliberately.",
				age.Truncate(time.Minute), s.denyAfter),
		}
	case age >= s.warnAfter:
		return sanctionsStatus{
			OK:  true,
			Age: age.Truncate(time.Minute).String(),
			Warning: fmt.Sprintf(
				"the sanctions list is %s old and will stop being served at %s. The OFAC sync has "+
					"probably stopped; check the ofac-sync CronJob.",
				age.Truncate(time.Minute), s.denyAfter),
		}
	default:
		return sanctionsStatus{OK: true, Age: age.Truncate(time.Minute).String()}
	}
}

// describe is what GET /info reports.
func (s *sanctionsSource) describe(now time.Time) map[string]interface{} {
	st := s.status(now)
	out := map[string]interface{}{
		"addresses":  len(s.list.Addresses),
		"embedded":   s.embedded,
		"usable":     st.OK,
		"age":        st.Age,
		"source":     s.list.Source,
		"warn_after": s.warnAfter.String(),
		"deny_after": s.denyAfter.String(),
	}
	if !s.embedded {
		out["fetched_at"] = s.list.FetchedAt.UTC().Format(time.RFC3339)
		if s.list.PublishedAt != "" {
			out["published_at"] = s.list.PublishedAt
		}
	}
	if st.Warning != "" {
		out["warning"] = st.Warning
	}
	if st.Reason != "" {
		out["reason"] = st.Reason
	}
	return out
}
