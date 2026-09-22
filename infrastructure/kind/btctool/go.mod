// btctool exposes the Bitcoin threshold-signing helpers to shell scripts.
//
// It deliberately contains no logic of its own. Everything it does lives in
// services/mpc-signer/chains, where it is unit tested against btcd's script
// interpreter, and is reached here through a replace directive -- so the
// drill exercises exactly the code the platform ships rather than a
// reimplementation that would drift from it.
module btctool

go 1.24

require (
	forge-crypto/mpc-signer v0.0.0
	github.com/btcsuite/btcd v0.0.0-20190629003639-c26ffa870fd8
	github.com/btcsuite/btcutil v0.0.0-20190425235716-9e5f4b9a998d
)

require (
	github.com/bits-and-blooms/bitset v1.13.0 // indirect
	github.com/btcsuite/btcd/btcec/v2 v2.3.4 // indirect
	github.com/btcsuite/btclog v1.0.0 // indirect
	github.com/consensys/bavard v0.1.13 // indirect
	github.com/consensys/gnark-crypto v0.12.1 // indirect
	github.com/crate-crypto/go-ipa v0.0.0-20240223125850-b1e8a79f509c // indirect
	github.com/crate-crypto/go-kzg-4844 v1.0.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/ethereum/c-kzg-4844 v1.0.0 // indirect
	github.com/ethereum/go-ethereum v1.14.11 // indirect
	github.com/ethereum/go-verkle v0.1.1-0.20240829091221-dffa7562dbe9 // indirect
	github.com/holiman/uint256 v1.3.1 // indirect
	github.com/mmcloughlin/addchain v0.4.0 // indirect
	github.com/supranational/blst v0.3.13 // indirect
	golang.org/x/crypto v0.23.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	rsc.io/tmplfunc v0.0.3 // indirect
)

replace forge-crypto/mpc-signer => ../../../services/mpc-signer
