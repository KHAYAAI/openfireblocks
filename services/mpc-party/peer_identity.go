package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
)

// Binding a relayed protocol message to the peer that actually sent it.
//
// The gap this closes: every /tss/*/message handler read from_party_id out
// of the JSON body and passed it to tss-lib's UpdateFromBytes as the
// sender. Mutual TLS proved the connection came from *a* holder of a
// Vault-issued party certificate; nothing connected that certificate to
// the party id the body claimed.
//
// In a threshold protocol that is not a small hole. The entire security
// argument for t-of-n is that an attacker who controls fewer than t
// parties learns nothing and can do nothing. An attacker who controls one
// party and can address messages as any other party is not confined to one
// party any more -- they can feed a victim a whole round of forged
// contributions and drive its state machine wherever the protocol's
// robustness assumptions stop holding. tss-lib assumes an authenticated
// channel per peer; it does not authenticate senders itself, and its
// documentation is explicit that this is the integrator's job.
//
// The fix is to take the sender's identity from the one thing the caller
// cannot choose: the client certificate they completed the TLS handshake
// with. The Vault PKI issues each party a certificate whose common name is
// party-N.internal (see services/vault-pki-init), so the party number is
// already there and already verified against the CA.
//
// Prior art: fystack/mpcium does the equivalent with per-message Ed25519
// signatures over a NATS bus, plus non-replayable session identifiers.
// Signing each message is the stronger construction -- it survives a
// terminating proxy, which channel binding does not -- and is the right
// next step. This is the part that removes the hole without changing the
// transport.

// partyCNPattern matches the common name the PKI issues: party-1.internal.
var partyCNPattern = regexp.MustCompile(`^party-(\d+)(\.|$)`)

// ErrUnauthenticatedPeer is returned when a relayed message cannot be
// attributed to the party it claims to come from.
type ErrUnauthenticatedPeer struct {
	Claimed int
	Reason  string
}

func (e *ErrUnauthenticatedPeer) Error() string {
	return fmt.Sprintf("refusing a protocol message claiming to be from party %d: %s", e.Claimed, e.Reason)
}

// peerEnforcementDisabled reports whether sender binding has been turned
// off deliberately.
//
// It exists for the plain-HTTP development path (docker-compose, a laptop,
// the integration tests that run parties in one process without a PKI),
// where there is no client certificate to bind to and the alternative
// would be a test suite that cannot run.
//
// Off by default, and it has to be set to the exact string "1": a
// mistyped value leaves enforcement on, which is the correct direction for
// a flag whose other position disables an authentication check.
func peerEnforcementDisabled() bool {
	return os.Getenv("TSS_ALLOW_UNAUTHENTICATED_PEERS") == "1"
}

// authenticatePeer checks that the party id a relayed message claims is the
// party id of the certificate the sender presented.
//
// Fails closed in every ambiguous case. A request with no TLS state, no
// client certificate, or a certificate whose common name does not name a
// party is refused rather than waved through -- each of those is either a
// misconfiguration or an attempt, and neither should be able to inject a
// protocol message.
func authenticatePeer(r *http.Request, claimed int) error {
	if peerEnforcementDisabled() {
		return nil
	}

	if r.TLS == nil {
		return &ErrUnauthenticatedPeer{
			Claimed: claimed,
			Reason: "the request did not arrive over TLS, so the sender cannot be identified. " +
				"Party-to-party traffic must use mutual TLS; set TSS_ALLOW_UNAUTHENTICATED_PEERS=1 " +
				"only for local development",
		}
	}
	if len(r.TLS.PeerCertificates) == 0 {
		return &ErrUnauthenticatedPeer{
			Claimed: claimed,
			Reason:  "no client certificate was presented",
		}
	}

	// PeerCertificates[0] is the leaf, and the server was configured with
	// RequireAndVerifyClientCert, so it has already been verified against
	// the CA by the time a handler runs.
	cn := r.TLS.PeerCertificates[0].Subject.CommonName
	actual, err := partyIDFromCommonName(cn)
	if err != nil {
		return &ErrUnauthenticatedPeer{Claimed: claimed, Reason: err.Error()}
	}
	if actual != claimed {
		return &ErrUnauthenticatedPeer{
			Claimed: claimed,
			Reason: fmt.Sprintf("the client certificate belongs to party %d (CN %q). "+
				"A party may only speak as itself", actual, cn),
		}
	}
	return nil
}

// partyIDFromCommonName extracts N from a party-N.internal common name.
func partyIDFromCommonName(cn string) (int, error) {
	match := partyCNPattern.FindStringSubmatch(cn)
	if match == nil {
		return 0, fmt.Errorf("the client certificate's common name %q does not identify a party; "+
			"the PKI issues party-N.internal", cn)
	}
	id, err := strconv.Atoi(match[1])
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("the client certificate's common name %q carries no usable party number", cn)
	}
	return id, nil
}

// peerCertificateSummary describes the authenticated peer, for logging.
//
// Only the common name and serial: enough to identify which certificate
// was used when investigating, and nothing that would put key material or
// a full certificate into a log line.
func peerCertificateSummary(state *tls.ConnectionState) string {
	if state == nil || len(state.PeerCertificates) == 0 {
		return "no client certificate"
	}
	leaf := state.PeerCertificates[0]
	return fmt.Sprintf("CN=%s serial=%s", leaf.Subject.CommonName, leaf.SerialNumber)
}
