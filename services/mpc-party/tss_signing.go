package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"sync"

	"github.com/bnb-chain/tss-lib/v2/common"
	tsskeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	tsssigning "github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// Real, network-driven threshold-ECDSA signing -- the counterpart to
// tss_party.go's real DKG. Same relationship to
// services/mpc-signer/tss/tss.go's already-verified Sign() as StartKeygen
// has to its Keygen(): same library, same protocol, same message-driven
// state machine, but relayed over real HTTP between independent processes
// instead of in-process Go channels.
//
// A signing ceremony always references a completed keygen ceremony by ID
// and reuses that ceremony's tsscommon.SortedPartyIDs verbatim (not
// re-derived from a subset) -- each PartyID's .Index there is baked into
// the LocalPartySaveData produced at DKG time (Ks, BigXj, etc. are keyed
// by original position among ALL n DKG parties), so a signing committee
// smaller than n must select a SUBSET of those exact objects, in their
// original relative order, never a freshly re-sorted list of just the
// committee -- re-sorting would silently reassign indices and produce a
// cryptographically invalid (or, worse, silently wrong) result.

// tssSigningCeremony holds one in-flight or completed signing ceremony.
type tssSigningCeremony struct {
	mu           sync.Mutex
	status       ceremonyStatus
	errorMessage string

	selfPartyID int
	committee   tsscommon.SortedPartyIDs // subset of the DKG parties, original relative order preserved
	peers       map[int]string           // partyId -> base URL, restricted to committee members
	localParty  tsscommon.Party
	signature   string // hex-encoded 65-byte [R||S||V], once completed
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
	if len(messageHash) != 32 {
		return fmt.Errorf("message hash must be 32 bytes, got %d", len(messageHash))
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
	saveData := *keygenCeremony.saveData
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

	// Subset fullSortedIDs preserving original order/Index -- see the
	// package doc comment above for why this must not be a fresh sort.
	var committee tsscommon.SortedPartyIDs
	peers := make(map[int]string, len(committeePartyIDs))
	for _, sortedID := range fullSortedIDs {
		id := int(sortedID.KeyInt().Int64())
		if wanted[id] {
			committee = append(committee, sortedID)
			peers[id] = fullPeers[id]
		}
	}
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

	go m.runSigning(signID, ceremony, committee, peers, self, threshold, saveData, messageHash)

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
	saveData tsskeygen.LocalPartySaveData,
	messageHash []byte,
) {
	peerCtx := tsscommon.NewPeerContext(committee)
	params := tsscommon.NewParameters(tsscommon.S256(), peerCtx, self, len(committee), threshold)
	msg := new(big.Int).SetBytes(messageHash)

	outCh := make(chan tsscommon.Message, len(committee)*len(committee))
	endCh := make(chan common.SignatureData, 1)
	errCh := make(chan *tsscommon.Error, 1)

	localParty := tsssigning.NewLocalParty(msg, params, saveData, outCh, endCh)

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
			r, s, recovery := sd.R, sd.S, sd.SignatureRecovery
			m.completeSigning(signID, ceremony, r, s, recovery)
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

func (m *TSSPartyManager) completeSigning(signID string, ceremony *tssSigningCeremony, r, s []byte, recovery []byte) {
	// Assemble the 65-byte Ethereum-compatible signature [R || S || V],
	// exactly as services/mpc-signer/tss/tss.go's Sign() does.
	sig := make([]byte, 65)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):64], s)
	if len(recovery) > 0 {
		sig[64] = recovery[0]
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
