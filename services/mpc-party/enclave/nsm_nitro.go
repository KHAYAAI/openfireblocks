//go:build nitro

package enclave

import (
	"errors"
	"fmt"
	"sync"

	"github.com/hf/nsm"
	"github.com/hf/nsm/request"
	"github.com/hf/nsm/response"
)

// NSMAttester talks to /dev/nsm inside a Nitro Enclave.
type NSMAttester struct {
	mu   sync.Mutex
	sess *nsm.Session
}

// NewAttester opens the Nitro Secure Module. It fails outside an enclave.
func NewAttester() (Attester, error) {
	sess, err := nsm.OpenDefaultSession()
	if err != nil {
		return nil, fmt.Errorf("opening the Nitro Secure Module (/dev/nsm): %w", err)
	}
	return &NSMAttester{sess: sess}, nil
}

// Attest implements Attester.
func (a *NSMAttester) Attest(userData, nonce, publicKey []byte) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	res, err := a.sess.Send(&request.Attestation{UserData: userData, Nonce: nonce, PublicKey: publicKey})
	if err != nil {
		return nil, fmt.Errorf("NSM attestation request: %w", err)
	}
	if res.Error != "" && res.Error != response.ECSuccess {
		return nil, fmt.Errorf("NSM refused the attestation request: %s", res.Error)
	}
	if res.Attestation == nil || len(res.Attestation.Document) == 0 {
		return nil, errors.New("NSM returned no attestation document")
	}
	return res.Attestation.Document, nil
}

// BuiltForNitro reports whether this binary can attest.
const BuiltForNitro = true
