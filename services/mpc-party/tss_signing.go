package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"sync"

	"github.com/bnb-chain/tss-lib/v2/common"
	tsssigning "github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	eddsasigning "github.com/bnb-chain/tss-lib/v2/eddsa/signing"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// Real, network-driven threshold-ECDSA signing -- the counterpart to
// tss_party.go's real DKG. Same relationship to
// services/mpc-signer/tss/tss.go's already-verified Sign() as StartKeygen
// has to its Keygen(): same library, same protocol, same message-driven
// state machine, but relayed over real HTTP between independent processes
// instead of in-process Go channels.
//
// A signing ceremony references a completed keygen ceremony by ID and
// builds its committee from that ceremony's parties. Two things have to be
// true of that committee, and they pull in opposite directions:
//
//   - The *order* must match the DKG's, because the save data is keyed by
//     Shamir share id. tss-lib's BuildLocalSaveDataSubset re-associates
//     Ks/BigXj/PaillierPKs/NTildej/H1j/H2j by looking each committee
//     member's key up in the original data, so the committee must contain
//     exactly the DKG's PartyID keys, sorted the same way.
//
//   - The *indices* must be renumbered 0..len(committee)-1. tss-lib subsets
//     the save data to the committee, then indexes it with this party's
//     PartyID.Index. Carrying the original DKG index into a smaller
//     committee therefore indexes past the end of the subset.
//
// This code previously did the first and not the second, on the reasoning
// that indices were "baked into" the save data. They are not -- tss-lib
// rebuilds them. Committees were always [1, 2] in practice, where the
// original and committee indices happen to coincide, so it worked. The
// first committee that did not start at party 1 (a committee of [2, 3],
// chosen because party 1's node had been drained) panicked inside tss-lib
// with "PrepareForSigning: len(ks) <= i".
//
// So: filter the DKG's sorted ids to the committee, then re-sort *copies*
// through tsscommon.SortPartyIDs, which assigns fresh indices. Copies
// because SortPartyIDs assigns Index in place, and mutating the keygen
// ceremony's own PartyIDs would corrupt every later signing that reads
// them.

// tssSigningCeremony holds one in-flight or completed signing ceremony.
type tssSigningCeremony struct {
	mu           sync.Mutex
	status       ceremonyStatus
	errorMessage string

	selfPartyID int
	committee   tsscommon.SortedPartyIDs // subset of the DKG parties, original relative order preserved
	peers       map[int]string           // partyId -> base URL, restricted to committee members
	localParty  tsscommon.Party
	curve       Curve  // decides the signature encoding below
	signature   string // hex: 65-byte [R||S||V] on secp256k1, 64-byte [R||S] on ed25519
}

// StartSigning begins a threshold signing ceremony over a 32-byte message
// hash, using the key produced by a prior, completed keygen ceremony.
// committeePartyIDs must be exactly threshold+1 party IDs (the DKG
// ceremony's own threshold, not re-specified here since it's fixed by the
// key), all of which must have taken part in that DKG, and must be sent
// identically (same members, same set) to every party in the committee by
// the signing initiator -- mirroring how StartKeygen's peers map must be
// identical across all parties.
func (m *TSSPartyManager) StartSigning(signID, keygenCeremonyID string, messageHash []byte, committeePartyIDs []int) error {
	if len(messageHash) == 0 {
		return fmt.Errorf("nothing to sign")
	}

	m.mu.Lock()
	keygenCeremony, ok := m.ceremonies[keygenCeremonyID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown keygen ceremony %s", keygenCeremonyID)
	}

	keygenCeremony.mu.Lock()
	if keygenCeremony.status != ceremonyCompleted || keygenCeremony.saveData == nil {
		status := keygenCeremony.status
		keygenCeremony.mu.Unlock()
		return fmt.Errorf("keygen ceremony %s has not completed (status: %s)", keygenCeremonyID, status)
	}
	share := keygenCeremony.saveData

	// The length rule is per curve, so it is checked here rather than at
	// the top -- ECDSA signs a 32-byte digest and nothing else, while
	// Ed25519 signs the message itself and has no fixed length. Applying
	// the ECDSA rule to both would have made every Solana transaction
	// unsignable.
	if share.Curve == CurveSecp256k1 && len(messageHash) != 32 {
		return fmt.Errorf("a secp256k1 signature is over a 32-byte digest, got %d bytes", len(messageHash))
	}
	fullSortedIDs := keygenCeremony.sortedIDs
	fullPeers := keygenCeremony.peers
	threshold := keygenCeremony.threshold
	keygenCeremony.mu.Unlock()

	if len(committeePartyIDs) != threshold+1 {
		return fmt.Errorf("signing committee must have exactly threshold+1=%d members, got %d", threshold+1, len(committeePartyIDs))
	}

	selfIncluded := false
	wanted := make(map[int]bool, len(committeePartyIDs))
	for _, id := range committeePartyIDs {
		wanted[id] = true
		if id == m.partyID {
			selfIncluded = true
		}
	}
	if !selfIncluded {
		return fmt.Errorf("this party (%d) is not a member of the requested signing committee", m.partyID)
	}

	// Select the committee out of the DKG's parties, as fresh PartyID
	// values, then let tsscommon.SortPartyIDs assign committee-relative
	// indices. See the package doc comment for why both halves matter and
	// why these must be copies.
	unsorted := make(tsscommon.UnSortedPartyIDs, 0, len(committeePartyIDs))
	peers := make(map[int]string, len(committeePartyIDs))
	for _, sortedID := range fullSortedIDs {
		id := int(sortedID.KeyInt().Int64())
		if wanted[id] {
			unsorted = append(unsorted,
				tsscommon.NewPartyID(sortedID.Id, sortedID.Moniker, sortedID.KeyInt()))
			peers[id] = fullPeers[id]
		}
	}
	committee := tsscommon.SortPartyIDs(unsorted)
	if len(committee) != len(committeePartyIDs) {
		return fmt.Errorf("one or more requested committee party IDs were not part of the original DKG ceremony %s", keygenCeremonyID)
	}

	self := findPartyID(committee, m.partyID)
	if self == nil {
		return fmt.Errorf("failed to resolve own party ID %d within signing committee", m.partyID)
	}

	ceremony := &tssSigningCeremony{
		status:      ceremonyInProgress,
		selfPartyID: m.partyID,
		committee:   committee,
		peers:       peers,
	}
	m.signMu.Lock()
	m.signings[signID] = ceremony
	m.signMu.Unlock()

	ceremony.curve = share.Curve
	go m.runSigning(signID, ceremony, committee, peers, self, threshold, share, messageHash)

	return nil
}

// runSigning constructs and drives this party's signing.LocalParty to
// completion. Unlike keygen there's no slow pre-params step here (that
// happened once at DKG time), so unlike runKeygen there's no separate
// "not ready yet" window to worry about in practice -- but
// HandleIncomingSignMessage still reports ErrCeremonyNotReady defensively
// for the brief span between StartSigning returning and this goroutine
// actually constructing localParty.
func (m *TSSPartyManager) runSigning(
	signID string,
	ceremony *tssSigningCeremony,
	committee tsscommon.SortedPartyIDs,
	peers map[int]string,
	self *tsscommon.PartyID,
	threshold int,
	share *KeyShare,
	messageHash []byte,
) {
	ec, err := share.Curve.ellipticCurve()
	if err != nil {
		m.failSigning(signID, err)
		return
	}

	peerCtx := tsscommon.NewPeerContext(committee)
	params := tsscommon.NewParameters(ec, peerCtx, self, len(committee), threshold)
	msg := new(big.Int).SetBytes(messageHash)

	outCh := make(chan tsscommon.Message, len(committee)*len(committee))
	// The same type for both curves, which is why everything downstream of
	// here -- the relay loop, the completion path, the signature encoding
	// -- needs no branch at all.
	endCh := make(chan common.SignatureData, 1)
	errCh := make(chan *tsscommon.Error, 1)

	var localParty tsscommon.Party
	switch share.Curve {
	case CurveSecp256k1:
		localParty = tsssigning.NewLocalParty(msg, params, *share.ECDSA, outCh, endCh)
	case CurveEd25519:
		// Note what `msg` is here. For ECDSA it is a hash; Ed25519 signs
		// the message itself, and services/mpc-signer/chains/solana.go
		// documents that its "messageHash" parameter is really the
		// serialised message. The caller decides which it passed; this
		// only signs the bytes it is given.
		localParty = eddsasigning.NewLocalParty(msg, params, *share.EdDSA, outCh, endCh)
	default:
		m.failSigning(signID, fmt.Errorf("unknown curve %q", share.Curve))
		return
	}

	ceremony.mu.Lock()
	ceremony.localParty = localParty
	ceremony.mu.Unlock()

	if err := localParty.Start(); err != nil {
		m.failSigning(signID, fmt.Errorf("failed to start local party: %w", err))
		return
	}

	m.driveSigning(signID, ceremony, outCh, endCh, errCh)
}

func (m *TSSPartyManager) driveSigning(
	signID string,
	ceremony *tssSigningCeremony,
	outCh chan tsscommon.Message,
	endCh chan common.SignatureData,
	errCh chan *tsscommon.Error,
) {
	for {
		select {
		case msg := <-outCh:
			if err := m.relaySignMessage(signID, ceremony, msg); err != nil {
				m.failSigning(signID, fmt.Errorf("failed to relay message: %w", err))
				return
			}
		case err := <-errCh:
			m.failSigning(signID, fmt.Errorf("tss-lib protocol error: %w", err))
			return
		// R/S/SignatureRecovery are copied out of the channel receive
		// immediately rather than assigned into an outer-scope
		// common.SignatureData variable -- see the matching comment in
		// services/mpc-signer/tss/tss.go for why (go vet's copylocks
		// check on the channel receive itself is unavoidable without
		// forking tss-lib's public API; documented there).
		case sd := <-endCh:
			// sd.Signature is the encoding tss-lib itself produced, which
			// for Ed25519 is the only correct one: R and S are
			// little-endian there, and assembling them from sd.R/sd.S the
			// way the ECDSA path does yields 64 well-formed bytes that no
			// verifier accepts.
			r, s, recovery, encoded := sd.R, sd.S, sd.SignatureRecovery, sd.Signature
			m.completeSigning(signID, ceremony, r, s, recovery, encoded)
			return
		}
	}
}

func (m *TSSPartyManager) relaySignMessage(signID string, ceremony *tssSigningCeremony, msg tsscommon.Message) error {
	data, _, err := msg.WireBytes()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	envelope := tssSignMessageEnvelope{
		SignID:      signID,
		FromPartyID: ceremony.selfPartyID,
		IsBroadcast: msg.IsBroadcast(),
		WireBytes:   base64.StdEncoding.EncodeToString(data),
	}

	return relayTSSMessage(ceremony.selfPartyID, ceremony.peers, ceremony.committee, msg, func(baseURL string) error {
		return postTSSSignMessage(m.client, baseURL, envelope)
	})
}

func (m *TSSPartyManager) failSigning(signID string, err error) {
	m.signMu.Lock()
	ceremony, ok := m.signings[signID]
	m.signMu.Unlock()
	if !ok {
		log.Printf("signing ceremony %s failed but was never registered: %v", signID, err)
		return
	}
	ceremony.mu.Lock()
	ceremony.status = ceremonyFailed
	ceremony.errorMessage = err.Error()
	ceremony.mu.Unlock()
	log.Printf("signing ceremony %s failed: %v", signID, err)
}

func (m *TSSPartyManager) completeSigning(signID string, ceremony *tssSigningCeremony, r, s []byte, recovery []byte, encoded []byte) {
	// The encoding is per curve, and getting it wrong produces a
	// well-formed signature that the chain rejects.
	//
	// secp256k1: 65 bytes [R || S || V], as services/mpc-signer/tss's
	// Sign() does, where V is the recovery id Ethereum uses to recover the
	// signer's address.
	//
	// Ed25519: 64 bytes [R || S], and no recovery byte -- Ed25519 has no
	// public-key recovery at all, so a 65th byte would simply make the
	// signature invalid. Solana verifies exactly 64.
	var sig []byte
	if ceremony.curve == CurveEd25519 {
		// Taken as tss-lib encoded it. R and S are little-endian for
		// Ed25519, so building the 64 bytes from sd.R and sd.S the way the
		// branch below does produces a signature of exactly the right
		// length that fails every verification.
		sig = encoded
	} else {
		sig = make([]byte, 65)
		copy(sig[32-len(r):32], r)
		copy(sig[64-len(s):64], s)
		if len(recovery) > 0 {
			sig[64] = recovery[0]
		}
	}

	ceremony.mu.Lock()
	ceremony.status = ceremonyCompleted
	ceremony.signature = hex.EncodeToString(sig)
	ceremony.mu.Unlock()

	log.Printf("signing ceremony %s completed: party %d produced its share of the signature", signID, ceremony.selfPartyID)
}

// ErrSigningNotReady mirrors ErrCeremonyNotReady for the signing path --
// see that var's doc comment in tss_party.go.
var ErrSigningNotReady = fmt.Errorf("signing ceremony registered but not yet ready to receive messages")

// HandleIncomingSignMessage feeds a relayed protocol message into the
// local signing party's state machine. Called by the HTTP handler for
// /tss/sign/message.
func (m *TSSPartyManager) HandleIncomingSignMessage(env tssSignMessageEnvelope) error {
	m.signMu.Lock()
	ceremony, ok := m.signings[env.SignID]
	m.signMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown signing ceremony %s", env.SignID)
	}

	ceremony.mu.Lock()
	localParty := ceremony.localParty
	committee := ceremony.committee
	ceremony.mu.Unlock()
	if localParty == nil {
		return ErrSigningNotReady
	}

	from := findPartyID(committee, env.FromPartyID)
	if from == nil {
		return fmt.Errorf("unknown sender party %d for signing ceremony %s", env.FromPartyID, env.SignID)
	}

	wireBytes, err := base64.StdEncoding.DecodeString(env.WireBytes)
	if err != nil {
		return fmt.Errorf("failed to decode wire bytes: %w", err)
	}

	if _, err := localParty.UpdateFromBytes(wireBytes, from, env.IsBroadcast); err != nil {
		return fmt.Errorf("failed to update local party state: %w", err)
	}
	return nil
}

// SigningStatusResult is what GET /tss/sign/status returns.
type SigningStatusResult struct {
	SignID    string         `json:"sign_id"`
	PartyID   int            `json:"party_id"`
	Status    ceremonyStatus `json:"status"`
	Error     string         `json:"error,omitempty"`
	Signature string         `json:"signature,omitempty"` // hex, 65 bytes [R||S||V], once completed
}

func (m *TSSPartyManager) GetSigningStatus(signID string) (*SigningStatusResult, error) {
	m.signMu.Lock()
	ceremony, ok := m.signings[signID]
	m.signMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown signing ceremony %s", signID)
	}

	ceremony.mu.Lock()
	defer ceremony.mu.Unlock()
	return &SigningStatusResult{
		SignID:    signID,
		PartyID:   ceremony.selfPartyID,
		Status:    ceremony.status,
		Error:     ceremony.errorMessage,
		Signature: ceremony.signature,
	}, nil
}
