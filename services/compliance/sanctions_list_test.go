package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeList(t *testing.T, fetched time.Time, addrs ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sanctions.json")
	b, _ := json.Marshal(map[string]interface{}{"fetched_at": fetched, "source": "test", "addresses": addrs})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func listFor(t *testing.T, env map[string]string) *SanctionsList {
	t.Helper()
	l, err := NewSanctionsListFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNoConfiguredListRefusesRatherThanPassing(t *testing.T) {
	hit, err := listFor(t, nil).Check("0xabc")
	if err == nil || hit {
		t.Fatalf("an unconfigured list must return an error, got hit=%v err=%v", hit, err)
	}
	if _, err := (*SanctionsList)(nil).Check("0xabc"); err == nil {
		t.Fatal("a nil list must refuse")
	}
}

func TestADesignatedAddressIsAHitRegardlessOfCase(t *testing.T) {
	p := writeList(t, time.Now(), "0x8589427373D6D84E98730D7795D8f6f8731FDA16")
	l := listFor(t, map[string]string{"SANCTIONS_FILE": p})
	for _, a := range []string{"0x8589427373d6d84e98730d7795d8f6f8731fda16", " 0x8589427373D6D84E98730D7795D8f6f8731FDA16 "} {
		if hit, err := l.Check(a); err != nil || !hit {
			t.Fatalf("%q: want a hit, got hit=%v err=%v", a, hit, err)
		}
	}
	if hit, err := l.Check("0x0000000000000000000000000000000000000001"); err != nil || hit {
		t.Fatalf("an undesignated address must be clean, got hit=%v err=%v", hit, err)
	}
}

func TestAListPastTheDenyAgeRefusesToScreen(t *testing.T) {
	p := writeList(t, time.Now().Add(-100*time.Hour), "0xabc")
	l := listFor(t, map[string]string{"SANCTIONS_FILE": p})
	if _, err := l.Check("0xdef"); err == nil || !strings.Contains(err.Error(), "old") {
		t.Fatalf("a stale list must refuse and say why, got %v", err)
	}
	if st := l.Status(); !strings.Contains(st.Warning, "stale") {
		t.Fatalf("status must report stale, got %q", st.Warning)
	}
}

func TestAnAgeingListStillScreensButWarns(t *testing.T) {
	p := writeList(t, time.Now().Add(-30*time.Hour), "0xabc")
	l := listFor(t, map[string]string{"SANCTIONS_FILE": p})
	if hit, err := l.Check("0xabc"); err != nil || !hit {
		t.Fatalf("an ageing list still screens, got hit=%v err=%v", hit, err)
	}
	if st := l.Status(); !strings.Contains(st.Warning, "ageing") {
		t.Fatalf("status must warn, got %q", st.Warning)
	}
}

func TestAFileWithoutFetchedAtIsRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(p, []byte(`{"addresses":["0xabc"]}`), 0o600)
	l := listFor(t, map[string]string{"SANCTIONS_FILE": p})
	if _, err := l.Check("0xabc"); err == nil {
		t.Fatal("a list whose age cannot be known must not be used")
	}
}

func TestAnUnreadableFileRefuses(t *testing.T) {
	l := listFor(t, map[string]string{"SANCTIONS_FILE": "/nonexistent/s.json"})
	if _, err := l.Check("0xabc"); err == nil {
		t.Fatal("an unreadable list must refuse")
	}
}

func TestDenyShorterThanWarnIsAConfigurationError(t *testing.T) {
	_, err := NewSanctionsListFromEnv(func(k string) string {
		return map[string]string{"SANCTIONS_WARN_AFTER": "48h", "SANCTIONS_DENY_AFTER": "24h"}[k]
	})
	if err == nil {
		t.Fatal("expected a configuration error")
	}
}

func TestOFACClientReportsMatchesAndNeverPassesOnError(t *testing.T) {
	p := writeList(t, time.Now(), "0xbad")
	c := NewOFACClient(listFor(t, map[string]string{"SANCTIONS_FILE": p}))
	if m, err := c.CheckSDN(nil, "0xBAD"); err != nil || len(m) != 1 {
		t.Fatalf("want one match, got %v %v", m, err)
	}
	if m, err := c.CheckSDN(nil, "0xok"); err != nil || len(m) != 0 {
		t.Fatalf("want clean, got %v %v", m, err)
	}
	if _, err := NewOFACClient(nil).CheckSDN(nil, "0xok"); err == nil {
		t.Fatal("a client with no list must error")
	}
}

func TestRestrictedCountriesAreOverridableAndNormalised(t *testing.T) {
	got := restrictedCountriesFromEnv(func(string) string { return " ru, by ,xx" })
	if len(got) != 3 || got[0] != "RU" || got[1] != "BY" {
		t.Fatalf("got %v", got)
	}
	if def := restrictedCountriesFromEnv(func(string) string { return "" }); len(def) != 4 {
		t.Fatalf("default list changed: %v", def)
	}
}
