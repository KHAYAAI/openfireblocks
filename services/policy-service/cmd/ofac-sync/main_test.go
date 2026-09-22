package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cut-down SDN.XML in the real schema.
//
// Real structure, invented entries. It covers the four cases that matter:
// an address in the structured idType field, one only in free-text
// remarks, several addresses on one designation, and an entry with no
// crypto address at all that must not contribute anything.
const sdnFixture = `<?xml version="1.0" encoding="UTF-8"?>
<sdnList xmlns="">
  <publshInformation>
    <Publish_Date>01/15/2026</Publish_Date>
    <Record_Count>4</Record_Count>
    <Date_Recorded>2026-01-15</Date_Recorded>
  </publshInformation>
  <sdnEntry>
    <uid>1001</uid>
    <lastName>EXAMPLE MIXER</lastName>
    <sdnType>Entity</sdnType>
    <remarks>Sanctioned under CYBER2.</remarks>
    <idList>
      <id>
        <uid>5001</uid>
        <idType>Digital Currency Address - ETH</idType>
        <idNumber>0x8589427373D6D84E98730D7795D8f6f8731FDA16</idNumber>
      </id>
      <id>
        <uid>5002</uid>
        <idType>Digital Currency Address - XBT</idType>
        <idNumber>1DUb2YYbQA1jjaNYzVXLZ7ZioEhLXtbUru</idNumber>
      </id>
      <id>
        <uid>5003</uid>
        <idType>Email Address</idType>
        <idNumber>nobody@example.invalid</idNumber>
      </id>
    </idList>
  </sdnEntry>
  <sdnEntry>
    <uid>1002</uid>
    <lastName>REMARKS ONLY</lastName>
    <sdnType>Individual</sdnType>
    <remarks>Digital Currency Address - XBT 12t9YDPgwueZ9NyMgw519p7AA8isjr6SMw; additional text.</remarks>
    <idList></idList>
  </sdnEntry>
  <sdnEntry>
    <uid>1003</uid>
    <lastName>NO CRYPTO</lastName>
    <sdnType>Individual</sdnType>
    <remarks>Passport 123456 (Country).</remarks>
    <idList>
      <id>
        <uid>5004</uid>
        <idType>Passport</idType>
        <idNumber>123456</idNumber>
      </id>
    </idList>
  </sdnEntry>
  <sdnEntry>
    <uid>1004</uid>
    <lastName>DUPLICATE ACROSS FIELDS</lastName>
    <sdnType>Entity</sdnType>
    <remarks>Digital Currency Address - ETH 0x722122dF12D4e14e13Ac3b6895a86e84145b6967</remarks>
    <idList>
      <id>
        <uid>5005</uid>
        <idType>Digital Currency Address - ETH</idType>
        <idNumber>0x722122dF12D4e14e13Ac3b6895a86e84145b6967</idNumber>
      </id>
    </idList>
  </sdnEntry>
</sdnList>`

func TestParseFindsAddressesInBothPlacesAndNowhereElse(t *testing.T) {
	addresses, published, err := parse([]byte(sdnFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if published != "2026-01-15" {
		t.Errorf("published date = %q, want 2026-01-15", published)
	}

	want := []string{
		"0x722122df12d4e14e13ac3b6895a86e84145b6967", // in both fields, once
		"0x8589427373d6d84e98730d7795d8f6f8731fda16", // structured
		"12t9ydpgwueznymgw519p7aa8isjr6smw",          // remarks only (lowercased)
		"1dub2yybqa1jjanyzvxlz7zioehlxtburu",         // structured, base58
	}
	// The remarks-only address lowercases differently than a naive reader
	// expects, so build the expectation the same way the code does rather
	// than by hand.
	want[2] = strings.ToLower("12t9YDPgwueZ9NyMgw519p7AA8isjr6SMw")

	if len(addresses) != len(want) {
		t.Fatalf("got %d addresses %v, want %d %v", len(addresses), addresses, len(want), want)
	}
	for i := range want {
		if addresses[i] != want[i] {
			t.Errorf("address[%d] = %q, want %q", i, addresses[i], want[i])
		}
	}

	// Nothing that is not a crypto address may leak in. An email or a
	// passport number on the screening list blocks nothing and makes the
	// list's size meaningless as a health signal.
	for _, a := range addresses {
		if strings.Contains(a, "@") || a == "123456" {
			t.Errorf("a non-address %q reached the screening list", a)
		}
	}
}

// Everything is lowercased, because screening is a comparison.
func TestEveryAddressIsLowercased(t *testing.T) {
	addresses, _, err := parse([]byte(sdnFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addresses {
		if a != strings.ToLower(a) {
			t.Errorf("%q was not lowercased; a mixed-case comparison lets a designated address through", a)
		}
	}
}

// Two runs over unchanged source must produce an identical file, or the
// diffs are unreadable and the one run that mattered is invisible.
func TestOutputIsStableAcrossRuns(t *testing.T) {
	a, _, err := parse([]byte(sdnFixture))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := parse([]byte(sdnFixture))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("two parses of the same input disagreed:\n%v\n%v", a, b)
	}
}

func TestSomethingThatIsNotTheSdnSchemaIsRefused(t *testing.T) {
	// What a moved URL actually returns: an HTML error page.
	if _, _, err := parse([]byte("<html><body>404 Not Found</body></html>")); err == nil {
		t.Fatal("an HTML error page was accepted as a sanctions list")
	}
}

// The write must be atomic, because the policy service reads this file
// while this tool writes it.
func TestWriteAtomicLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sanctions.json")

	body, err := json.Marshal(output{Addresses: []string{"0xabc"}, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, body); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	var parsed output
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("what was written is not valid JSON: %v", err)
	}
	if len(parsed.Addresses) != 1 {
		t.Fatalf("round trip lost the addresses: %+v", parsed)
	}

	// No temporary files left behind. One accumulating file per sync run
	// fills the volume and eventually stops the sync it belongs to.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sanctions-") {
			t.Errorf("a temporary file %q was left behind", e.Name())
		}
	}
}

// Overwriting must replace, not merge. A list that only grows can never
// drop a delisted address, which blocks a customer's money for no lawful
// reason.
func TestWritingTwiceReplacesRatherThanAccumulates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sanctions.json")

	first, _ := json.Marshal(output{Addresses: []string{"0xaaa", "0xbbb"}})
	if err := writeAtomic(path, first); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(output{Addresses: []string{"0xccc"}})
	if err := writeAtomic(path, second); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed output
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Addresses) != 1 || parsed.Addresses[0] != "0xccc" {
		t.Fatalf("the second write did not replace the first: %v", parsed.Addresses)
	}
}
