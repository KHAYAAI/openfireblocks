package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"

	tsslib "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	eddsakeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// Real, network-driven threshold-ECDSA DKG. This drives an
// actual bnb-chain/tss-lib keygen.LocalParty exactly the way
// services/mpc-signer/tss/tss.go's already-verified Keygen does -- same
// library, same protocol, same message-driven state machine -- but
// relayed over real HTTP between independent processes instead of
// in-process Go channels, which is the actual production shape (each
// party is a separate process/host with no shared memory).
//
// Scope of this increment: DKG only. Threshold SIGNING over the same
// network-relay model is real, valuable, separate work not attempted
// here -- see docs/security/audit-checklist.md.

// ceremonyStatus mirrors the lifecycle a caller polls for.
type ceremonyStatus string

const (
	ceremonyPending    ceremonyStatus = "pending"
	ceremonyInProgress ceremonyStatus = "in_progress"
	ceremonyCompleted  ceremonyStatus = "completed"
	ceremonyFailed     ceremonyStatus = "failed"
)

// tssKeygenCeremony holds one in-flight or completed DKG ceremony's state.
type tssKeygenCeremony struct {
	mu           sync.Mutex
	status       ceremonyStatus
	errorMessage string

	selfPartyID int
	threshold   int                      // needed later by StartSigning to size the signing committee
	sortedIDs   tsscommon.SortedPartyIDs // identical on every process, see deterministicPartyIDs
	peers       map[int]string           // partyId -> base URL, for relaying outgoing messages
	localParty  tsscommon.Party
	// Which curve this ceremony ran on, and the share it produced. Both
	// are needed: the save-data types come from different packages and
	// share no interface, so the curve is what says which pointer is set.
	curve        Curve
	saveData     *KeyShare
	publicKeyHex string
	address      string
	sealed       bool // true if the key share was durably sealed in Vault, see vault_seal.go
}

// TSSPartyManager tracks every ceremony this process (one party) is or has
// been a member of, both keygen (tss_party.go) and signing (tss_signing.go).
type TSSPartyManager struct {
	partyID int
	client  *http.Client

	mu         sync.Mutex
	ceremonies map[string]*tssKeygenCeremony

	signMu   sync.Mutex
	signings map[string]*tssSigningCeremony

	// Filled in the background from process start so a ceremony does not
	// pay for safe-prime generation on its critical path -- see
	// preparams.go for why that mattered enough to be a correctness bug
	// and not just a latency one.
	preParams *preParamsPool
}

func NewTSSPartyManager(partyID int, client *http.Client) *TSSPartyManager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	pool := newPreParamsPool(preParamsPoolSize)
	pool.start()
	return &TSSPartyManager{
		partyID:    partyID,
		client:     client,
		ceremonies: make(map[string]*tssKeygenCeremony),
		signings:   make(map[string]*tssSigningCeremony),
		preParams:  pool,
	}
}

// preParamsPoolSize is how many pre-generated parameter sets to keep warm.
//
// One, not two. The filler regenerates as soon as a slot frees, so a pool
// of two means each party keeps two safe-prime searches running -- six
// across a three-party committee -- and safe-prime generation is entirely
// CPU-bound. Measured on a cluster where each party is capped at one CPU,
// that background load was enough to starve the *inline* generation a new
// ceremony depends on, and ceremonies failed outright. The deeper pool made
// the cold path slower, not faster.
//
// One slot still covers the case it was meant to: a ceremony arriving while
// the party is otherwise idle takes a ready-made set instead of paying for
// generation on the critical path.
const preParamsPoolSize = 1

// deterministicPartyIDs builds the identical tss.SortedPartyIDs every
// process in the ceremony must independently arrive at. peers is
// {partyId: baseURL} for every member, this party's own ID included --
// the same map (same keys) sent to every party by the ceremony initiator,
// so every process sorts the same input and gets the same result. Key is
// derived from the party ID alone (big.NewInt(partyId)), not randomly
// generated, specifically so it's reproducible without a discovery round.
func deterministicPartyIDs(peers map[int]string) tsscommon.SortedPartyIDs {
	unsorted := make(tsscommon.UnSortedPartyIDs, 0, len(peers))
	for id := range peers {
		unsorted = append(unsorted, tsscommon.NewPartyID(fmt.Sprintf("%d", id), fmt.Sprintf("party%d", id), partyKey(id)))
	}
	return tsscommon.SortPartyIDs(unsorted)
}

func partyKey(partyID int) *big.Int {
	return big.NewInt(int64(partyID))
}

// findPartyID looks up the *tss.PartyID matching a deterministic party ID
// number within an already-sorted list -- used both to resolve outgoing
// message targets and to resolve an incoming message's declared sender.
func findPartyID(sorted tsscommon.SortedPartyIDs, partyID int) *tsscommon.PartyID {
	target := partyKey(partyID)
	for _, id := range sorted {
		if id.KeyInt().Cmp(target) == 0 {
			return id
		}
	}
	return nil
}

// StartKeygen begins a DKG ceremony: constructs this party's LocalParty,
// starts it, and launches the background goroutines that relay its
// outgoing messages over HTTP and watch for completion/failure. Returns
// once the ceremony is registered and Start() has been called -- it does
// NOT block for completion; poll GetStatus for that.
func (m *TSSPartyManager) StartKeygen(ceremonyID string, threshold int, peers map[int]string, curve Curve) error {
	if _, ok := peers[m.partyID]; !ok {
		return fmt.Errorf("peers map does not include this party's own id %d", m.partyID)
	}

	// Validated before anything starts. A ceremony on a curve this build
	// does not know would otherwise run to completion and produce a share
	// nothing can sign with -- discovered by a customer whose key never
	// works, long after the DKG reported success.
	if _, err := curve.ellipticCurve(); err != nil {
		return fmt.Errorf("cannot start a ceremony: %w", err)
	}

	sorted := deterministicPartyIDs(peers)
	self := findPartyID(sorted, m.partyID)
	if self == nil {
		return fmt.Errorf("failed to resolve own party ID %d after sorting", m.partyID)
	}

	// Registered as "in progress" with no localParty yet, before the slow
	// part (prime generation for preParams, seconds to low minutes) even
	// starts. Other parties may finish their own preParams first and start
	// sending protocol messages before this party's localParty exists --
	// HandleIncomingMessage below reports that as a distinct "not ready"
	// error, and postTSSMessage's sender-side retry (tss_handlers.go)
	// tolerates it rather than aborting the ceremony over a startup race.
	ceremony := &tssKeygenCeremony{
		status:      ceremonyInProgress,
		selfPartyID: m.partyID,
		threshold:   threshold,
		sortedIDs:   sorted,
		peers:       peers,
		curve:       curve,
	}
	m.mu.Lock()
	m.ceremonies[ceremonyID] = ceremony
	m.mu.Unlock()

	go m.runKeygen(ceremonyID, ceremony, sorted, self, threshold)

	return nil
}

// runKeygen does the actual (slow) party construction and drives the
// protocol to completion. Split out of StartKeygen so the HTTP handler
// calling StartKeygen returns immediately rather than blocking on prime
// generation.
func (m *TSSPartyManager) runKeygen(ceremonyID string, ceremony *tssKeygenCeremony, sorted tsscommon.SortedPartyIDs, self *tsscommon.PartyID, threshold int) {
	ec, err := ceremony.curve.ellipticCurve()
	if err != nil {
		m.failCeremony(ceremonyID, err)
		return
	}

	peerCtx := tsscommon.NewPeerContext(sorted)
	params := tsscommon.NewParameters(ec, peerCtx, self, len(sorted), threshold)
	outCh := make(chan tsscommon.Message, len(sorted)*len(sorted))
	errCh := make(chan *tsscommon.Error, 1)

	// The two curves differ here and nowhere else in the ceremony. ECDSA
	// needs Paillier pre-parameters, whose safe-prime generation is the
	// slow part of a DKG; EdDSA needs none, so an Ed25519 ceremony starts
	// immediately and finishes in a fraction of the time.
	switch ceremony.curve {
	case CurveSecp256k1:
		preParams, err := m.preParams.get()
		if err != nil {
			m.failCeremony(ceremonyID, fmt.Errorf("failed to generate pre-params: %w", err))
			return
		}

		endCh := make(chan tsslib.LocalPartySaveData, 1)
		localParty := tsslib.NewLocalParty(params, outCh, endCh, *preParams)
		if !m.startLocalParty(ceremonyID, ceremony, localParty) {
			return
		}
		driveKeygen(m, ceremonyID, ceremony, outCh, endCh, errCh,
			func(save tsslib.LocalPartySaveData) *KeyShare {
				return &KeyShare{Curve: CurveSecp256k1, ECDSA: &save}
			})

	case CurveEd25519:
		endCh := make(chan eddsakeygen.LocalPartySaveData, 1)
		localParty := eddsakeygen.NewLocalParty(params, outCh, endCh)
		if !m.startLocalParty(ceremonyID, ceremony, localParty) {
			return
		}
		driveKeygen(m, ceremonyID, ceremony, outCh, endCh, errCh,
			func(save eddsakeygen.LocalPartySaveData) *KeyShare {
				return &KeyShare{Curve: CurveEd25519, EdDSA: &save}
			})

	default:
		m.failCeremony(ceremonyID, fmt.Errorf("unknown curve %q", ceremony.curve))
	}
}

// startLocalParty records the party and starts it, reporting whether the
// caller should continue. Shared so the curve branches above differ only in
// the types they name.
func (m *TSSPartyManager) startLocalParty(ceremonyID string, ceremony *tssKeygenCeremony, localParty tsscommon.Party) bool {
	ceremony.mu.Lock()
	ceremony.localParty = localParty
	ceremony.mu.Unlock()

	if err := localParty.Start(); err != nil {
		m.failCeremony(ceremonyID, fmt.Errorf("failed to start local party: %w", err))
		return false
	}
	return true
}

// driveKeygen relays outgoing protocol messages over HTTP and watches for
// the ceremony to finish (successfully or not). Mirrors
// mpc-signer/tss/tss.go's Keygen event loop, transport swapped from
// in-process channels to real HTTP POSTs.
// driveKeygen is generic over the save-data type because that is the only
// thing that differs between the curves -- the message relay, the error
// handling and the completion path are identical, and duplicating this loop
// per curve would mean fixing every future transport bug twice.
func driveKeygen[S any](
	m *TSSPartyManager,
	ceremonyID string,
	ceremony *tssKeygenCeremony,
	outCh chan tsscommon.Message,
	endCh chan S,
	errCh chan *tsscommon.Error,
	wrap func(S) *KeyShare,
) {
	for {
		select {
		case msg := <-outCh:
			if err := m.relayMessage(ceremonyID, ceremony, msg); err != nil {
				m.failCeremony(ceremonyID, fmt.Errorf("failed to relay message: %w", err))
				return
			}
		case err := <-errCh:
			m.failCeremony(ceremonyID, fmt.Errorf("tss-lib protocol error: %w", err))
			return
		case save := <-endCh:
			m.completeCeremony(ceremonyID, ceremony, wrap(save))
			return
		}
	}
}

func (m *TSSPartyManager) relayMessage(ceremonyID string, ceremony *tssKeygenCeremony, msg tsscommon.Message) error {
	data, _, err := msg.WireBytes()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	envelope := tssMessageEnvelope{
		CeremonyID:  ceremonyID,
		FromPartyID: ceremony.selfPartyID,
		IsBroadcast: msg.IsBroadcast(),
		WireBytes:   base64.StdEncoding.EncodeToString(data),
	}

	return relayTSSMessage(ceremony.selfPartyID, ceremony.peers, ceremony.sortedIDs, msg, func(baseURL string) error {
		return postTSSMessage(m.client, baseURL, envelope)
	})
}

// relayTSSMessage resolves which peers a tss-lib protocol message needs to
// reach (everyone else in peers, if broadcast; msg.GetTo() mapped back to
// party-ID numbers via sortedIDs, if not) and calls postFn once per
// target. Shared between keygen (relayMessage above) and signing
// (relaySignMessage, tss_signing.go) -- the routing logic is identical;
// only the wire envelope shape and HTTP endpoint differ, which postFn owns.
func relayTSSMessage(selfPartyID int, peers map[int]string, sortedIDs tsscommon.SortedPartyIDs, msg tsscommon.Message, postFn func(baseURL string) error) error {
	var targets []int
	if msg.IsBroadcast() {
		for id := range peers {
			if id != selfPartyID {
				targets = append(targets, id)
			}
		}
	} else {
		for _, to := range msg.GetTo() {
			for id, sortedID := range peerIndexByKey(sortedIDs) {
				if sortedID.KeyInt().Cmp(to.KeyInt()) == 0 {
					targets = append(targets, id)
				}
			}
		}
	}

	for _, targetID := range targets {
		baseURL, ok := peers[targetID]
		if !ok {
			return fmt.Errorf("no known endpoint for target party %d", targetID)
		}
		if err := postFn(baseURL); err != nil {
			return fmt.Errorf("failed to deliver message to party %d: %w", targetID, err)
		}
	}
	return nil
}

// peerIndexByKey maps each sorted party ID back to its deterministic
// party-ID number, the inverse of deterministicPartyIDs/partyKey.
func peerIndexByKey(sorted tsscommon.SortedPartyIDs) map[int]*tsscommon.PartyID {
	out := make(map[int]*tsscommon.PartyID, len(sorted))
	for _, id := range sorted {
		out[int(id.KeyInt().Int64())] = id
	}
	return out
}

func (m *TSSPartyManager) failCeremony(ceremonyID string, err error) {
	m.mu.Lock()
	ceremony, ok := m.ceremonies[ceremonyID]
	m.mu.Unlock()
	if !ok {
		log.Printf("ceremony %s failed but was never registered: %v", ceremonyID, err)
		return
	}
	ceremony.mu.Lock()
	ceremony.status = ceremonyFailed
	ceremony.errorMessage = err.Error()
	ceremony.mu.Unlock()
	log.Printf("keygen ceremony %s failed: %v", ceremonyID, err)
}

func (m *TSSPartyManager) completeCeremony(ceremonyID string, ceremony *tssKeygenCeremony, share *KeyShare) {
	// Derived through the share rather than here, because the two curves
	// produce addresses in entirely different ways -- an Ethereum address
	// is the last 20 bytes of a hash of the public key, and a Solana
	// address is the public key itself, base58 encoded.
	pubKeyHex, address, err := share.PublicKey()
	if err != nil {
		m.failCeremony(ceremonyID, fmt.Errorf("keygen succeeded but the public key could not be derived: %w", err))
		return
	}

	// Seal this party's own share before reporting success -- a key share
	// that exists only in this process's RAM is not durably generated. If
	// Vault isn't configured (VAULT_ADDR unset) this is a documented no-op
	// (see vault_seal.go); if it IS configured, a sealing failure fails
	// the whole ceremony rather than silently reporting "completed" with
	// nothing durable to show for it.
	sealed, err := SealKeyShare(context.Background(), os.Getenv, ceremony.selfPartyID, ceremonyID, share)
	if err != nil {
		m.failCeremony(ceremonyID, fmt.Errorf("keygen succeeded but sealing the key share in Vault failed: %w", err))
		return
	}

	ceremony.mu.Lock()
	ceremony.status = ceremonyCompleted
	ceremony.saveData = share
	ceremony.publicKeyHex = pubKeyHex
	ceremony.address = address
	ceremony.sealed = sealed
	ceremony.mu.Unlock()

	if sealed {
		log.Printf("keygen ceremony %s completed on %s: party %d derived shared address %s (key share sealed in Vault)", ceremonyID, ceremony.curve, ceremony.selfPartyID, address)
	} else {
		log.Printf("keygen ceremony %s completed on %s: party %d derived shared address %s (VAULT_ADDR not set -- key share held in memory only)", ceremonyID, ceremony.curve, ceremony.selfPartyID, address)
	}
}

// ErrCeremonyNotReady means the ceremony is registered but this party's
// localParty hasn't been constructed yet (still generating preParams) --
// a real, expected race when a faster peer starts sending protocol
// messages before this process is ready to receive them, not a failure.
// The HTTP handler reports it with a distinct status so the sender can
// retry instead of aborting the whole ceremony over a startup race.
var ErrCeremonyNotReady = fmt.Errorf("ceremony registered but not yet ready to receive messages")

// HandleIncomingMessage feeds a relayed protocol message into the local
// party's state machine. Called by the HTTP handler for /tss/keygen/message.
func (m *TSSPartyManager) HandleIncomingMessage(env tssMessageEnvelope) error {
	m.mu.Lock()
	ceremony, ok := m.ceremonies[env.CeremonyID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown ceremony %s", env.CeremonyID)
	}

	ceremony.mu.Lock()
	localParty := ceremony.localParty
	ceremony.mu.Unlock()
	if localParty == nil {
		return ErrCeremonyNotReady
	}

	from := findPartyID(ceremony.sortedIDs, env.FromPartyID)
	if from == nil {
		return fmt.Errorf("unknown sender party %d for ceremony %s", env.FromPartyID, env.CeremonyID)
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

// KeygenStatusResult is what GET /tss/keygen/{ceremonyId}/status returns.
// Deliberately excludes raw key-share material -- the shared public
// key/address prove the ceremony produced a real result without exposing
// anything a caller shouldn't see over HTTP. Sealed reports whether the
// share was durably persisted to Vault (see vault_seal.go) or only ever
// existed in this process's memory.
type KeygenStatusResult struct {
	CeremonyID string         `json:"ceremony_id"`
	PartyID    int            `json:"party_id"`
	Status     ceremonyStatus `json:"status"`
	Error      string         `json:"error,omitempty"`
	PublicKey  string         `json:"public_key,omitempty"`
	Address    string         `json:"address,omitempty"`
	Sealed     bool           `json:"sealed"`
}

// CeremonyCounts reports how many keygen and signing ceremonies this party
// is tracking. Used by /info for operator visibility; it deliberately
// exposes counts rather than ceremony ids or any state derived from key
// material.
func (m *TSSPartyManager) CeremonyCounts() (keygens, signings int) {
	m.mu.Lock()
	keygens = len(m.ceremonies)
	m.mu.Unlock()
	m.signMu.Lock()
	signings = len(m.signings)
	m.signMu.Unlock()
	return keygens, signings
}

func (m *TSSPartyManager) GetStatus(ceremonyID string) (*KeygenStatusResult, error) {
	m.mu.Lock()
	ceremony, ok := m.ceremonies[ceremonyID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown ceremony %s", ceremonyID)
	}

	ceremony.mu.Lock()
	defer ceremony.mu.Unlock()
	return &KeygenStatusResult{
		CeremonyID: ceremonyID,
		PartyID:    ceremony.selfPartyID,
		Status:     ceremony.status,
		Error:      ceremony.errorMessage,
		Sealed:     ceremony.sealed,
		PublicKey:  ceremony.publicKeyHex,
		Address:    ceremony.address,
	}, nil
}
