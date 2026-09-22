package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeList(t *testing.T, l sanctionsList) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sanctions.json")
	body, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func envWith(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// The three ages, and the two boundaries between them.
//
// This is the whole point of the file. A stale sanctions list has two
// failure modes pointing opposite ways -- screen against it and miss a
// new designation, or refuse and take the platform down -- and these are
// the thresholds that choose between them.
func TestTheListIsServedWarnedAboutThenRefusedAsItAges(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name      string
		age       time.Duration
		wantOK    bool
		wantWarn  bool
		wantInMsg string
	}{
		{name: "fresh", age: time.Hour, wantOK: true},
		{name: "just inside the warning", age: 23 * time.Hour, wantOK: true},
		{name: "warning", age: 25 * time.Hour, wantOK: true, wantWarn: true, wantInMsg: "ofac-sync"},
		{name: "just inside the denial", age: 71 * time.Hour, wantOK: true, wantWarn: true},
		{name: "refused", age: 73 * time.Hour, wantOK: false, wantInMsg: "SANCTIONS_DENY_AFTER"},
		{name: "refused, badly", age: 30 * 24 * time.Hour, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeList(t, sanctionsList{
				FetchedAt: now.Add(-tc.age),
				Source:    "test",
				Addresses: []string{"0xABC"},
			})
			src, err := loadSanctionsSource(envWith(map[string]string{"SANCTIONS_FILE": path}), nil)
			if err != nil {
				t.Fatalf("loading: %v", err)
			}

			st := src.status(now)
			if st.OK != tc.wantOK {
				t.Fatalf("at %s old: OK = %v, want %v (reason %q)", tc.age, st.OK, tc.wantOK, st.Reason)
			}
			if tc.wantWarn && st.Warning == "" {
				t.Errorf("at %s old the list is served with no warning; an operator sees nothing "+
					"until it starts refusing", tc.age)
			}
			if !tc.wantOK && st.Reason == "" {
				t.Error("refused without saying why")
			}
			if tc.wantInMsg != "" {
				msg := st.Warning + st.Reason
				if !strings.Contains(msg, tc.wantInMsg) {
					t.Errorf("the message does not mention %q, so it does not say what to do: %q",
						tc.wantInMsg, msg)
				}
			}
		})
	}
}

func TestTheThresholdsAreConfigurable(t *testing.T) {
	now := time.Now()
	path := writeList(t, sanctionsList{
		FetchedAt: now.Add(-90 * time.Minute),
		Addresses: []string{"0xABC"},
	})
	src, err := loadSanctionsSource(envWith(map[string]string{
		"SANCTIONS_FILE":       path,
		"SANCTIONS_WARN_AFTER": "30m",
		"SANCTIONS_DENY_AFTER": "1h",
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := src.status(now); st.OK {
		t.Fatalf("a 90m-old list was served under a 1h limit: %+v", st)
	}
}

// Thresholds that cannot both fire are a misconfiguration, not a
// preference: the warning exists to be seen before the denial.
func TestADenyThresholdBelowTheWarnThresholdIsRefusedAtStartup(t *testing.T) {
	path := writeList(t, sanctionsList{FetchedAt: time.Now(), Addresses: []string{"0xABC"}})
	_, err := loadSanctionsSource(envWith(map[string]string{
		"SANCTIONS_FILE":       path,
		"SANCTIONS_WARN_AFTER": "48h",
		"SANCTIONS_DENY_AFTER": "24h",
	}), nil)
	if err == nil {
		t.Fatal("a deny threshold shorter than the warn threshold was accepted")
	}
}

// A configured file that cannot be used must be fatal, never a quiet
// fallback to the build-time list. The deployment has said where its
// screening comes from; serving something years old instead, silently, is
// the exact failure this file exists to prevent.
func TestAConfiguredFileThatIsUnusableIsFatalRatherThanFallingBack(t *testing.T) {
	embedded := []byte(`{"addresses":["0xdeadbeef"]}`)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing fetched_at", body: `{"addresses":["0xABC"]}`},
		{name: "empty list", body: `{"fetched_at":"2026-01-01T00:00:00Z","addresses":[]}`},
		{name: "malformed", body: `not json at all`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sanctions.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSanctionsSource(
				envWith(map[string]string{"SANCTIONS_FILE": path}), embedded); err == nil {
				t.Fatalf("%s was accepted, and the service would have screened against "+
					"the embedded list while believing it used the feed", tc.name)
			}
		})
	}

	if _, err := loadSanctionsSource(
		envWith(map[string]string{"SANCTIONS_FILE": "/nonexistent/sanctions.json"}), embedded); err == nil {
		t.Fatal("a missing sanctions file fell back silently")
	}
}

// The embedded list still works, and says what it is.
func TestTheEmbeddedListIsUsableAndAnnouncesThatItIsAFixture(t *testing.T) {
	src, err := loadSanctionsSource(envWith(nil), sanctionsJSON)
	if err != nil {
		t.Fatalf("the embedded list did not load: %v", err)
	}
	if !src.embedded {
		t.Fatal("the embedded list was not marked as embedded")
	}
	st := src.status(time.Now())
	if !st.OK {
		t.Fatal("the embedded list refused to serve; every deployment without a feed would be down")
	}
	if st.Warning == "" || !strings.Contains(st.Warning, "cmd/ofac-sync") {
		t.Errorf("the embedded list does not tell an operator how to stop using it: %q", st.Warning)
	}
	// Its age must not be reported as a number. An embedded list is not
	// old, it is disconnected, and "3 days" would imply a sync exists.
	if st.Age != "n/a" {
		t.Errorf("the embedded list reported an age of %q, implying a feed behind it", st.Age)
	}
}

// Screening is a comparison, so casing must be settled once, at load.
func TestAddressesAreLowercasedAtLoad(t *testing.T) {
	path := writeList(t, sanctionsList{
		FetchedAt: time.Now(),
		Addresses: []string{"0xAAAA", "  0xBbBb  ", ""},
	})
	src, err := loadSanctionsSource(envWith(map[string]string{"SANCTIONS_FILE": path}), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0xaaaa", "0xbbbb"}
	if len(src.list.Addresses) != len(want) {
		t.Fatalf("got %v, want %v", src.list.Addresses, want)
	}
	for i := range want {
		if src.list.Addresses[i] != want[i] {
			t.Errorf("address[%d] = %q, want %q", i, src.list.Addresses[i], want[i])
		}
	}
}
