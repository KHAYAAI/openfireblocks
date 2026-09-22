// Recovers the signer's Ethereum address from a message hash and a 65-byte
// compact [R||S||V] signature.
//
// Used by ../smoke-test.sh as the final assertion. It is the only step that
// actually proves anything: a DKG ceremony can report success, three shares
// can appear in Vault, and a signing workflow can return a well-formed
// signature, all while the pieces belong to different keys. Recovering the
// public key from the signature and hashing it to an address is what ties
// the signature back to the address the ceremony derived.
//
// Its own module rather than a package inside a service, so the smoke test
// does not depend on any service's build.
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: recover <message-hash-hex> <signature-hex>")
		os.Exit(2)
	}

	msg, err := hex.DecodeString(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "message hash is not hex:", err)
		os.Exit(2)
	}
	sig, err := hex.DecodeString(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "signature is not hex:", err)
		os.Exit(2)
	}

	// SigToPub wants V as 0/1 in the last byte, which is what the signing
	// workflow emits.
	pub, err := crypto.SigToPub(msg, sig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "recovery failed:", err)
		os.Exit(1)
	}
	fmt.Println(crypto.PubkeyToAddress(*pub).Hex())
}
