// ofac-sync fetches OFAC's SDN list and writes the digital-currency
// addresses on it to a file services/policy-service screens against.
//
// It exists because the sanctions list was embedded in the policy
// service's binary, which made it exactly as current as the last
// redeploy. OFAC designates new addresses continuously; a list frozen at
// build time is a compliance control with a slowly expanding hole in it.
//
// Run it as a CronJob (the chart has one) writing to a volume the policy
// service reads. The policy service refuses to serve a list past
// SANCTIONS_DENY_AFTER, so a sync that quietly stops becomes visible as a
// warning and then as a refusal, rather than as screening that silently
// stops finding anything.
//
// # What it parses
//
// OFAC publishes crypto addresses in SDN.XML as id entries whose idType
// begins "Digital Currency Address - ", e.g.
//
//	<id><uid>1</uid><idType>Digital Currency Address - XBT</idType>
//	    <idNumber>1AbC...</idNumber></id>
//
// and, historically and inconsistently, in free-text <remarks>. Both are
// read: the structured field because it is the supported one, and remarks
// because entries have appeared there and a screening list that misses
// designated addresses on a technicality is worse than useless.
//
// # What it deliberately does not do
//
// It does not merge, append or de-duplicate against a previous run. Each
// run writes what the source currently says. A sanctions list that
// accumulates is one that can never remove a delisted address, and
// screening against addresses OFAC has removed blocks customers' money
// for no lawful reason -- a different compliance problem, in the other
// direction.
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// defaultSource is OFAC's current SDN.XML endpoint.
//
// Overridable, and it has to be: Treasury has moved this URL more than
// once, and a hard-coded address that 404s turns into a silently stale
// list. A deployment that finds this broken should be able to repoint it
// without waiting for a release.
const defaultSource = "https://sanctionslistservice.ofac.treas.gov/api/PublicationPreview/exports/SDN.XML"

// digitalCurrencyPrefix is how the structured field marks a crypto
// address.
const digitalCurrencyPrefix = "Digital Currency Address - "

// remarksPattern catches the same thing in free text.
//
// Deliberately loose on the address and strict on the label: the label is
// what establishes that the following token is a designated address, and
// the address formats vary by chain (base58, bech32, 0x-hex) in ways a
// tight pattern gets wrong as new chains are added.
var remarksPattern = regexp.MustCompile(
	`Digital Currency Address\s*-\s*[A-Za-z0-9]+\s+([A-Za-z0-9:_-]{16,128})`)

type sdnList struct {
	XMLName     xml.Name `xml:"sdnList"`
	PublishInfo struct {
		DateRecorded string `xml:"Date_Recorded"`
	} `xml:"publshInformation"`
	Entries []struct {
		UID     string `xml:"uid"`
		Remarks string `xml:"remarks"`
		IDList  struct {
			IDs []struct {
				IDType   string `xml:"idType"`
				IDNumber string `xml:"idNumber"`
			} `xml:"id"`
		} `xml:"idList"`
	} `xml:"sdnEntry"`
}

type output struct {
	FetchedAt   time.Time `json:"fetched_at"`
	Source      string    `json:"source"`
	PublishedAt string    `json:"published_at,omitempty"`
	Addresses   []string  `json:"addresses"`
}

func main() {
	var (
		source  = flag.String("source", envOr("OFAC_SOURCE_URL", defaultSource), "SDN.XML URL")
		out     = flag.String("out", envOr("SANCTIONS_FILE", "/var/lib/openfireblocks/sanctions.json"), "where to write the list")
		timeout = flag.Duration("timeout", 2*time.Minute, "fetch timeout")
		minimum = flag.Int("min-addresses", 100, "refuse to write a list smaller than this")
		// Looping is done here rather than by a shell wrapper, because the
		// runtime image is distroless and has no shell. A sidecar running
		// `sh -c "while true; ..."` against it does not start at all.
		interval = flag.Duration("interval", 0, "when set, keep running and re-sync on this interval")
		// Read a local SDN.XML instead of fetching one.
		//
		// Two reasons, and the second is the one that made this worth a
		// flag. An air-gapped deployment cannot reach Treasury at all and
		// has to move the file in by hand. And an operator who wants to
		// know whether this tool understands today's SDN.XML -- rather
		// than the fixture its tests use -- can download the file with
		// anything and point this at it, which is a check that needs no
		// credentials, writes nothing, and takes a second.
		from = flag.String("file", "", "parse this local SDN.XML instead of fetching one")
		// Parse and report without writing. The verification mode.
		check = flag.Bool("check", false, "parse the source and report what was found, without writing")
	)
	flag.Parse()

	if *check {
		raw, err := read(*source, *from, *timeout)
		if err != nil {
			fail("%v", err)
		}
		addresses, published, err := parse(raw)
		if err != nil {
			fail("parsing: %v", err)
		}
		fmt.Printf("parsed %d digital-currency addresses (published %s)\n",
			len(addresses), orNone(published))
		if len(addresses) < *minimum {
			fail("that is below the floor of %d; this tool does not understand what it was given",
				*minimum)
		}
		// A sample, so an operator can eyeball that these look like
		// addresses rather than like fragments of prose the regex caught.
		for i, a := range addresses {
			if i == 5 {
				fmt.Printf("  ... and %d more\n", len(addresses)-5)
				break
			}
			fmt.Printf("  %s\n", a)
		}
		return
	}

	if err := syncOnce(*source, *from, *out, *timeout, *minimum); err != nil {
		fail("%v", err)
	}
	if *interval <= 0 {
		return
	}

	// From here on a failure is logged and retried rather than fatal. The
	// first sync must succeed -- an initContainer that "succeeds" without
	// a list leaves the service with nothing to screen against -- but
	// once a good list is on disk, exiting on a transient Treasury outage
	// would replace a list that is merely aging with no refresher at all.
	// The service's own staleness check turns a persistently failing sync
	// into a visible warning and then a refusal, which is the right way
	// for this to surface.
	for range time.Tick(*interval) {
		if err := syncOnce(*source, *from, *out, *timeout, *minimum); err != nil {
			fmt.Fprintf(os.Stderr,
				"ofac-sync: %v\nthe previous list is still in place and will age into a warning\n", err)
		}
	}
}

// read gets the SDN list, from a file when one is named and from the
// network otherwise.
func read(source, from string, timeout time.Duration) ([]byte, error) {
	if from != "" {
		raw, err := os.ReadFile(from)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", from, err)
		}
		return raw, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := fetch(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", source, err)
	}
	return raw, nil
}

func syncOnce(source, from, out string, timeout time.Duration, minimum int) error {
	raw, err := read(source, from, timeout)
	if err != nil {
		return err
	}

	addresses, published, err := parse(raw)
	if err != nil {
		return fmt.Errorf("parsing the SDN list: %w", err)
	}

	// A sanity floor, and the reason is a specific failure this would
	// otherwise cause. If Treasury moves the URL and the new one returns
	// an HTML error page, or changes the schema, parsing yields zero or a
	// handful of addresses. Writing that over a good list replaces real
	// screening with none, and the policy service cannot tell the
	// difference -- the file is fresh and well-formed, it just does not
	// contain anything. Better to leave the previous list in place and
	// let it age into a visible warning.
	if len(addresses) < minimum {
		return fmt.Errorf("the source yielded only %d addresses, below the floor of %d. "+
			"The previous list has been left in place. Check whether %s still serves SDN.XML "+
			"in the expected schema before lowering --min-addresses",
			len(addresses), minimum, source)
	}

	origin := source
	if from != "" {
		origin = "file:" + from
	}
	body, err := json.MarshalIndent(output{
		FetchedAt:   time.Now().UTC(),
		Source:      origin,
		PublishedAt: published,
		Addresses:   addresses,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the list: %w", err)
	}

	if err := writeAtomic(out, body); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	fmt.Printf("wrote %d designated addresses to %s (published %s)\n",
		len(addresses), out, orNone(published))
	return nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// parse extracts every digital-currency address, from both places OFAC
// puts them.
func parse(raw []byte) (addresses []string, published string, err error) {
	var list sdnList
	if err := xml.Unmarshal(raw, &list); err != nil {
		return nil, "", fmt.Errorf("not the SDN.XML schema this tool understands: %w", err)
	}

	// A set, because the same address appears in both the structured
	// field and the remarks on some entries, and because one address can
	// be listed against several designations.
	seen := make(map[string]struct{})
	add := func(a string) {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" {
			seen[a] = struct{}{}
		}
	}

	for _, entry := range list.Entries {
		for _, id := range entry.IDList.IDs {
			if strings.HasPrefix(id.IDType, digitalCurrencyPrefix) {
				add(id.IDNumber)
			}
		}
		if entry.Remarks != "" {
			for _, m := range remarksPattern.FindAllStringSubmatch(entry.Remarks, -1) {
				add(m[1])
			}
		}
	}

	addresses = make([]string, 0, len(seen))
	for a := range seen {
		addresses = append(addresses, a)
	}
	// Sorted so two runs over unchanged source produce an identical file.
	// A list that reshuffles on every sync makes its own diffs unreadable
	// and hides the one run where something actually changed.
	sort.Strings(addresses)
	return addresses, strings.TrimSpace(list.PublishInfo.DateRecorded), nil
}

// writeAtomic writes via a temporary file and a rename.
//
// The policy service reads this file while this process writes it. A
// plain write leaves a window in which the reader sees a truncated file
// and refuses to start; a rename on the same filesystem does not.
func writeAtomic(path string, body []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sanctions-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	// Flushed before the rename. Without it a node that loses power
	// between the two leaves a correctly-named, empty file, which is the
	// one outcome worse than no file at all.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ofac-sync: "+format+"\n", args...)
	os.Exit(1)
}
