package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"forge-crypto/mpc-signer/keys"
)

// Signing with a key this package never sees.
//
// Every ChainSigner.SignMessage takes a raw private key string and parses
// it. That stays exactly as it was -- it is tested, deployed, and the
// oracle below. What this file adds is a second entry point on the three
// secp256k1 chains that takes a keys.KeySigner instead, so the key can be
// a handle to one inside a hardware security module.
//
// Added alongside rather than by changing SignMessage's signature, for
// two reasons. Solana is Ed25519, which a secp256k1 KeySigner cannot
// sign for and most deployed PKCS#11 hardware cannot either; changing the
// shared interface would drag it into a scope it does not belong in. And
// the existing raw-key methods are the reference the new ones are tested
// against: for the same key and message, both paths must produce
// byte-identical output (keysigned_test.go), which is only a meaningful
// check while the old path still exists unchanged.

// KeySignedChain is implemented by chains that can sign with a
// keys.KeySigner.
type KeySignedChain interface {
	SignMessageWithKey(ctx context.Context, messageHash []byte, key keys.KeySigner) (*Signature, error)
}

func signDigest(ctx context.Context, key keys.KeySigner, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("message hash must be 32 bytes, got %d", len(digest))
	}
	sig, err := key.SignDigest(ctx, digest)
	if err != nil {
		return nil, fmt.Errorf("signing with %s: %w", key.Describe(), err)
	}
	if len(sig) != 65 || sig[64] > 1 {
		// A KeySigner promises [R||S||V]; a backend that returns
		// something else is broken, and re-encoding it per chain would
		// silently produce garbage in three different formats.
		return nil, fmt.Errorf("%s returned a malformed signature (%d bytes)", key.Describe(), len(sig))
	}
	return sig, nil
}

// SignMessageWithKey is Ethereum's SignMessage with the key held elsewhere.
// Layout identical: [R || S || V], 0x-prefixed.
func (e *EthereumSigner) SignMessageWithKey(ctx context.Context, messageHash []byte, key keys.KeySigner) (*Signature, error) {
	sig, err := signDigest(ctx, key, messageHash)
	if err != nil {
		return nil, err
	}
	return &Signature{
		R:              hex.EncodeToString(sig[:32]),
		S:              hex.EncodeToString(sig[32:64]),
		V:              sig[64],
		SignatureBytes: "0x" + hex.EncodeToString(sig),
	}, nil
}

// SignMessageWithKey is Cosmos's SignMessage with the key held elsewhere.
// Same secp256k1 signature; Cosmos's own encoding, unprefixed.
func (c *CosmosSigner) SignMessageWithKey(ctx context.Context, messageHash []byte, key keys.KeySigner) (*Signature, error) {
	sig, err := signDigest(ctx, key, messageHash)
	if err != nil {
		return nil, err
	}
	return &Signature{
		R:              hex.EncodeToString(sig[:32]),
		S:              hex.EncodeToString(sig[32:64]),
		V:              sig[64],
		SignatureBytes: hex.EncodeToString(sig),
	}, nil
}

// SignMessageWithKey is Bitcoin's SignMessage with the key held elsewhere.
//
// Bitcoin's compact layout puts the recovery byte first and offsets it:
// 27 + recid, plus 4 because the address this key controls is derived
// from the compressed public key. Getting the +4 wrong still produces a
// signature that recovers -- to the uncompressed key, whose address is a
// different address. btcec.SignCompact does this internally; here it is
// done by hand, which is why the equivalence test exists.
func (b *BitcoinSigner) SignMessageWithKey(ctx context.Context, messageHash []byte, key keys.KeySigner) (*Signature, error) {
	sig, err := signDigest(ctx, key, messageHash)
	if err != nil {
		return nil, err
	}
	compact := make([]byte, 65)
	compact[0] = 27 + 4 + sig[64]
	copy(compact[1:], sig[:64])
	return &Signature{
		V:              compact[0],
		R:              hex.EncodeToString(compact[1:33]),
		S:              hex.EncodeToString(compact[33:65]),
		SignatureBytes: hex.EncodeToString(compact),
	}, nil
}

// SignTransactionInputWithKey signs one P2PKH input with a KeySigner.
//
// SignTransactionInput hands a private key to txscript.SignatureScript,
// which computes the sighash, signs it and assembles the script in one
// call -- and so needs the key. This is the same three steps done
// separately, so that only the signing step touches the key, and that
// step can happen inside an HSM:
//
//  1. the sighash, from txscript -- the same function SignatureScript
//     uses, so the digest cannot drift from what a node verifies;
//  2. the signature, from the KeySigner, already normalised to low S;
//  3. DER encoding plus the sighash-type byte, then
//     <signature> <compressed public key>.
//
// P2PKH only, as SignTransactionInput is.
func (b *BitcoinSigner) SignTransactionInputWithKey(
	ctx context.Context,
	rawTx []byte,
	inputIndex int,
	prevOutScript []byte,
	key keys.KeySigner,
) ([]byte, error) {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return nil, fmt.Errorf("failed to deserialize transaction: %w", err)
	}
	if inputIndex < 0 || inputIndex >= len(tx.TxIn) {
		return nil, fmt.Errorf("input index %d out of range (%d inputs)", inputIndex, len(tx.TxIn))
	}

	sighash, err := txscript.CalcSignatureHash(prevOutScript, txscript.SigHashAll, tx, inputIndex)
	if err != nil {
		return nil, fmt.Errorf("computing the sighash for input %d: %w", inputIndex, err)
	}
	sig, err := signDigest(ctx, key, sighash)
	if err != nil {
		return nil, err
	}

	// btcec v1's Serialize emits canonical DER and folds S low again. S is
	// already low from the KeySigner, so that is a no-op -- kept rather
	// than hand-rolling DER, because DER has enough length and sign-bit
	// edge cases that a second implementation is a second place to be
	// wrong.
	der := (&btcec.Signature{
		R: new(big.Int).SetBytes(sig[:32]),
		S: new(big.Int).SetBytes(sig[32:64]),
	}).Serialize()

	pub, err := keys.CompressedPublicKey(key)
	if err != nil {
		return nil, err
	}

	sigScript, err := txscript.NewScriptBuilder().
		AddData(append(der, byte(txscript.SigHashAll))).
		AddData(pub).
		Script()
	if err != nil {
		return nil, fmt.Errorf("building the signature script: %w", err)
	}
	tx.TxIn[inputIndex].SignatureScript = sigScript

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize signed transaction: %w", err)
	}
	return buf.Bytes(), nil
}

// SignMultiChainWithKey is SignMultiChain with the key held elsewhere.
//
// A chain that cannot sign with a KeySigner -- Solana, whose curve is
// Ed25519 -- is refused by name rather than handed a raw-key fallback.
// A deployment configured for hardware signing believes its keys are in
// hardware; quietly signing Solana with a software key would make that
// belief false for one chain without anyone being told.
func (sr *SignerRouter) SignMultiChainWithKey(ctx context.Context, req *ChainSignRequest, key keys.KeySigner) (*ChainSignResponse, error) {
	signer, ok := sr.signers[req.ChainID]
	if !ok {
		return nil, fmt.Errorf("unsupported chain: %s", req.ChainID)
	}
	keyed, ok := signer.(KeySignedChain)
	if !ok {
		return nil, fmt.Errorf("chain %s cannot sign with %s: its signatures are not secp256k1, "+
			"and this deployment does not hold a key for it anywhere else", req.ChainID, key.Describe())
	}

	sig, err := keyed.SignMessageWithKey(ctx, req.Message, key)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}
	from, err := signer.RecoverAddress(ctx, req.Message, sig)
	if err != nil {
		return nil, fmt.Errorf("address recovery failed: %w", err)
	}
	return &ChainSignResponse{
		ChainID:   req.ChainID,
		Signature: sig,
		From:      from,
		Status:    "signed",
	}, nil
}
