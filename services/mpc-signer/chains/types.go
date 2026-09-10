package chains

import "context"

// Signature represents a signature for any chain.
type Signature struct {
	R              string `json:"r"`               // signature component R (hex)
	S              string `json:"s"`               // signature component S (hex)
	V              byte   `json:"v"`               // recovery ID (Ethereum/Bitcoin) or 0 where the curve has none (Solana)
	SignatureBytes string `json:"signature_bytes"` // full signature (hex)
}

// ChainSigner is the interface for chain-specific signing.
//
// A note on the messageHash parameter: for Ethereum, Bitcoin and Cosmos it
// is a 32-byte hash. For Solana it is the serialized message itself, since
// Ed25519 signs the message rather than a digest of it. Each implementation
// documents its own expectation.
type ChainSigner interface {
	// SignMessage signs a message hash and returns the signature.
	SignMessage(ctx context.Context, messageHash []byte, privKey string) (*Signature, error)

	// VerifySignature verifies a signature against a message and public key.
	VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKey string) (bool, error)

	// RecoverAddress recovers the signer address from a message and signature.
	// Not all curves support this (Ed25519 does not); those implementations
	// return an error rather than a placeholder.
	RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error)

	// BuildTransaction builds a transaction message ready for signing.
	// Input: chain-specific request data
	// Output: message bytes to sign
	BuildTransaction(ctx context.Context, tx interface{}) ([]byte, error)

	// BroadcastTransaction broadcasts a signed transaction to the network.
	BroadcastTransaction(ctx context.Context, signedTx []byte) (txHash string, err error)
}

// EthereumSignRequest is the request to sign an Ethereum transaction.
type EthereumSignRequest struct {
	ChainID  uint64 `json:"chain_id"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Data     string `json:"data"`
	GasLimit uint64 `json:"gas_limit"`
	GasPrice string `json:"gas_price"`
	Nonce    uint64 `json:"nonce"`
}

// BitcoinSignRequest is the request to sign a Bitcoin transaction.
type BitcoinSignRequest struct {
	Inputs  []BitcoinInput  `json:"inputs"`
	Outputs []BitcoinOutput `json:"outputs"`
	Network string          `json:"network"` // "mainnet", "testnet", "regtest"
}

// BitcoinInput represents a UTXO input.
type BitcoinInput struct {
	Txid   string `json:"txid"`
	Vout   int    `json:"vout"`
	Amount int64  `json:"amount"`
	Script string `json:"script"` // previous output script (hex), needed to sign this input
	SegWit bool   `json:"segwit"`
}

// BitcoinOutput represents a transaction output.
type BitcoinOutput struct {
	Address string `json:"address"`
	Amount  int64  `json:"amount"` // satoshis
}

// SolanaSignRequest is the request to sign a Solana transaction.
type SolanaSignRequest struct {
	Instructions    []SolanaInstruction `json:"instructions"`
	RecentBlockhash string              `json:"recent_blockhash"` // base58
	FeePayer        string              `json:"fee_payer"`        // base58 account address
}

// SolanaInstruction represents a Solana transaction instruction.
type SolanaInstruction struct {
	ProgramID string              `json:"program_id"` // base58
	Accounts  []SolanaAccountMeta `json:"accounts"`
	Data      string              `json:"data"` // hex-encoded instruction data
}

// SolanaAccountMeta carries the per-account privileges Solana's message
// format requires. Without these flags a message cannot be serialized
// correctly at all: the header counts signers and readonly accounts, and
// account ordering is defined by exactly these two bits.
type SolanaAccountMeta struct {
	Pubkey     string `json:"pubkey"` // base58
	IsSigner   bool   `json:"is_signer"`
	IsWritable bool   `json:"is_writable"`
}

// CosmosSignRequest is the request to sign a Cosmos transaction.
type CosmosSignRequest struct {
	Messages   []map[string]interface{} `json:"messages"`
	Fee        CosmosFee                `json:"fee"`
	Timeout    uint64                   `json:"timeout"`
	AccountNum uint64                   `json:"account_number"`
	Sequence   uint64                   `json:"sequence"`
	ChainID    string                   `json:"chain_id"`
}

// CosmosFee represents a Cosmos transaction fee.
type CosmosFee struct {
	Amount string `json:"amount"`
	Gas    string `json:"gas"`
}

// ChainSignRequest is the generic request for signing on any chain.
type ChainSignRequest struct {
	ChainID  string                 `json:"chain_id"` // "ethereum", "bitcoin", "solana", "cosmos-hub"
	Message  []byte                 `json:"message"`  // pre-built message bytes (RLP hash, sighash, SignDoc hash, Solana message)
	Metadata map[string]interface{} `json:"metadata"` // chain-specific hints
}

// ChainSignResponse is the response after signing on any chain.
type ChainSignResponse struct {
	ChainID     string     `json:"chain_id"`
	Signature   *Signature `json:"signature"`
	SignedTx    string     `json:"signed_tx,omitempty"` // full signed transaction (hex), when the chain's flow produces one
	TxHash      string     `json:"tx_hash,omitempty"`
	From        string     `json:"from"`
	Status      string     `json:"status"`
	Broadcasted bool       `json:"broadcasted"`
}

// NewChainSigner returns the appropriate ChainSigner for a given chain, or
// nil for an unsupported one.
func NewChainSigner(chainID string) ChainSigner {
	switch chainID {
	case "ethereum":
		return NewEthereumSigner()
	case "bitcoin":
		return NewBitcoinSigner()
	case "solana":
		return NewSolanaSigner()
	case "cosmos-hub":
		return NewCosmosSigner()
	default:
		return nil
	}
}
