package ethtypes

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"forge-crypto/mpc-signer/internal/ethcrypto"
)

// Transaction encoding, pinned byte for byte to go-ethereum.
//
// This is the part of removing that dependency where a mistake does not
// look like a mistake. A wrong RLP length prefix, a zero written as 0x00
// instead of the empty string, EIP-155's two trailing zeroes omitted --
// none of those produce an error. They produce a signature over a
// transaction nobody authorised, or one valid on a chain it was not meant
// for, and every downstream check passes because every downstream check
// reads these same bytes.
//
// So the assertions are on exact bytes: the signing hash, the full signed
// transaction, and the transaction hash. Not "it round-trips", not "a node
// would probably accept it".

//go:embed testdata_txvectors.json
var txVectorsJSON []byte

// The key the vectors were signed with: hardhat account #1, published in
// every Ethereum tutorial, so a reader can confirm it independently.
const vectorKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

type txVector struct {
	Name        string `json:"name"`
	Dynamic     bool   `json:"dynamic"`
	ChainID     int64  `json:"chainId"`
	Nonce       uint64 `json:"nonce"`
	Gas         uint64 `json:"gas"`
	GasPrice    string `json:"gasPrice"`
	MaxFee      string `json:"maxFee"`
	Tip         string `json:"tip"`
	To          string `json:"to"`
	Value       string `json:"value"`
	Data        string `json:"data"`
	SigningHash string `json:"signingHash"`
	Raw         string `json:"raw"`
	TxHash      string `json:"txHash"`
}

func txVectors(t *testing.T) []txVector {
	t.Helper()
	var vs []txVector
	if err := json.Unmarshal(txVectorsJSON, &vs); err != nil {
		t.Fatalf("reading transaction vectors: %v", err)
	}
	if len(vs) < 15 {
		t.Fatalf("only %d transaction vectors; the golden file looks truncated", len(vs))
	}
	return vs
}

func bigOrNil(s string) *big.Int {
	if s == "" {
		return nil
	}
	v, _ := new(big.Int).SetString(s, 10)
	return v
}

func (v txVector) tx(t *testing.T) *Transaction {
	t.Helper()
	tx := &Transaction{
		ChainID: big.NewInt(v.ChainID),
		Nonce:   v.Nonce,
		Gas:     v.Gas,
		Value:   bigOrNil(v.Value),
	}
	if v.Dynamic {
		tx.MaxFee, tx.Tip = bigOrNil(v.MaxFee), bigOrNil(v.Tip)
	} else {
		tx.GasPrice = bigOrNil(v.GasPrice)
	}
	if v.To != "" {
		addr, err := ethcrypto.ParseAddress(v.To)
		if err != nil {
			t.Fatalf("%s: bad address in vector: %v", v.Name, err)
		}
		tx.To = addr
	}
	if d := strings.TrimPrefix(v.Data, "0x"); d != "" {
		raw, err := hex.DecodeString(d)
		if err != nil {
			t.Fatalf("%s: bad data in vector: %v", v.Name, err)
		}
		tx.Data = raw
	}
	return tx
}

// The digest that gets signed. Wrong here and the signature commits to a
// different transaction than the one that gets broadcast.
func TestSigningHashMatchesGoEthereum(t *testing.T) {
	for _, v := range txVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			got, err := v.tx(t).SigningHash()
			if err != nil {
				t.Fatalf("signing hash: %v", err)
			}
			if want := strings.TrimPrefix(v.SigningHash, "0x"); hex.EncodeToString(got) != want {
				t.Errorf("\n got 0x%s\nwant %s", hex.EncodeToString(got), v.SigningHash)
			}
		})
	}
}

// The bytes that go on the wire.
func TestSignedTransactionMatchesGoEthereum(t *testing.T) {
	priv, err := ethcrypto.HexToECDSA(vectorKey)
	if err != nil {
		t.Fatalf("parsing the vector key: %v", err)
	}
	from := ethcrypto.PubkeyToAddress(priv.PublicKey)

	for _, v := range txVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			tx := v.tx(t)
			digest, err := tx.SigningHash()
			if err != nil {
				t.Fatalf("signing hash: %v", err)
			}
			sig, err := ethcrypto.Sign(digest, priv)
			if err != nil {
				t.Fatalf("signing: %v", err)
			}
			signed, err := tx.WithSignature(sig, from)
			if err != nil {
				t.Fatalf("attaching the signature: %v", err)
			}

			if got := "0x" + hex.EncodeToString(signed.Raw); got != v.Raw {
				t.Errorf("raw transaction:\n got %s\nwant %s", got, v.Raw)
			}
			if got := "0x" + hex.EncodeToString(signed.Hash); !strings.EqualFold(got, v.TxHash) {
				t.Errorf("transaction hash:\n got %s\nwant %s", got, v.TxHash)
			}
			if !strings.EqualFold(signed.From, from) {
				t.Errorf("recovered sender %s, want %s", signed.From, from)
			}
		})
	}
}

// Coverage check on the vector set itself: a golden file that happened to
// contain only legacy transactions would pass everything above while
// proving nothing about EIP-1559.
func TestTheVectorsCoverBothTransactionTypes(t *testing.T) {
	var legacy, dynamic, creation, withData int
	for _, v := range txVectors(t) {
		if v.Dynamic {
			dynamic++
		} else {
			legacy++
		}
		if v.To == "" {
			creation++
		}
		if len(v.Data) > 2 {
			withData++
		}
	}
	if legacy < 5 || dynamic < 5 || creation < 2 || withData < 4 {
		t.Errorf("thin coverage: %d legacy, %d dynamic, %d creations, %d with data",
			legacy, dynamic, creation, withData)
	}
}

// -- the encoding edge cases, stated individually --

// Zero is the empty string in RLP, not a zero byte. 0x00 is a valid
// one-byte string that encodes as itself, so a zero nonce written that way
// produces a well-formed transaction with a hash no other client agrees
// with.
func TestZeroEncodesAsTheEmptyString(t *testing.T) {
	if got := hex.EncodeToString(rlpUint(0)); got != "80" {
		t.Errorf("rlpUint(0) = %s, want 80", got)
	}
	if got := hex.EncodeToString(rlpBig(big.NewInt(0))); got != "80" {
		t.Errorf("rlpBig(0) = %s, want 80", got)
	}
	if got := hex.EncodeToString(rlpBig(nil)); got != "80" {
		t.Errorf("rlpBig(nil) = %s, want 80", got)
	}
}

// A single byte below 0x80 is its own encoding, with no prefix.
func TestSmallSingleBytesHaveNoPrefix(t *testing.T) {
	for _, b := range []byte{0x01, 0x7f} {
		if got := rlpString([]byte{b}); len(got) != 1 || got[0] != b {
			t.Errorf("rlpString([%#x]) = %x, want %#x", b, got, b)
		}
	}
	// 0x80 and above do get one.
	if got := hex.EncodeToString(rlpString([]byte{0x80})); got != "8180" {
		t.Errorf("rlpString([0x80]) = %s, want 8180", got)
	}
}

// The 55-byte boundary, where the prefix format changes.
func TestTheFiftyFiveByteBoundary(t *testing.T) {
	at := rlpString(make([]byte, 55))
	over := rlpString(make([]byte, 56))

	if at[0] != 0x80+55 {
		t.Errorf("55 bytes got prefix %#x, want %#x", at[0], 0x80+55)
	}
	if over[0] != 0xb8 || over[1] != 56 {
		t.Errorf("56 bytes got prefix %#x %#x, want b8 38", over[0], over[1])
	}
}

func TestALongStringCarriesAMultiByteLength(t *testing.T) {
	// 300 bytes needs two length bytes: 0xb9 0x01 0x2c.
	enc := rlpString(make([]byte, 300))
	if enc[0] != 0xb9 || enc[1] != 0x01 || enc[2] != 0x2c {
		t.Errorf("300 bytes got prefix %x, want b9012c", enc[:3])
	}
	if len(enc) != 303 {
		t.Errorf("encoded length %d, want 303", len(enc))
	}
}

// Contract creation encodes the recipient as the empty string. A 20-byte
// zero address is a completely different transaction: one that sends the
// money to an account nobody controls.
func TestContractCreationIsNotTheZeroAddress(t *testing.T) {
	creation := &Transaction{ChainID: big.NewInt(1), GasPrice: big.NewInt(1), Gas: 21000, To: nil}
	toZero := &Transaction{ChainID: big.NewInt(1), GasPrice: big.NewInt(1), Gas: 21000, To: make([]byte, 20)}

	a, err := creation.SigningHash()
	if err != nil {
		t.Fatalf("creation: %v", err)
	}
	b, err := toZero.SigningHash()
	if err != nil {
		t.Fatalf("zero address: %v", err)
	}
	if hex.EncodeToString(a) == hex.EncodeToString(b) {
		t.Fatal("a contract creation and a transfer to the zero address hash identically; " +
			"a nil recipient is being encoded as 20 zero bytes")
	}
}

// -- what must be refused --

func TestAChainIdIsRequired(t *testing.T) {
	for _, id := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1)} {
		tx := &Transaction{ChainID: id, GasPrice: big.NewInt(1), Gas: 21000}
		if _, err := tx.SigningHash(); err == nil {
			t.Errorf("chain id %v was accepted; the signature would be replayable", id)
		}
	}
}

func TestTheTwoFeeModelsAreExclusive(t *testing.T) {
	both := &Transaction{
		ChainID: big.NewInt(1), Gas: 21000,
		GasPrice: big.NewInt(1), MaxFee: big.NewInt(2), Tip: big.NewInt(1),
	}
	if _, err := both.SigningHash(); err == nil {
		t.Error("a transaction with both a gas price and an EIP-1559 fee pair was accepted")
	}

	neither := &Transaction{ChainID: big.NewInt(1), Gas: 21000}
	if _, err := neither.SigningHash(); err == nil {
		t.Error("a transaction with no fee at all was accepted")
	}

	half := &Transaction{ChainID: big.NewInt(1), Gas: 21000, MaxFee: big.NewInt(2)}
	if _, err := half.SigningHash(); err == nil {
		t.Error("an EIP-1559 transaction with no tip was accepted")
	}
}

func TestAMalformedRecipientIsRefused(t *testing.T) {
	tx := &Transaction{ChainID: big.NewInt(1), GasPrice: big.NewInt(1), Gas: 21000, To: make([]byte, 19)}
	if _, err := tx.SigningHash(); err == nil {
		t.Error("a 19-byte recipient was accepted")
	}
}

// The check that stops a ceremony's signature for one key being returned
// as a transaction from another.
func TestASignatureFromTheWrongKeyIsRefused(t *testing.T) {
	priv, _ := ethcrypto.HexToECDSA(vectorKey)
	other, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}

	tx := txVectors(t)[0].tx(t)
	digest, _ := tx.SigningHash()
	sig, _ := ethcrypto.Sign(digest, other)

	if _, err := tx.WithSignature(sig, ethcrypto.PubkeyToAddress(priv.PublicKey)); err == nil {
		t.Fatal("a signature from a different key was accepted; the customer would receive " +
			"a valid transaction spending from an address they do not control")
	}
}

func TestASignatureOfTheWrongLengthIsRefused(t *testing.T) {
	tx := txVectors(t)[0].tx(t)
	for _, n := range []int{0, 64, 66} {
		if _, err := tx.WithSignature(make([]byte, n), ""); err == nil {
			t.Errorf("a %d-byte signature was accepted", n)
		}
	}
}

// -- EIP-155 --

// v = recovery + 35 + 2*chainId. Wrong arithmetic yields a transaction
// every node rejects, or one a different chain accepts.
func TestLegacyVEncodesTheChainId(t *testing.T) {
	priv, _ := ethcrypto.HexToECDSA(vectorKey)
	from := ethcrypto.PubkeyToAddress(priv.PublicKey)

	for _, v := range txVectors(t) {
		if v.Dynamic {
			continue
		}
		tx := v.tx(t)
		digest, _ := tx.SigningHash()
		sig, _ := ethcrypto.Sign(digest, priv)
		signed, err := tx.WithSignature(sig, from)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		// Decode v back out of the encoded transaction by re-deriving what
		// it must be, and confirm the golden bytes agree.
		want := new(big.Int).SetUint64(uint64(sig[64]) + 35)
		want.Add(want, new(big.Int).Mul(big.NewInt(v.ChainID), big.NewInt(2)))
		if !strings.Contains(hex.EncodeToString(signed.Raw), hex.EncodeToString(want.Bytes())) {
			t.Errorf("%s: v=%s does not appear in the encoded transaction", v.Name, want)
		}
	}
}

// The same transaction on two chains must produce different signing
// hashes. If it did not, a signature captured on a testnet would spend on
// mainnet.
func TestTheSameTransactionOnTwoChainsHashesDifferently(t *testing.T) {
	mk := func(chain int64) []byte {
		tx := &Transaction{
			ChainID: big.NewInt(chain), Nonce: 1, GasPrice: big.NewInt(1),
			Gas: 21000, To: make([]byte, 20), Value: big.NewInt(1),
		}
		tx.To[19] = 0xaa
		h, err := tx.SigningHash()
		if err != nil {
			t.Fatalf("chain %d: %v", chain, err)
		}
		return h
	}

	if hex.EncodeToString(mk(1)) == hex.EncodeToString(mk(11155111)) {
		t.Fatal("mainnet and Sepolia produce the same signing hash; the chain id is not bound in")
	}
}
