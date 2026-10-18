package chains

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"google.golang.org/protobuf/encoding/protowire"
)

// decodeFields parses one protobuf message into field -> repeated raw values
// (bytes for length-delimited, uint64 for varints), the way a node reads it.
// It is deliberately independent of the encoder in cosmos_tx.go: nothing here
// is shared with it but protowire's primitives.
func decodeFields(t *testing.T, b []byte) map[protowire.Number][]interface{} {
	t.Helper()
	out := map[protowire.Number][]interface{}{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		switch typ {
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				t.Fatal("bad bytes")
			}
			out[num] = append(out[num], v)
			b = b[m:]
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				t.Fatal("bad varint")
			}
			out[num] = append(out[num], v)
			b = b[m:]
		default:
			t.Fatalf("unexpected wire type %d on field %d", typ, num)
		}
	}
	return out
}

func testSend(t *testing.T, pub []byte) CosmosSend {
	return CosmosSend{
		ChainID: "cosmoshub-4", AccountNumber: 1234, Sequence: 7,
		From: "cosmos1w508d6qejxtdg4y5r3zarvary0c5xw7k6ah60c", To: "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu",
		Denom: "uatom", Amount: "1000000", FeeDenom: "uatom", FeeAmount: "5000", GasLimit: 200000,
		Memo: "invoice 42", PubKey: pub,
	}
}

func genKey(t *testing.T) (*btcec.PrivateKey, []byte) {
	t.Helper()
	k, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k, k.PubKey().SerializeUncompressed() // what a DKG stores: 65 bytes
}

func TestPlanEncodesTheFieldsANodeReads(t *testing.T) {
	_, pub := genKey(t)
	plan, err := PlanCosmosSend(testSend(t, pub))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := hex.DecodeString(plan.BodyBytesHex)
	auth, _ := hex.DecodeString(plan.AuthInfoBytesHex)

	b := decodeFields(t, body)
	any := decodeFields(t, b[1][0].([]byte))
	if string(any[1][0].([]byte)) != "/cosmos.bank.v1beta1.MsgSend" {
		t.Fatalf("type url = %s", any[1][0])
	}
	msg := decodeFields(t, any[2][0].([]byte))
	coin := decodeFields(t, msg[3][0].([]byte))
	if string(msg[1][0].([]byte)) != testSend(t, nil).From || string(msg[2][0].([]byte)) != testSend(t, nil).To ||
		string(coin[1][0].([]byte)) != "uatom" || string(coin[2][0].([]byte)) != "1000000" {
		t.Fatalf("MsgSend decoded wrong: %v %v", msg, coin)
	}
	if string(b[2][0].([]byte)) != "invoice 42" {
		t.Fatalf("memo = %s", b[2][0])
	}

	a := decodeFields(t, auth)
	signer := decodeFields(t, a[1][0].([]byte))
	pubAny := decodeFields(t, signer[1][0].([]byte))
	if string(pubAny[1][0].([]byte)) != "/cosmos.crypto.secp256k1.PubKey" {
		t.Fatalf("pubkey type = %s", pubAny[1][0])
	}
	if key := decodeFields(t, pubAny[2][0].([]byte))[1][0].([]byte); len(key) != 33 {
		t.Fatalf("the signer key must be the 33-byte compressed form, got %d bytes", len(key))
	}
	mode := decodeFields(t, decodeFields(t, signer[2][0].([]byte))[1][0].([]byte))
	if mode[1][0].(uint64) != 1 {
		t.Fatalf("sign mode = %v, want SIGN_MODE_DIRECT (1)", mode[1][0])
	}
	if signer[3][0].(uint64) != 7 {
		t.Fatalf("sequence = %v", signer[3][0])
	}
	fee := decodeFields(t, a[2][0].([]byte))
	if fee[2][0].(uint64) != 200000 || string(decodeFields(t, fee[1][0].([]byte))[2][0].([]byte)) != "5000" {
		t.Fatalf("fee decoded wrong: %v", fee)
	}
}

// Proto3 omits zero values and the node rebuilds the SignDoc the same way.
// Writing account_number=0 or sequence=0 explicitly would sign bytes the node
// never sees.
func TestZeroAccountNumberAndSequenceAreOmittedNotWritten(t *testing.T) {
	_, pub := genKey(t)
	s := testSend(t, pub)
	s.AccountNumber, s.Sequence = 0, 0
	plan, _ := PlanCosmosSend(s)
	auth, _ := hex.DecodeString(plan.AuthInfoBytesHex)
	signer := decodeFields(t, decodeFields(t, auth)[1][0].([]byte))
	if _, present := signer[3]; present {
		t.Fatal("a zero sequence was written to the wire")
	}
	doc := cosmosSignDocFor(plan)
	if _, present := decodeFields(t, doc)[4]; present {
		t.Fatal("a zero account_number was written into the SignDoc")
	}
}

func cosmosSignDocFor(plan *CosmosSigningPlan) []byte {
	body, _ := hex.DecodeString(plan.BodyBytesHex)
	auth, _ := hex.DecodeString(plan.AuthInfoBytesHex)
	doc := appendBytesField(nil, 1, body)
	doc = appendBytesField(doc, 2, auth)
	doc = appendStringField(doc, 3, plan.ChainID)
	return appendVarintField(doc, 4, plan.AccountNumber)
}

// signAsNode plays the node: parse a broadcast TxRaw, take the signer key from
// its own AuthInfo, rebuild the SignDoc from the bytes it received, and verify.
func nodeAccepts(t *testing.T, raw []byte, chainID string, accountNumber uint64) bool {
	t.Helper()
	tx := decodeFields(t, raw)
	body, auth := tx[1][0].([]byte), tx[2][0].([]byte)
	sig := tx[3][0].([]byte)
	if len(sig) != 64 {
		return false
	}
	signer := decodeFields(t, decodeFields(t, auth)[1][0].([]byte))
	key := decodeFields(t, decodeFields(t, signer[1][0].([]byte))[2][0].([]byte))[1][0].([]byte)
	pub, err := btcec.ParsePubKey(key)
	if err != nil {
		return false
	}
	doc := appendBytesField(nil, 1, body)
	doc = appendBytesField(doc, 2, auth)
	doc = appendStringField(doc, 3, chainID)
	doc = appendVarintField(doc, 4, accountNumber)
	digest := sha256.Sum256(doc)
	var r, s btcec.ModNScalar
	r.SetByteSlice(sig[:32])
	s.SetByteSlice(sig[32:])
	// The SDK refuses a high-S signature as malleable.
	if s.IsOverHalfOrder() {
		return false
	}
	return btcecdsa.NewSignature(&r, &s).Verify(digest[:], pub)
}

func cosmosTestSign(t *testing.T, k *btcec.PrivateKey, plan *CosmosSigningPlan) (r, s []byte) {
	t.Helper()
	d, _ := hex.DecodeString(plan.DigestHex)
	compact := btcecdsa.SignCompact(k, d, true) // header || r || s
	return compact[1:33], compact[33:65]
}

func TestAnAssembledTransactionIsOneANodeWouldAccept(t *testing.T) {
	k, pub := genKey(t)
	plan, err := PlanCosmosSend(testSend(t, pub))
	if err != nil {
		t.Fatal(err)
	}
	r, s := cosmosTestSign(t, k, plan)
	tx, err := AssembleCosmosTx(plan, r, s, pub)
	if err != nil {
		t.Fatal(err)
	}
	if !nodeAccepts(t, tx.Raw, "cosmoshub-4", 1234) {
		t.Fatal("a node would reject the assembled transaction")
	}
	if nodeAccepts(t, tx.Raw, "osmosis-1", 1234) || nodeAccepts(t, tx.Raw, "cosmoshub-4", 1235) {
		t.Fatal("the signature verified under a different chain id or account number; it is not bound to them")
	}
	sum := sha256.Sum256(tx.Raw)
	if tx.Hash != fmt.Sprintf("%X", sum) {
		t.Fatalf("hash %s is not the upper-case SHA-256 of the raw transaction", tx.Hash)
	}
}

// A threshold ceremony returns either S or its twin n-S with equal
// probability. The SDK accepts only the low one.
func TestAHighSSignatureIsNormalisedBeforeItIsRelayed(t *testing.T) {
	k, pub := genKey(t)
	plan, _ := PlanCosmosSend(testSend(t, pub))
	r, s := cosmosTestSign(t, k, plan)
	var ss btcec.ModNScalar
	ss.SetByteSlice(s)
	if !ss.IsOverHalfOrder() {
		ss.Negate() // force the high twin
	}
	hi := ss.Bytes()
	tx, err := AssembleCosmosTx(plan, r, hi[:], pub)
	if err != nil {
		t.Fatal(err)
	}
	if !nodeAccepts(t, tx.Raw, "cosmoshub-4", 1234) {
		t.Fatal("a high-S signature was relayed as-is; the node would reject it")
	}
}

func TestASignatureThatIsNotTheSignersIsRefused(t *testing.T) {
	_, pub := genKey(t)
	other, _ := genKey(t)
	plan, _ := PlanCosmosSend(testSend(t, pub))
	r, s := cosmosTestSign(t, other, plan)
	if _, err := AssembleCosmosTx(plan, r, s, pub); err == nil {
		t.Fatal("a signature from a different key was accepted")
	}
	// A plan whose bytes were altered after signing no longer matches.
	k, pub2 := genKey(t)
	plan2, _ := PlanCosmosSend(testSend(t, pub2))
	r2, s2 := cosmosTestSign(t, k, plan2)
	plan2.ChainID = "other-1"
	if _, err := AssembleCosmosTx(plan2, r2, s2, pub2); err == nil {
		t.Fatal("a plan altered after signing was accepted")
	}
	if _, err := AssembleCosmosTx(plan2, r2[:5], s2, pub2); err == nil {
		t.Fatal("a short r was accepted")
	}
}

func TestPlanRefusesWhatCannotBeASend(t *testing.T) {
	_, pub := genKey(t)
	mutate := map[string]func(*CosmosSend){
		"no chain id":   func(s *CosmosSend) { s.ChainID = "" },
		"self send":     func(s *CosmosSend) { s.To = s.From },
		"zero amount":   func(s *CosmosSend) { s.Amount = "0" },
		"negative":      func(s *CosmosSend) { s.Amount = "-1" },
		"decimal":       func(s *CosmosSend) { s.Amount = "1.5" },
		"leading zeros": func(s *CosmosSend) { s.Amount = "0100" },
		"zero fee":      func(s *CosmosSend) { s.FeeAmount = "0" },
		"no gas":        func(s *CosmosSend) { s.GasLimit = 0 },
		"bad key":       func(s *CosmosSend) { s.PubKey = []byte{1, 2, 3} },
		"no fee denom":  func(s *CosmosSend) { s.FeeDenom = "" },
	}
	for name, m := range mutate {
		s := testSend(t, pub)
		m(&s)
		if _, err := PlanCosmosSend(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFeeRoundsUp(t *testing.T) {
	n := &CosmosNode{GasPrice: "0.025", GasLimit: 200000}
	if f, err := n.Fee(); err != nil || f != "5000" {
		t.Fatalf("fee = %s %v", f, err)
	}
	n = &CosmosNode{GasPrice: "0.0251", GasLimit: 1001}
	if f, _ := n.Fee(); f != "26" { // 25.1251 -> 26
		t.Fatalf("fee = %s, want 26", f)
	}
	if _, err := (&CosmosNode{GasPrice: "abc", GasLimit: 1}).Fee(); err == nil {
		t.Fatal("a non-numeric gas price was accepted")
	}
}
