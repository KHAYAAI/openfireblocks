package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil"

	"forge-crypto/mpc-signer/chains"
)

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		die("usage: btctool <address|plan|assemble> [args]")
	}
	switch os.Args[1] {
	case "address":
		cmdAddress()
	case "plan":
		cmdPlan()
	case "assemble":
		cmdAssemble()
	default:
		die("unknown command %q", os.Args[1])
	}
}

// address derives a regtest address for a secp256k1 public key -- the same
// key the DKG produced, read through Bitcoin's address rules instead of
// Ethereum's.
//
// Both output types come from the same 20-byte hash of the same compressed
// key; only the encoding differs. That is worth stating explicitly because
// it is the reason a segwit address needs no new key material, no second
// DKG, and no change to the ceremony -- only a different sighash on the way
// out.
func cmdAddress() {
	if len(os.Args) < 3 {
		die("usage: btctool address <compressed-pubkey-hex> [p2pkh|p2wpkh]")
	}
	raw, err := hex.DecodeString(stripHex(os.Args[2]))
	if err != nil {
		die("invalid public key hex: %v", err)
	}
	pub, err := btcec.ParsePubKey(raw, btcec.S256())
	if err != nil {
		die("invalid public key: %v", err)
	}
	hash := btcutil.Hash160(pub.SerializeCompressed())

	kind := "p2pkh"
	if len(os.Args) > 3 && os.Args[3] != "" {
		kind = os.Args[3]
	}

	var addr btcutil.Address
	switch kind {
	case "p2pkh":
		addr, err = btcutil.NewAddressPubKeyHash(hash, &chaincfg.RegressionNetParams)
	case "p2wpkh":
		addr, err = btcutil.NewAddressWitnessPubKeyHash(hash, &chaincfg.RegressionNetParams)
	default:
		die("unknown address type %q (want p2pkh or p2wpkh)", kind)
	}
	if err != nil {
		die("address: %v", err)
	}
	fmt.Println(addr.EncodeAddress())
}

func cmdPlan() {
	var req chains.BitcoinSigningRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		die("reading the request: %v", err)
	}
	plan, err := chains.PlanBitcoinTransaction(&req)
	if err != nil {
		die("plan: %v", err)
	}
	json.NewEncoder(os.Stdout).Encode(plan)
}

type assembleInput struct {
	Plan      *chains.BitcoinSigningPlan     `json:"plan"`
	Sigs      []chains.BitcoinInputSignature `json:"signatures"`
	PubKeyHex string                         `json:"pubkey_hex"`
}

func cmdAssemble() {
	var in assembleInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		die("reading the request: %v", err)
	}
	raw, err := chains.AssembleBitcoinTransaction(in.Plan, in.Sigs, in.PubKeyHex)
	if err != nil {
		die("assemble: %v", err)
	}
	txid, err := chains.BitcoinTxID(raw)
	if err != nil {
		die("txid: %v", err)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]string{
		"raw_tx_hex": hex.EncodeToString(raw),
		"txid":       txid,
	})
}

func stripHex(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
