package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"

	ecdsakeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	ecdsaresharing "github.com/bnb-chain/tss-lib/v2/ecdsa/resharing"
	eddsakeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	eddsaresharing "github.com/bnb-chain/tss-lib/v2/eddsa/resharing"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// Proactive key refresh: re-randomising the shares of a key without
// changing the key.
//
// The gap this closes is the largest one an auditor finds in a threshold
// system that does not have it. Without refresh, share compromise is
// *cumulative*. An attacker who obtains one share in January and a second
// in June holds two shares, and at 2-of-3 two shares is the private key --
// even though at no single moment did they hold a threshold, and even
// though each individual breach was contained and remediated.
//
// Resharing defeats that adversary. After a refresh, shares from before
// and after cannot be combined: they are shares of the same secret under a
// different random polynomial, and mixing epochs yields nothing. An
// attacker must now compromise t parties *within one refresh interval*,
// which is a categorically harder problem and the one the threshold was
// supposed to pose in the first place.
//
// This is the property MPC-CMP names as proactive security, and it is why
// "we use 2-of-3 threshold signing" is a weaker claim without it than
// most people assume.
//
// tss-lib has shipped ecdsa/resharing and eddsa/resharing since v1. The
// protocol was never the missing piece -- the ceremony orchestration was,
// and that already existed for DKG. This file is largely the keygen
// ceremony with a different local party and one additional obligation:
// proving the key did not change.

// tssResharingCeremony holds one in-flight or completed refresh.
//
// Deliberately a separate type from tssKeygenCeremony rather than a status
// on it. A refresh operates on a key that already exists, already has an
// address, and may already hold money -- conflating the two would make it
// possible to write a refresh result into the slot a fresh ceremony reads.
type tssResharingCeremony struct {
	mu           sync.Mutex
	status       ceremonyStatus
	errorMessage string

	selfPartyID int
	threshold   int
	curve       Curve
	peers       map[int]string

	// Two committees, two local parties, one physical node.
	//
	// tss-lib's resharing requires the old and new committees to have
	// distinct party identities: round 3 marks "all old parties OK" for
	// any member of the old committee, so a party that is a member of
	// both short-circuits its own message accounting and reaches round 4
	// with messages it never received. The library's own tests generate a
	// fresh set of ids for the new committee for exactly this reason.
	//
	// So each node plays both roles explicitly -- contributing its old
	// share as an old-committee member, and receiving a new one as a new-
	// committee member -- and messages are routed to the role they are
	// addressed to.
	oldSorted tsscommon.SortedPartyIDs
	newSorted tsscommon.SortedPartyIDs
	newEpoch  int
	oldParty  tsscommon.Party
	newParty  tsscommon.Party

	// The ceremony whose share is being refreshed, and what that share's
	// public key was before. The latter is the invariant: a refresh that
	// changes the public key has not refreshed anything, it has generated
	// a different key, and the customer's funds are at the old address.
	sourceCeremonyID  string
	expectedPublicKey string
	expectedAddress   string

	saveData *KeyShare
	sealed   bool
}

// StartResharing refreshes the shares of an existing key.
//
// Same committee, same threshold: this is a refresh, not a committee
// change. Adding or removing parties is a different operation with
// different failure modes (a removed party still holds its old share until
// it destroys it) and it deserves its own route rather than a flag on this
// one.
func (m *TSSPartyManager) StartResharing(reshareID, sourceCeremonyID string, peers map[int]string) error {
	m.mu.Lock()
	source, ok := m.ceremonies[sourceCeremonyID]
	if _, exists := m.resharings[reshareID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("resharing %s already exists", reshareID)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("unknown ceremony %s: nothing to refresh", sourceCeremonyID)
	}

	source.mu.Lock()
	status := source.status
	share := source.saveData
	threshold := source.threshold
	curve := source.curve
	publicKey := source.publicKeyHex
	address := source.address
	epoch := source.epoch
	source.mu.Unlock()

	// Refusing anything but a completed ceremony. Refreshing a ceremony
	// that is still running would race the DKG for the same slot, and
	// refreshing a failed one has no share to refresh.
	if status != ceremonyCompleted || share == nil {
		return fmt.Errorf("ceremony %s is %s; only a completed ceremony can be refreshed",
			sourceCeremonyID, status)
	}
	if len(peers) == 0 {
		return fmt.Errorf("no peers supplied for resharing %s", reshareID)
	}

	oldSorted := committeePartyIDs(peers, epoch)
	newSorted := committeePartyIDs(peers, epoch+1)
	if findPartyIDByKey(oldSorted, committeeKey(m.partyID, epoch)) == nil {
		return fmt.Errorf("this party (%d) is not a member of resharing %s", m.partyID, reshareID)
	}

	ceremony := &tssResharingCeremony{
		status:            ceremonyInProgress,
		selfPartyID:       m.partyID,
		threshold:         threshold,
		curve:             curve,
		oldSorted:         oldSorted,
		newSorted:         newSorted,
		newEpoch:          epoch + 1,
		peers:             peers,
		sourceCeremonyID:  sourceCeremonyID,
		expectedPublicKey: publicKey,
		expectedAddress:   address,
	}

	m.mu.Lock()
	m.resharings[reshareID] = ceremony
	m.mu.Unlock()

	go m.runResharing(reshareID, ceremony, share)
	return nil
}

func (m *TSSPartyManager) runResharing(
	reshareID string,
	ceremony *tssResharingCeremony,
	share *KeyShare,
) {
	ec, err := ceremony.curve.ellipticCurve()
	if err != nil {
		m.failResharing(reshareID, err)
		return
	}

	oldSorted := ceremony.oldSorted
	newSorted := ceremony.newSorted
	oldSelf := findPartyID(oldSorted, ceremony.selfPartyID)
	newSelf := findPartyIDByKey(newSorted, committeeKey(ceremony.selfPartyID, ceremony.newEpoch))
	if oldSelf == nil || newSelf == nil {
		m.failResharing(reshareID, fmt.Errorf("this party is not in both committees of resharing %s", reshareID))
		return
	}

	oldCtx := tsscommon.NewPeerContext(oldSorted)
	newCtx := tsscommon.NewPeerContext(newSorted)
	n := len(oldSorted)
	t := ceremony.threshold

	oldParams := tsscommon.NewReSharingParameters(ec, oldCtx, newCtx, oldSelf, n, t, n, t)
	newParams := tsscommon.NewReSharingParameters(ec, oldCtx, newCtx, newSelf, n, t, n, t)

	// One outbound channel for both roles. Which role a message came from
	// does not matter; where it is addressed does, and that is carried on
	// the message itself.
	outCh := make(chan tsscommon.Message, n*n*4)
	errCh := make(chan *tsscommon.Error, 2)

	switch ceremony.curve {
	case CurveSecp256k1:
		if share.ECDSA == nil {
			m.failResharing(reshareID, fmt.Errorf("secp256k1 ceremony carries no ECDSA share"))
			return
		}
		// The old role contributes the existing share. A copy, so a failed
		// refresh leaves the share this party already holds untouched.
		oldInput := *share.ECDSA

		// The new role starts from empty save data sized for the new
		// committee, plus Paillier pre-parameters of its own. Fresh ones,
		// not the existing key's: reusing them would rotate the secret
		// shares and leave the Paillier keypairs exactly where they were,
		// which is a partial refresh presented as a whole one.
		newInput := ecdsakeygen.NewLocalPartySaveData(n)
		if preParams, err := m.preParams.get(); err == nil {
			newInput.LocalPreParams = *preParams
		} else {
			// Falling back to the existing parameters rather than failing.
			// A refresh that rotates the shares and not the Paillier keys
			// is materially better than no refresh, and the most valuable
			// operation here should not also be the most fragile.
			newInput.LocalPreParams = share.ECDSA.LocalPreParams
			m.logf("resharing %s: reusing existing Paillier parameters: %v", reshareID, err)
		}

		oldEnd := make(chan ecdsakeygen.LocalPartySaveData, 1)
		newEnd := make(chan ecdsakeygen.LocalPartySaveData, 1)
		oldParty := ecdsaresharing.NewLocalParty(oldParams, oldInput, outCh, oldEnd)
		newParty := ecdsaresharing.NewLocalParty(newParams, newInput, outCh, newEnd)
		if !m.startResharingParties(reshareID, ceremony, oldParty, newParty) {
			return
		}
		driveResharing(m, reshareID, ceremony, outCh, newEnd, oldEnd, errCh,
			func(save ecdsakeygen.LocalPartySaveData) *KeyShare {
				return &KeyShare{Curve: CurveSecp256k1, ECDSA: &save}
			})

	case CurveEd25519:
		if share.EdDSA == nil {
			m.failResharing(reshareID, fmt.Errorf("ed25519 ceremony carries no EdDSA share"))
			return
		}
		oldInput := *share.EdDSA
		newInput := eddsakeygen.NewLocalPartySaveData(n)

		oldEnd := make(chan eddsakeygen.LocalPartySaveData, 1)
		newEnd := make(chan eddsakeygen.LocalPartySaveData, 1)
		oldParty := eddsaresharing.NewLocalParty(oldParams, oldInput, outCh, oldEnd)
		newParty := eddsaresharing.NewLocalParty(newParams, newInput, outCh, newEnd)
		if !m.startResharingParties(reshareID, ceremony, oldParty, newParty) {
			return
		}
		driveResharing(m, reshareID, ceremony, outCh, newEnd, oldEnd, errCh,
			func(save eddsakeygen.LocalPartySaveData) *KeyShare {
				return &KeyShare{Curve: CurveEd25519, EdDSA: &save}
			})

	default:
		m.failResharing(reshareID, fmt.Errorf("unknown curve %q", ceremony.curve))
	}
}

func (m *TSSPartyManager) startResharingParties(
	reshareID string,
	ceremony *tssResharingCeremony,
	oldParty, newParty tsscommon.Party,
) bool {
	ceremony.mu.Lock()
	ceremony.oldParty = oldParty
	ceremony.newParty = newParty
	ceremony.mu.Unlock()

	// The new committee first. It only receives, so starting it second
	// would leave a window where an old-committee message arrives for a
	// party that does not exist yet and is retried for no reason.
	for name, party := range map[string]tsscommon.Party{"new": newParty, "old": oldParty} {
		if err := party.Start(); err != nil {
			m.failResharing(reshareID, fmt.Errorf("failed to start the %s-committee party: %w", name, err))
			return false
		}
	}
	return true
}

// driveResharing relays outgoing messages and waits for the new-committee
// party to produce a share.
//
// oldEnd is drained and discarded on purpose. The old-committee role also
// signals completion, and its "save data" is the key it already had --
// reading it as the refresh result would record the pre-refresh share and
// report success.
func driveResharing[S any](
	m *TSSPartyManager,
	reshareID string,
	ceremony *tssResharingCeremony,
	outCh chan tsscommon.Message,
	newEnd chan S,
	oldEnd chan S,
	errCh chan *tsscommon.Error,
	wrap func(S) *KeyShare,
) {
	for {
		select {
		case msg := <-outCh:
			if err := m.relayReshareMessage(reshareID, ceremony, msg); err != nil {
				m.failResharing(reshareID, fmt.Errorf("failed to relay message: %w", err))
				return
			}
		case err := <-errCh:
			m.failResharing(reshareID, fmt.Errorf("tss-lib protocol error: %w", err))
			return
		case <-oldEnd:
			// The old role finished. Not the answer; keep going.
		case save := <-newEnd:
			m.completeResharing(reshareID, ceremony, wrap(save))
			return
		}
	}
}

func (m *TSSPartyManager) relayReshareMessage(
	reshareID string,
	ceremony *tssResharingCeremony,
	msg tsscommon.Message,
) error {
	data, _, err := msg.WireBytes()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	// Resharing has no broadcasts. Every message carries an explicit
	// destination list, and which committee it is for decides which of the
	// two local parties on the receiving node should see it -- delivering
	// an old-committee message to a new-committee party is not a routing
	// inefficiency, it is a message the recipient will never account for.
	targets := msg.GetTo()
	if len(targets) == 0 {
		return fmt.Errorf("a resharing message carried no destination")
	}

	// Which of this node's two parties produced the message. Read off the
	// message rather than tracked alongside it: both roles write to one
	// outbound channel, and the sender identity on the message is the only
	// thing that distinguishes them.
	_, fromEpoch := nodeForPartyKey(msg.GetFrom())
	fromIsNew := fromEpoch == ceremony.newEpoch

	deliver := func(node int, toOld bool) error {
		envelope := tssReshareMessageEnvelope{
			ReshareID:        reshareID,
			FromPartyID:      ceremony.selfPartyID,
			ToOldCommittee:   toOld,
			FromOldCommittee: !fromIsNew,
			IsBroadcast:      msg.IsBroadcast(),
			WireBytes:        base64.StdEncoding.EncodeToString(data),
		}
		if node == ceremony.selfPartyID {
			// Delivered in-process rather than posted to ourselves, and in
			// a goroutine because this runs on the loop draining outCh:
			// feeding a message back synchronously can produce further
			// messages and fill the channel we are meant to be draining.
			go func() {
				if err := m.HandleIncomingReshareMessage(envelope); err != nil {
					m.logf("resharing %s: delivering to self failed: %v", reshareID, err)
				}
			}()
			return nil
		}
		baseURL, ok := ceremony.peers[node]
		if !ok {
			return fmt.Errorf("no known endpoint for party %d", node)
		}
		if err := postReshareEnvelope(m.client, baseURL, envelope); err != nil {
			return fmt.Errorf("failed to deliver message to party %d: %w", node, err)
		}
		return nil
	}

	toOld := msg.IsToOldCommittee() || msg.IsToOldAndNewCommittees()
	toNew := !msg.IsToOldCommittee() || msg.IsToOldAndNewCommittees()

	for _, to := range targets {
		node, toEpoch := nodeForPartyKey(to)
		isNewID := toEpoch == ceremony.newEpoch
		if node < 0 {
			return fmt.Errorf("resharing message addressed to an unrecognised party")
		}
		// A destination drawn from the new-committee id space is for the
		// new role whatever the flags say, and vice versa. The flags
		// disambiguate only the messages addressed to both.
		switch {
		case isNewID && toNew:
			if err := deliver(node, false); err != nil {
				return err
			}
		case !isNewID && toOld:
			if err := deliver(node, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// completeResharing records the new share, having first proved it is a
// share of the same key.
//
// This check is the reason a refresh is safe to run against a funded key.
// A resharing that produced a different public key would not be a refresh
// at all: the customer's money would sit at the old address, controlled by
// shares this party is about to discard. Refusing to record the result
// leaves the old share in place and the key exactly as it was.
func (m *TSSPartyManager) completeResharing(
	reshareID string,
	ceremony *tssResharingCeremony,
	share *KeyShare,
) {
	publicKey, address, err := share.PublicKey()
	if err != nil {
		m.failResharing(reshareID, fmt.Errorf("refreshed share has no usable public key: %w", err))
		return
	}

	if publicKey != ceremony.expectedPublicKey || address != ceremony.expectedAddress {
		m.failResharing(reshareID, fmt.Errorf(
			"refusing the refreshed share: it controls %s, but the key being refreshed is %s. "+
				"A refresh must not change the key; the existing share has been left untouched",
			address, ceremony.expectedAddress))
		return
	}

	ceremony.mu.Lock()
	ceremony.saveData = share
	ceremony.status = ceremonyCompleted
	ceremony.mu.Unlock()

	// Sealed under the source ceremony's id, replacing the share it is a
	// refresh of. The key's identity does not change, so neither does
	// where its share lives -- a refresh that wrote to a new location
	// would leave the old share readable, which is most of the point of
	// refreshing undone.
	//
	// Sealed *with* its context, and the epoch is the reason this cannot
	// use the bare SealKeyShare. Overwriting the entry with a share alone
	// would strip the context the DKG sealed, and the key would become
	// unrecoverable at the moment it was made more secure -- a refresh
	// that quietly destroys the backup is worse than no refresh at all.
	//
	// The epoch written here is the new one. Restoring a refreshed share
	// with the old epoch rebuilds the committee at the x-coordinates the
	// DKG used, and the shares no longer sit on that polynomial: the
	// restore would succeed and the signing would produce nothing that
	// verifies. See section 4 of docs/deployment/KEY-RECOVERY.md.
	sealed, err := SealKeyShareWithContext(context.Background(), os.Getenv,
		ceremony.selfPartyID, ceremony.sourceCeremonyID, share, &CeremonyContext{
			Threshold:    ceremony.threshold,
			TotalParties: len(ceremony.newSorted),
			Curve:        string(ceremony.curve),
			Epoch:        ceremony.newEpoch,
			PublicKeyHex: publicKey,
			Address:      address,
		})
	if err != nil {
		// Logged, not fatal, and the distinction matters. The refreshed
		// share is valid and has already replaced the old one in memory;
		// a sealing failure means it is not durable, which an operator
		// must fix -- but failing the ceremony here would leave this
		// party holding a share it has marked failed.
		m.logf("resharing %s: refreshed share could not be sealed: %v", reshareID, err)
	}
	ceremony.mu.Lock()
	ceremony.sealed = sealed
	ceremony.mu.Unlock()

	// The in-memory share for the source ceremony is replaced too, so the
	// next signing ceremony uses the refreshed share rather than the one
	// this refresh was meant to retire.
	m.mu.Lock()
	source, ok := m.ceremonies[ceremony.sourceCeremonyID]
	m.mu.Unlock()
	if ok {
		source.mu.Lock()
		source.saveData = share
		// The key's committee identity moves with its shares. A refresh
		// re-evaluates the secret at new x-coordinates, so signing after
		// this must interpolate at those and not the ones the DKG used.
		source.sortedIDs = ceremony.newSorted
		source.epoch = ceremony.newEpoch
		source.mu.Unlock()
	}

	m.logf("resharing %s: refreshed the shares of %s, key unchanged at %s",
		reshareID, ceremony.sourceCeremonyID, address)
}

func (m *TSSPartyManager) failResharing(reshareID string, err error) {
	m.mu.Lock()
	ceremony, ok := m.resharings[reshareID]
	m.mu.Unlock()
	if !ok {
		return
	}
	ceremony.mu.Lock()
	ceremony.status = ceremonyFailed
	ceremony.errorMessage = err.Error()
	ceremony.mu.Unlock()
	m.logf("resharing %s failed: %v", reshareID, err)
}

// HandleIncomingReshareMessage feeds a relayed refresh message into this
// party's state machine.
func (m *TSSPartyManager) HandleIncomingReshareMessage(env tssReshareMessageEnvelope) error {
	m.mu.Lock()
	ceremony, ok := m.resharings[env.ReshareID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown resharing %s", env.ReshareID)
	}

	ceremony.mu.Lock()
	target := ceremony.newParty
	if env.ToOldCommittee {
		target = ceremony.oldParty
	}
	ceremony.mu.Unlock()
	if target == nil {
		return ErrResharingNotReady
	}

	// The sender's identity is drawn from the committee it sent as, not
	// the one it is addressing. An old-committee message is from an
	// old-committee id even when its destination is a new-committee party.
	var from *tsscommon.PartyID
	if env.FromOldCommittee {
		from = findPartyIDByKey(ceremony.oldSorted, committeeKey(env.FromPartyID, ceremony.newEpoch-1))
	} else {
		from = findPartyIDByKey(ceremony.newSorted, committeeKey(env.FromPartyID, ceremony.newEpoch))
	}
	if from == nil {
		return fmt.Errorf("unknown sender party %d for resharing %s", env.FromPartyID, env.ReshareID)
	}

	wireBytes, err := base64.StdEncoding.DecodeString(env.WireBytes)
	if err != nil {
		return fmt.Errorf("failed to decode wire bytes: %w", err)
	}
	if _, err := target.UpdateFromBytes(wireBytes, from, env.IsBroadcast); err != nil {
		return fmt.Errorf("failed to update resharing state: %w", err)
	}
	return nil
}

// ErrResharingNotReady mirrors ErrCeremonyNotReady: the refresh is
// registered but its local party has not been constructed yet, so the
// sender should retry rather than abandon the ceremony.
var ErrResharingNotReady = fmt.Errorf("resharing registered but not yet ready to receive messages")

// ResharingStatusResult is what the status endpoint returns.
//
// Carries the address deliberately, so an operator can see at a glance
// that it is unchanged. A refresh that reported success without saying
// which key it refreshed would be hard to verify and easy to trust.
type ResharingStatusResult struct {
	ReshareID        string `json:"reshare_id"`
	SourceCeremonyID string `json:"source_ceremony_id"`
	Status           string `json:"status"`
	Curve            string `json:"curve"`
	Address          string `json:"address,omitempty"`
	KeyUnchanged     bool   `json:"key_unchanged"`
	Sealed           bool   `json:"sealed"`
	Error            string `json:"error,omitempty"`
}

func (m *TSSPartyManager) GetResharingStatus(reshareID string) (*ResharingStatusResult, error) {
	m.mu.Lock()
	ceremony, ok := m.resharings[reshareID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown resharing %s", reshareID)
	}

	ceremony.mu.Lock()
	defer ceremony.mu.Unlock()

	result := &ResharingStatusResult{
		ReshareID:        reshareID,
		SourceCeremonyID: ceremony.sourceCeremonyID,
		Status:           string(ceremony.status),
		Curve:            string(ceremony.curve),
		Sealed:           ceremony.sealed,
		Error:            ceremony.errorMessage,
	}
	if ceremony.status == ceremonyCompleted {
		result.Address = ceremony.expectedAddress
		result.KeyUnchanged = true
	}
	return result, nil
}

// logf is the manager's log line. Resharing is the one operation here that
// touches a key already holding money, so what it did needs to be legible
// in a log an operator reads after the fact.
func (m *TSSPartyManager) logf(format string, args ...interface{}) {
	log.Printf("[party %d] "+format, append([]interface{}{m.partyID}, args...)...)
}

// newCommitteeKeyOffset separates the two committees' identity spaces.
//
// tss-lib requires them distinct -- a party in both short-circuits round
// 3's message accounting and dereferences a nil message in round 4 -- so
// each node holds one id as an old-committee member and another as a new
// one. The offset is large enough that no plausible party count reaches
// it, and fixed so that every node derives the same ids without a
// discovery round, exactly as deterministicPartyIDs does.
const committeeEpochStride = 1_000_000

// committeeKey is a node's tss-lib identity in a given refresh epoch.
//
// Epoch 0 is the original DKG, whose keys are just the party numbers --
// so keys generated before refresh existed keep the identities they were
// generated with, and nothing has to be migrated.
//
// Each refresh moves the committee into the next epoch. That is not
// bookkeeping: resharing re-evaluates the secret at *new* x-coordinates,
// so the shares a refresh produces genuinely belong to different party
// identities, and signing afterwards has to interpolate at those.
func committeeKey(node, epoch int) *big.Int {
	return big.NewInt(int64(epoch)*committeeEpochStride + int64(node))
}

// committeePartyIDs is deterministicPartyIDs for an arbitrary epoch.
func committeePartyIDs(peers map[int]string, epoch int) tsscommon.SortedPartyIDs {
	if epoch == 0 {
		return deterministicPartyIDs(peers)
	}
	unsorted := make(tsscommon.UnSortedPartyIDs, 0, len(peers))
	for id := range peers {
		unsorted = append(unsorted, tsscommon.NewPartyID(
			fmt.Sprintf("e%d-%d", epoch, id), fmt.Sprintf("party%d.e%d", id, epoch),
			committeeKey(id, epoch)))
	}
	return tsscommon.SortPartyIDs(unsorted)
}

// findPartyIDByKey is findPartyID for an arbitrary key rather than a party
// number.
func findPartyIDByKey(sorted tsscommon.SortedPartyIDs, key *big.Int) *tsscommon.PartyID {
	for _, id := range sorted {
		if id.KeyInt().Cmp(key) == 0 {
			return id
		}
	}
	return nil
}

// nodeForPartyKey maps a party id from either committee back to the node
// that holds it, and says which committee it came from.
func nodeForPartyKey(id *tsscommon.PartyID) (node, epoch int) {
	key := id.KeyInt().Int64()
	if key <= 0 {
		return -1, 0
	}
	return int(key % committeeEpochStride), int(key / committeeEpochStride)
}

// findPartyIDByNode resolves a node number within a committee whatever
// epoch that committee is in.
func findPartyIDByNode(sorted tsscommon.SortedPartyIDs, node int) *tsscommon.PartyID {
	for _, id := range sorted {
		if n, _ := nodeForPartyKey(id); n == node {
			return id
		}
	}
	return nil
}

// RestoreCeremony reconstitutes a completed ceremony from sealed material.
//
// This is what makes the recovery procedure in
// docs/deployment/KEY-RECOVERY.md a procedure rather than a hope. Before
// it existed, a party that restarted held a share it could not use: the
// share was durable in Vault, and the committee identities, threshold,
// curve and refresh epoch that tss-lib needs to sign with it were only
// ever in the process that died.
//
// The peers map is supplied by the caller rather than sealed, deliberately.
// Endpoints change -- that is most of what happens during a recovery, when
// hosts are rebuilt at new addresses -- and a sealed endpoint list would be
// the one field guaranteed to be stale exactly when it is needed. What is
// sealed is the identity of the committee, which does not change.
func (m *TSSPartyManager) RestoreCeremony(ceremonyID string, peers map[int]string) error {
	m.mu.Lock()
	_, exists := m.ceremonies[ceremonyID]
	m.mu.Unlock()
	if exists {
		return fmt.Errorf("ceremony %s is already loaded; nothing to restore", ceremonyID)
	}

	share, cc, err := LoadSealedShare(context.Background(), os.Getenv, m.partyID, ceremonyID)
	if err != nil {
		return err
	}
	if cc == nil {
		return fmt.Errorf("the sealed share for %s carries no ceremony context; it was sealed "+
			"before contexts were stored and cannot be restored into a signing party. "+
			"Re-seal it from a running party, or recover via the break-glass path", ceremonyID)
	}
	if len(peers) != cc.TotalParties {
		return fmt.Errorf("ceremony %s had %d parties; %d endpoints were supplied",
			ceremonyID, cc.TotalParties, len(peers))
	}

	// Rebuilt at the epoch the share belongs to, not at epoch zero. A
	// refreshed share's coordinates are the refreshed committee's, and
	// interpolating at the original ones yields a key controlling nothing.
	sorted := committeePartyIDs(peers, cc.Epoch)
	if findPartyIDByNode(sorted, m.partyID) == nil {
		return fmt.Errorf("this party (%d) is not among the supplied endpoints", m.partyID)
	}

	// The restored share must control the address it was sealed against.
	// A mismatch means the sealed material and the context disagree, and
	// signing with it would produce valid signatures for the wrong key.
	pubKeyHex, address, err := share.PublicKey()
	if err != nil {
		return fmt.Errorf("the restored share has no usable public key: %w", err)
	}
	if cc.Address != "" && address != cc.Address {
		return fmt.Errorf("the restored share controls %s but was sealed against %s; "+
			"refusing to load it", address, cc.Address)
	}

	restored := &tssKeygenCeremony{
		status:       ceremonyCompleted,
		selfPartyID:  m.partyID,
		threshold:    cc.Threshold,
		sortedIDs:    sorted,
		peers:        peers,
		curve:        Curve(cc.Curve),
		epoch:        cc.Epoch,
		saveData:     share,
		publicKeyHex: pubKeyHex,
		address:      address,
		sealed:       true,
	}

	m.mu.Lock()
	m.ceremonies[ceremonyID] = restored
	m.mu.Unlock()

	m.logf("restored ceremony %s from sealed material: %s on %s, epoch %d",
		ceremonyID, address, cc.Curve, cc.Epoch)
	return nil
}
