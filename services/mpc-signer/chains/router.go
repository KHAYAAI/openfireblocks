package chains

import (
	"context"
	"encoding/hex"
	"fmt"
)

// SignerRouter routes signing requests to the appropriate chain-specific signer.
type SignerRouter struct {
	signers map[string]ChainSigner
}

// NewSignerRouter creates a new signer router with all supported chains.
func NewSignerRouter() *SignerRouter {
	return &SignerRouter{
		signers: map[string]ChainSigner{
			"ethereum":   NewEthereumSigner(),
			"bitcoin":    NewBitcoinSigner(),
			"solana":     NewSolanaSigner(),
			"cosmos-hub": NewCosmosSigner(),
		},
	}
}

// SignMultiChain routes a signing request to the appropriate chain signer.
func (sr *SignerRouter) SignMultiChain(ctx context.Context, req *ChainSignRequest, privKeyHex string) (*ChainSignResponse, error) {
	signer, ok := sr.signers[req.ChainId]
	if !ok {
		return nil, fmt.Errorf("unsupported chain: %s", req.ChainId)
	}

	// Sign the message
	sig, err := signer.SignMessage(ctx, req.Message, privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// Recover address
	from, err := signer.RecoverAddress(ctx, req.Message, sig)
	if err != nil {
		// Some chains don't support recovery (e.g., Solana)
		from = "unknown"
	}

	return &ChainSignResponse{
		ChainID:    req.ChainId,
		Signature:  sig,
		SignedTx:   "", // TODO: Add full transaction building
		From:       from,
		Status:     "signed",
		Broadcasted: false,
	}, nil
}

// VerifySignature verifies a signature for a specific chain.
func (sr *SignerRouter) VerifySignature(ctx context.Context, chainId string, message []byte, signature *Signature, pubKey string) (bool, error) {
	signer, ok := sr.signers[chainId]
	if !ok {
		return false, fmt.Errorf("unsupported chain: %s", chainId)
	}

	return signer.VerifySignature(ctx, message, signature, pubKey)
}

// RecoverAddress recovers the signer address for a specific chain.
func (sr *SignerRouter) RecoverAddress(ctx context.Context, chainId string, message []byte, signature *Signature) (string, error) {
	signer, ok := sr.signers[chainId]
	if !ok {
		return "", fmt.Errorf("unsupported chain: %s", chainId)
	}

	return signer.RecoverAddress(ctx, message, signature)
}

// BuildTransaction builds a transaction for a specific chain.
func (sr *SignerRouter) BuildTransaction(ctx context.Context, chainId string, tx interface{}) ([]byte, error) {
	signer, ok := sr.signers[chainId]
	if !ok {
		return nil, fmt.Errorf("unsupported chain: %s", chainId)
	}

	return signer.BuildTransaction(ctx, tx)
}

// BroadcastTransaction broadcasts a signed transaction for a specific chain.
func (sr *SignerRouter) BroadcastTransaction(ctx context.Context, chainId string, signedTx []byte) (string, error) {
	signer, ok := sr.signers[chainId]
	if !ok {
		return "", fmt.Errorf("unsupported chain: %s", chainId)
	}

	return signer.BroadcastTransaction(ctx, signedTx)
}

// IsValidChainId checks if a chain is supported.
func (sr *SignerRouter) IsValidChainId(chainId string) bool {
	_, ok := sr.signers[chainId]
	return ok
}

// SupportedChains returns a list of supported chains.
func (sr *SignerRouter) SupportedChains() []string {
	chains := make([]string, 0, len(sr.signers))
	for chainId := range sr.signers {
		chains = append(chains, chainId)
	}
	return chains
}
