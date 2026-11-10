package enclave

// Attester asks the Nitro Secure Module for an attestation document. The
// document is signed by the hypervisor and binds the enclave's measurements
// (PCRs) to the user data, nonce and public key passed in.
//
// The production implementation (NSMAttester) exists only in binaries built
// with the nitro tag. A software attester lives in
// nitroattest/nitroattesttest, which refuses to compile into such a binary.
type Attester interface {
	Attest(userData, nonce, publicKey []byte) ([]byte, error)
}
