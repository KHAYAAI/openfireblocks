// Command hsm-key creates and inspects the signing key on a PKCS#11 token.
//
//	hsm-key generate   create a secp256k1 key pair inside the token
//	hsm-key show       print the key's addresses
//
// Configured by the same HSM_PKCS11_* variables as the service, so what
// this tool creates is exactly what the service will open. The service
// itself never creates a key: a mistyped label would otherwise mint a new
// address on first start, and the first anyone heard of it would be a
// deposit sent to the old one.
//
// Built only into the PKCS#11 image (Dockerfile --target pkcs11):
//
//	kubectl exec deploy/<release>-mpc-signer -- hsm-key generate
package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"forge-crypto/mpc-signer/chains"
	"forge-crypto/mpc-signer/keys"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "generate" && os.Args[1] != "show") {
		fmt.Fprintln(os.Stderr, "usage: hsm-key generate|show   (configured by HSM_PKCS11_*)")
		os.Exit(2)
	}
	cfg, err := keys.PKCS11ConfigFromEnv()
	if err == nil && cfg == nil {
		err = fmt.Errorf("HSM_PKCS11_* is not set")
	}
	if err != nil {
		fail(err)
	}

	if os.Args[1] == "generate" {
		if err := keys.GeneratePKCS11Key(cfg); err != nil {
			fail(err)
		}
		fmt.Printf("created key %q; it was generated inside the token and cannot be exported\n\n", cfg.KeyLabel)
	}

	k, err := keys.OpenPKCS11(cfg)
	if err != nil {
		fail(err)
	}
	defer k.(io.Closer).Close()

	eth, err := keys.Address(k)
	if err != nil {
		fail(err)
	}
	pub, err := keys.CompressedPublicKey(k)
	if err != nil {
		fail(err)
	}
	fmt.Printf("key:                %s\n", k.Describe())
	fmt.Printf("public key:         %s\n", hex.EncodeToString(pub))
	fmt.Printf("ethereum address:   %s\n", eth)
	for _, network := range []string{"mainnet", "testnet"} {
		segwit, legacy, err := chains.BitcoinAddressesForPubKey(hex.EncodeToString(pub), network)
		if err != nil {
			fail(err)
		}
		fmt.Printf("bitcoin %-8s    %s (segwit), %s (legacy)\n", network+":", segwit, legacy)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hsm-key:", err)
	os.Exit(1)
}
