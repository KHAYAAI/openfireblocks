// Verifies an Ed25519 signature, for the local recovery drill.
//
// Its own tiny module, and it uses crypto/ed25519 from the standard
// library rather than anything hand-rolled. That is the point of it: the
// drill's final assertion is the only step that proves a recovery
// recovered the key, and an assertion implemented by the same person who
// wrote the thing under test is worth less than one that is not. A
// hand-written verifier in the drill script was rejecting roughly one
// good signature in five before this replaced it.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verify <public-key-hex> <message-hex> <signature-hex>")
		os.Exit(2)
	}
	pub, err := hex.DecodeString(os.Args[1])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		fmt.Fprintf(os.Stderr, "public key must be %d hex-encoded bytes\n", ed25519.PublicKeySize)
		os.Exit(2)
	}
	msg, err := hex.DecodeString(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "message is not hex:", err)
		os.Exit(2)
	}
	sig, err := hex.DecodeString(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, "signature is not hex:", err)
		os.Exit(2)
	}
	// Reported rather than rejected outright: a short signature is a real
	// finding about the signing path, and the drill should say which
	// failure it hit.
	if len(sig) != ed25519.SignatureSize {
		fmt.Printf("INVALID (signature is %d bytes, want %d)\n", len(sig), ed25519.SignatureSize)
		return
	}
	if ed25519.Verify(pub, msg, sig) {
		fmt.Println("VALID")
		return
	}
	fmt.Println("INVALID")
}
