package main

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Stopping one party from speaking as another.
//
// The hole these tests close: the message handlers read from_party_id out
// of the JSON body and handed it to tss-lib as the sender. Mutual TLS
// proved the caller held *a* valid party certificate. Nothing connected
// that certificate to the party id in the body, so any party could address
// messages as any other party.
//
// That is not a small hole in a threshold protocol. The whole security
// argument for t-of-n is that an attacker below the threshold can do
// nothing; an attacker who can forge the sender of a protocol message is
// no longer confined to the parties they actually control.

func certFor(cn string) *x509.Certificate {
	return &x509.Certificate{
		Subject:      pkix.Name{CommonName: cn},
		SerialNumber: big.NewInt(42),
	}
}

// requestFrom builds a request as if it arrived over mutual TLS with the
// given client certificate common name.
func requestFrom(cn string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/tss/keygen/message", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certFor(cn)}}
	return r
}

func TestAPartySpeakingAsItselfIsAccepted(t *testing.T) {
	for _, cn := range []string{"party-1.internal", "party-2.internal", "party-11.internal"} {
		id, err := partyIDFromCommonName(cn)
		if err != nil {
			t.Fatalf("%s: %v", cn, err)
		}
		if err := authenticatePeer(requestFrom(cn), id); err != nil {
			t.Errorf("%s speaking as party %d was refused: %v", cn, id, err)
		}
	}
}

// The attack. Party 2 holds a perfectly valid certificate and claims a
// message is from party 3.
func TestAPartyCannotSpeakAsAnother(t *testing.T) {
	err := authenticatePeer(requestFrom("party-2.internal"), 3)

	if err == nil {
		t.Fatal("party 2 was allowed to send a protocol message as party 3")
	}
	var unauth *ErrUnauthenticatedPeer
	if !asUnauthenticated(err, &unauth) {
		t.Fatalf("got %T, want *ErrUnauthenticatedPeer", err)
	}
	// The error has to say who actually connected, or an operator reading
	// the log learns only that something was refused.
	if !contains(err.Error(), "party 2") || !contains(err.Error(), "party-2.internal") {
		t.Errorf("the refusal does not name the real sender: %v", err)
	}
}

func TestEveryOtherPartyIdIsRefusedForOneCertificate(t *testing.T) {
	const cn = "party-1.internal"
	for claimed := 2; claimed <= 7; claimed++ {
		if err := authenticatePeer(requestFrom(cn), claimed); err == nil {
			t.Errorf("party 1 was allowed to speak as party %d", claimed)
		}
	}
}

// -- fail-closed cases --

// Plain HTTP means there is no certificate to bind to. Refused rather
// than trusted, because "no TLS" is either a misconfiguration or an
// attacker who did not have a certificate to present.
func TestAPlainHttpRequestIsRefused(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/tss/keygen/message", nil)
	r.TLS = nil

	if err := authenticatePeer(r, 1); err == nil {
		t.Fatal("a protocol message over plain HTTP was accepted")
	}
}

func TestTlsWithNoClientCertificateIsRefused(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/tss/keygen/message", nil)
	r.TLS = &tls.ConnectionState{} // handshake, no client cert

	if err := authenticatePeer(r, 1); err == nil {
		t.Fatal("a request with no client certificate was accepted")
	}
}

// A certificate from the right CA that is not a party's. The PKI could
// legitimately issue this -- a monitoring client, say -- and it must not
// be able to inject protocol messages.
func TestACertificateThatIsNotAPartyIsRefused(t *testing.T) {
	for _, cn := range []string{
		"api-gateway.internal",
		"prometheus.internal",
		"",
		"party.internal",    // no number
		"partyx-1.internal", // not the pattern
		"notparty-1.internal",
	} {
		if err := authenticatePeer(requestFrom(cn), 1); err == nil {
			t.Errorf("a certificate with CN %q was accepted as party 1", cn)
		}
	}
}

// "party-1x" must not be read as party 1. A prefix match would let a
// certificate for one name authenticate as another.
func TestThePartyNumberIsNotAPrefixMatch(t *testing.T) {
	if _, err := partyIDFromCommonName("party-1x.internal"); err == nil {
		t.Error("party-1x.internal was parsed as a party id")
	}
	// The bare form, with no domain suffix, is still valid.
	if id, err := partyIDFromCommonName("party-3"); err != nil || id != 3 {
		t.Errorf("party-3 parsed as %d, %v", id, err)
	}
}

func TestPartyZeroIsRefused(t *testing.T) {
	// Party ids are 1-indexed throughout this service; a zero would index
	// off the front of the sorted party list.
	if _, err := partyIDFromCommonName("party-0.internal"); err == nil {
		t.Error("party-0 was accepted as a party id")
	}
}

// -- the escape hatch --

// The development path exists, and it has to be opted into exactly.
func TestEnforcementCanBeDisabledForLocalDevelopment(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	r := httptest.NewRequest(http.MethodPost, "/tss/keygen/message", nil)
	if err := authenticatePeer(r, 9); err != nil {
		t.Errorf("enforcement was not disabled: %v", err)
	}
}

// A flag whose other position turns off an authentication check must not
// be satisfied by anything that merely looks true.
func TestAnythingOtherThanExactlyOneLeavesEnforcementOn(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "yes", "0", "", " 1", "1 ", "on"} {
		t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", value)
		r := httptest.NewRequest(http.MethodPost, "/tss/keygen/message", nil)
		if err := authenticatePeer(r, 1); err == nil {
			t.Errorf("TSS_ALLOW_UNAUTHENTICATED_PEERS=%q disabled enforcement", value)
		}
	}
}

func TestEnforcementIsOnByDefault(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "")
	if peerEnforcementDisabled() {
		t.Fatal("peer authentication is disabled by default")
	}
}

// -- logging --

// Enough to identify which certificate was used, and nothing more. A log
// line is the wrong place for a full certificate.
func TestThePeerSummaryNamesTheCertificateWithoutDumpingIt(t *testing.T) {
	state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certFor("party-2.internal")}}

	got := peerCertificateSummary(state)

	if !contains(got, "party-2.internal") || !contains(got, "42") {
		t.Errorf("summary does not identify the certificate: %q", got)
	}
	if len(got) > 120 {
		t.Errorf("summary is too long to be a log line: %q", got)
	}
	if peerCertificateSummary(nil) == "" {
		t.Error("a nil connection state produced an empty summary")
	}
}

func asUnauthenticated(err error, target **ErrUnauthenticatedPeer) bool {
	if e, ok := err.(*ErrUnauthenticatedPeer); ok {
		*target = e
		return true
	}
	return false
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
