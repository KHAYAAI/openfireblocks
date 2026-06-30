//go:build tss

// Package tss implements threshold ECDSA (secp256k1) key generation and signing
// using Binance/bnb-chain tss-lib. It proves the cryptographic core of real MPC
// signing: a k-of-n key where the private key is never reconstructed, producing
// signatures that verify exactly like a normal Ethereum signature.
//
// For clarity and testability the parties are driven in-process here (each as a
// goroutine with in-memory message routing). A production deployment runs each
// party in its own isolated process/host with authenticated transport and key
// shares sealed in Vault — the protocol and message handling are identical; only
// the transport changes.
package tss

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/bnb-chain/tss-lib/v2/common"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/v2/tss"
	"github.com/ethereum/go-ethereum/crypto"
)

// KeyShares holds the per-party save data produced by distributed key
// generation, plus the shared public key. In production each share is held by a
// separate party and stored in Vault; never co-located as they are here.
type KeyShares struct {
	Shares    []keygen.LocalPartySaveData
	PublicKey *ecdsa.PublicKey
	parties   tss.SortedPartyIDs
	threshold int
}

// Address returns the Ethereum address controlled by the threshold key.
func (k *KeyShares) Address() string {
	return crypto.PubkeyToAddress(*k.PublicKey).Hex()
}

// Keygen runs distributed key generation for n parties with the given threshold
// (threshold+1 parties are required to sign). secp256k1 is used (Ethereum).
func Keygen(ctx context.Context, n, threshold int) (*KeyShares, error) {
	if n < 2 || threshold < 1 || threshold >= n {
		return nil, fmt.Errorf("invalid (n=%d, threshold=%d): need n>=2 and 1<=threshold<n", n, threshold)
	}

	partyIDs := tss.GenerateTestPartyIDs(n)
	peerCtx := tss.NewPeerContext(partyIDs)
	curve := tss.S256()

	parties := make([]*keygen.LocalParty, n)
	outCh := make(chan tss.Message, n*n)
	endCh := make(chan keygen.LocalPartySaveData, n)
	errCh := make(chan error, n)

	for i := 0; i < n; i++ {
		params := tss.NewParameters(curve, peerCtx, partyIDs[i], n, threshold)
		// GeneratePreParams is the slow step; bound it by the caller's context.
		pre, err := keygen.GeneratePreParams(1 * time.Minute)
		if err != nil {
			return nil, fmt.Errorf("pre-params: %w", err)
		}
		parties[i] = keygen.NewLocalParty(params, outCh, endCh, *pre).(*keygen.LocalParty)
	}

	for i := 0; i < n; i++ {
		go func(p *keygen.LocalParty) {
			if err := p.Start(); err != nil {
				errCh <- err
			}
		}(parties[i])
	}

	saves := make([]keygen.LocalPartySaveData, n)
	got := 0
	for got < n {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, err
		case msg := <-outCh:
			if err := routeKeygen(msg, parties, partyIDs); err != nil {
				return nil, err
			}
		case save := <-endCh:
			idx := indexOf(partyIDs, save.ShareID)
			if idx < 0 {
				idx = got
			}
			saves[idx] = save
			got++
		}
	}

	pk := &ecdsa.PublicKey{
		Curve: crypto.S256(),
		X:     saves[0].ECDSAPub.X(),
		Y:     saves[0].ECDSAPub.Y(),
	}
	return &KeyShares{Shares: saves, PublicKey: pk, parties: partyIDs, threshold: threshold}, nil
}

// Sign produces a threshold ECDSA signature over a 32-byte message hash using
// the first (threshold+1) parties. The returned 65-byte [R||S||V] signature is
// Ethereum-compatible and verifies against the shared public key / address.
func (k *KeyShares) Sign(ctx context.Context, hash []byte) ([]byte, error) {
	if len(hash) != 32 {
		return nil, errors.New("hash must be 32 bytes")
	}
	participants := k.threshold + 1

	// The signing committee is the first `participants` parties.
	committee := k.parties[:participants]
	peerCtx := tss.NewPeerContext(committee)
	curve := tss.S256()
	msg := new(big.Int).SetBytes(hash)

	parties := make([]*signing.LocalParty, participants)
	outCh := make(chan tss.Message, participants*participants)
	endCh := make(chan common.SignatureData, participants)
	errCh := make(chan error, participants)

	for i := 0; i < participants; i++ {
		params := tss.NewParameters(curve, peerCtx, committee[i], participants, k.threshold)
		parties[i] = signing.NewLocalParty(msg, params, k.Shares[i], outCh, endCh).(*signing.LocalParty)
	}

	for i := 0; i < participants; i++ {
		go func(p *signing.LocalParty) {
			if err := p.Start(); err != nil {
				errCh <- err
			}
		}(parties[i])
	}

	var sigData common.SignatureData
	done := 0
	for done < participants {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, err
		case m := <-outCh:
			if err := routeSigning(m, parties, committee); err != nil {
				return nil, err
			}
		case sd := <-endCh:
			sigData = sd
			done++
		}
	}

	// Assemble the 65-byte Ethereum signature [R || S || V]. tss-lib returns R
	// and S as big-endian byte slices and a single recovery byte.
	sig := make([]byte, 65)
	copy(sig[32-len(sigData.R):32], sigData.R)
	copy(sig[64-len(sigData.S):64], sigData.S)
	sig[64] = sigData.SignatureRecovery[0]
	return sig, nil
}

// --- message routing helpers (in-process transport) ---

var routeMu sync.Mutex

func routeKeygen(msg tss.Message, parties []*keygen.LocalParty, ids tss.SortedPartyIDs) error {
	routeMu.Lock()
	defer routeMu.Unlock()
	data, _, err := msg.WireBytes()
	if err != nil {
		return err
	}
	from := msg.GetFrom()
	if msg.IsBroadcast() {
		for i, p := range parties {
			if ids[i].Index == from.Index {
				continue
			}
			if _, err := p.UpdateFromBytes(data, from, true); err != nil {
				return err
			}
		}
		return nil
	}
	for _, to := range msg.GetTo() {
		for i, p := range parties {
			if ids[i].Index == to.Index {
				if _, err := p.UpdateFromBytes(data, from, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func routeSigning(msg tss.Message, parties []*signing.LocalParty, ids tss.SortedPartyIDs) error {
	routeMu.Lock()
	defer routeMu.Unlock()
	data, _, err := msg.WireBytes()
	if err != nil {
		return err
	}
	from := msg.GetFrom()
	if msg.IsBroadcast() {
		for i, p := range parties {
			if ids[i].Index == from.Index {
				continue
			}
			if _, err := p.UpdateFromBytes(data, from, true); err != nil {
				return err
			}
		}
		return nil
	}
	for _, to := range msg.GetTo() {
		for i, p := range parties {
			if ids[i].Index == to.Index {
				if _, err := p.UpdateFromBytes(data, from, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func indexOf(ids tss.SortedPartyIDs, shareID *big.Int) int {
	for i, id := range ids {
		if id.KeyInt().Cmp(shareID) == 0 {
			return i
		}
	}
	return -1
}
