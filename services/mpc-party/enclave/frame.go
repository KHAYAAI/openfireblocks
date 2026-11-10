// Package enclave runs a signing party inside an AWS Nitro Enclave.
//
// The enclave has no network card and no disk. Its only link to the outside
// is vsock to the parent instance. This package provides:
//
//   - vsock listeners and dialers, and an egress map that sends each
//     outbound connection the party makes to a fixed vsock port, where the
//     host relays it to one fixed destination (egress.go, vsock_linux.go);
//   - an attested TLS identity: a key generated inside the enclave, in a
//     self-signed certificate that carries an attestation document binding
//     that key to the enclave image (identity.go, tls.go). Peers verify the
//     document, so TLS ends inside the enclave and the host relays only
//     ciphertext;
//   - envelope encryption of key shares with AWS KMS, where KMS releases the
//     data key only to an attested enclave image (kms.go);
//   - forwarding of the parent instance's IAM role credentials into the
//     enclave, which cannot reach the instance metadata service (creds.go).
//
// The host side of all of this is cmd/mpc-party-host.
package enclave

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame types on the host-enclave control channel.
const (
	FramePing                byte = 1
	FramePong                byte = 2
	FrameCredentialsRequest  byte = 3
	FrameCredentialsResponse byte = 4
	FrameError               byte = 5
	FrameConfigRequest       byte = 6
	FrameConfigResponse      byte = 7
	// FrameReport carries a JSON status report from the enclave to the host
	// log. A production enclave has no console, so this is the only way an
	// operator learns why one did not start.
	FrameReport byte = 8
)

// MaxFrame is the largest payload a frame may carry.
const MaxFrame = 16 << 20

var knownFrames = map[byte]bool{
	FramePing: true, FramePong: true, FrameCredentialsRequest: true,
	FrameCredentialsResponse: true, FrameError: true,
	FrameConfigRequest: true, FrameConfigResponse: true, FrameReport: true,
}

// ErrFrameTooLarge is returned for a frame over MaxFrame, in either direction.
var ErrFrameTooLarge = errors.New("enclave: frame exceeds the 16 MiB limit")

// WriteFrame writes a 4-byte big-endian length, a 1-byte type and the payload.
// The length covers the type byte and the payload.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if !knownFrames[typ] {
		return fmt.Errorf("enclave: unknown frame type %d", typ)
	}
	if len(payload) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(1+len(payload)))
	buf[4] = typ
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame. It refuses an oversize length before
// allocating, so a hostile peer cannot make the reader allocate 4 GiB.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:4]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n == 0 {
		return 0, nil, errors.New("enclave: empty frame")
	}
	if n-1 > MaxFrame {
		return 0, nil, ErrFrameTooLarge
	}
	if _, err := io.ReadFull(r, hdr[4:5]); err != nil {
		return 0, nil, fmt.Errorf("enclave: truncated frame: %w", err)
	}
	if !knownFrames[hdr[4]] {
		return 0, nil, fmt.Errorf("enclave: unknown frame type %d", hdr[4])
	}
	payload := make([]byte, n-1)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("enclave: truncated frame: %w", err)
	}
	return hdr[4], payload, nil
}
