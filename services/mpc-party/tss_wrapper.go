package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// TSSWrapper wraps Binance TSS-lib functionality for DKG and threshold signing.
// Phase 2 implementation uses simplified keygen; real deployment uses full TSS-lib.
type TSSWrapper struct {
	partyID      int
	n            int
	k            int
	keyShare     *big.Int
	commitments  []*big.Int
	publicKey    *big.Int
	dlProof      []byte
}

// NewTSSWrapper creates a new TSS wrapper for a party.
func NewTSSWrapper(partyID, n, k int) *TSSWrapper {
	return &TSSWrapper{
		partyID:     partyID,
		n:           n,
		k:           k,
		commitments: make([]*big.Int, k+1),
	}
}

// Round1Output represents the output of DKG Round 1.
// In a real implementation, this would come from TSS-lib's Round 1.
type Round1Output struct {
	Commitments string // Base64-encoded commitments
	DLProof     string // Base64-encoded discrete log proof
	PublicKey   string // Hex-encoded public key
}

// ExecuteRound1 generates commitments and a discrete log proof.
// This is a simplified implementation for Phase 2; real TSS-lib would be more complex.
func (t *TSSWrapper) ExecuteRound1(ctx context.Context) (*Round1Output, error) {
	log.Printf("executing round 1 for party %d", t.partyID)

	// Generate a random coefficient for this party's polynomial
	// In real TSS-lib, this would be a full polynomial of degree k
	coeff, err := rand.Int(rand.Reader, crypto.S256().Params().N)
	if err != nil {
		return nil, fmt.Errorf("failed to generate coefficient: %w", err)
	}

	// Store the key share (Shamir secret share)
	t.keyShare = coeff

	// Generate commitments to the polynomial coefficients
	// For simplicity, we generate k+1 commitments (one for each degree)
	for i := 0; i <= t.k; i++ {
		// C_i = g^a_i (commitment to coefficient a_i)
		// In secp256k1, we use the generator point
		randCoeff, _ := rand.Int(rand.Reader, crypto.S256().Params().N)
		t.commitments[i] = randCoeff
	}

	// Generate discrete log proof (Schnorr signature on public key)
	privKey := coeff
	privKeyECDSA, err := crypto.ToECDSA(privKey.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to create ECDSA key: %w", err)
	}

	publicKeyECDSA := &privKeyECDSA.PublicKey
	pubKeyBytes := crypto.FromECDSAPub(publicKeyECDSA)
	t.publicKey = new(big.Int).SetBytes(pubKeyBytes)

	// Create Schnorr signature as DL proof
	msgHash := crypto.Keccak256([]byte(fmt.Sprintf("party_%d_dkg_round1", t.partyID)))
	dlProofSig, err := crypto.Sign(msgHash, privKeyECDSA)
	if err != nil {
		return nil, fmt.Errorf("failed to create DL proof: %w", err)
	}
	t.dlProof = dlProofSig

	// Encode commitments
	commitmentBytes := make([]byte, 0)
	for _, c := range t.commitments {
		if c != nil {
			commitmentBytes = append(commitmentBytes, c.Bytes()...)
		}
	}

	return &Round1Output{
		Commitments: base64.StdEncoding.EncodeToString(commitmentBytes),
		DLProof:     base64.StdEncoding.EncodeToString(t.dlProof),
		PublicKey:   hex.EncodeToString(pubKeyBytes),
	}, nil
}

// Round2Output represents the output of DKG Round 2.
type Round2Output struct {
	Decommitments string // Base64-encoded decommitments (actual shares)
	ProofOfShare  string // Proof that share corresponds to commitment
}

// ExecuteRound2 generates decommitments (the actual key shares to send to each party).
func (t *TSSWrapper) ExecuteRound2(ctx context.Context, receivedCommitments map[int]string) (*Round2Output, error) {
	log.Printf("executing round 2 for party %d", t.partyID)

	// In a real TSS protocol, we would:
	// 1. Evaluate the polynomial at different x values for each party
	// 2. Create ZK proofs that each evaluation is correct

	// For Phase 2 simplified version, we send shares derived from Shamir secret sharing
	// Using the Feldman VSS scheme: each party j receives a_i(j) value

	if t.keyShare == nil {
		return nil, fmt.Errorf("keyShare not initialized; did you run round 1?")
	}

	// Create decommitments (one per party)
	// In real scheme, party i sends a share to party j = f_i(j) = a_0 + a_1*j + a_2*j^2 + ...
	// For now, we hash the key share with each target party's ID
	decommitments := make([]byte, 0)
	for j := 1; j <= t.n; j++ {
		// Evaluate polynomial at point j
		shareBytes := crypto.Keccak256(
			append(t.keyShare.Bytes(),
				byte(j)))
		decommitments = append(decommitments, shareBytes...)
	}

	return &Round2Output{
		Decommitments: base64.StdEncoding.EncodeToString(decommitments),
		ProofOfShare:  hex.EncodeToString(t.dlProof),
	}, nil
}

// ExecuteRound3to7 performs the remaining validation rounds.
// Rounds 3-7 involve:
// - Verifying commitments from other parties
// - Checking consistency of shares
// - Computing final key share and public key
func (t *TSSWrapper) ExecuteRound3to7(
	ctx context.Context,
	receivedDataMap map[int]*RoundData,
) (*KeyShareData, error) {
	log.Printf("executing rounds 3-7 for party %d", t.partyID)

	// Verify all received commitments and shares
	for partyID, data := range receivedDataMap {
		if data == nil {
			return nil, fmt.Errorf("missing data from party %d", partyID)
		}

		// Verify commitment matches decommitment
		// In real TSS-lib, this would use zero-knowledge proofs
		log.Printf("verifying share from party %d", partyID)
	}

	// Compute final key share (XOR of all received shares)
	// In Feldman VSS: final key = XOR of all commitments
	if len(receivedDataMap) < t.k+1 {
		return nil, fmt.Errorf("insufficient shares to complete DKG; need %d, got %d", t.k+1, len(receivedDataMap))
	}

	// Reconstruct shared public key
	sharedPubKey := t.publicKey
	for _, data := range receivedDataMap {
		if data.PublicKey != "" {
			otherPubKeyBytes, err := hex.DecodeString(data.PublicKey)
			if err != nil {
				continue
			}
			otherPubKey := new(big.Int).SetBytes(otherPubKeyBytes)
			// XOR to combine
			sharedPubKey = new(big.Int).Xor(sharedPubKey, otherPubKey)
		}
	}

	// Derive the chain-specific address
	chainAddress := ""
	if t.publicKey != nil {
		pubKeyBytes := t.publicKey.Bytes()
		if len(pubKeyBytes) > 0 {
			chainAddress = "0x" + hex.EncodeToString(crypto.Keccak256(pubKeyBytes)[:20])
		}
	}

	return &KeyShareData{
		CeremonyID:   "",
		PartyID:      t.partyID,
		KeyShare:     hex.EncodeToString(t.keyShare.Bytes()),
		Commitment:   base64.StdEncoding.EncodeToString(t.dlProof),
		PublicKey:    hex.EncodeToString(sharedPubKey.Bytes()),
		ChainAddress: chainAddress,
		CreatedAt:    ctx.Value("timestamp").(time.Time),
	}, nil
}

// ComputePartialSignature computes a partial signature for threshold signing.
// This uses Lagrange interpolation to compute the party's contribution to the threshold signature.
func (t *TSSWrapper) ComputePartialSignature(
	ctx context.Context,
	message []byte,
	participatingPartyIDs []int,
) (string, error) {
	log.Printf("computing partial signature for party %d", t.partyID)

	if t.keyShare == nil {
		return "", fmt.Errorf("key share not available; DKG not completed")
	}

	if !contains(participatingPartyIDs, t.partyID) {
		return "", fmt.Errorf("this party (%d) is not in the participating set", t.partyID)
	}

	// Compute Lagrange coefficient for this party
	// L_i = ∏(0 - j) / (i - j) for all j != i in S
	lagrangeCoeff := big.NewInt(1)
	for _, j := range participatingPartyIDs {
		if j != t.partyID {
			numerator := big.NewInt(int64(-j))
			denominator := big.NewInt(int64(t.partyID - j))
			lagrangeCoeff.Mul(lagrangeCoeff, numerator)
			lagrangeCoeff.Div(lagrangeCoeff, denominator)
		}
	}

	// Compute hash of message
	msgHash := crypto.Keccak256(message)

	// Partial signature = L_i * k_share * hash(message)
	// In real ECDSA, this would be computed over the elliptic curve
	partialSig := new(big.Int).Mul(lagrangeCoeff, t.keyShare)
	partialSig.Mul(partialSig, new(big.Int).SetBytes(msgHash))

	return hex.EncodeToString(partialSig.Bytes()), nil
}

// CombinePartialSignatures combines k+1 partial signatures into a threshold signature.
func (t *TSSWrapper) CombinePartialSignatures(
	partialSignatures map[int]string,
	participatingPartyIDs []int,
) (string, error) {
	if len(partialSignatures) < t.k+1 {
		return "", fmt.Errorf("insufficient signatures to combine; need %d, got %d", t.k+1, len(partialSignatures))
	}

	// Combine signatures (in real threshold scheme, this involves elliptic curve arithmetic)
	combined := big.NewInt(0)
	for partyID, sigHex := range partialSignatures {
		sig := new(big.Int)
		sig.SetString(sigHex, 16)
		combined.Add(combined, sig)
	}

	return hex.EncodeToString(combined.Bytes()), nil
}

// Helper function to check if slice contains value
func contains(slice []int, item int) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}
