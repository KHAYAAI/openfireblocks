//go:build !nitro

package enclave

import "errors"

// NewAttester always fails in a binary built without the nitro tag. Enclave
// mode then refuses to start: there is no software fallback.
func NewAttester() (Attester, error) {
	return nil, errors.New("this binary was built without the nitro tag, so it cannot attest; " +
		"build the enclave image with infrastructure/nitro/Dockerfile.enclave")
}

// BuiltForNitro reports whether this binary can attest.
const BuiltForNitro = false
