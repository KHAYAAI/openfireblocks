// Produces a ceremony authorisation, for the drills that call a party
// directly.
//
// With ceremonyAuthorizer enabled, a party refuses any ceremony request
// that does not carry a signature from the authorising key. The temporal
// worker does this for every ceremony the platform starts
// (services/temporal-worker/activities/ceremony_authorization.go), so the
// drills that go through the API gateway need nothing. The recovery drill
// is the exception: it calls POST /tss/keygen/restore on each party
// directly, because restoring a key is an operator action and there is no
// gateway route for it.
//
// Its own tiny module so the drill does not depend on any service's
// build, matching ../recover and ../btctool.
//
// The canonical form is duplicated here, as it is in the worker, because
// these are separate Go modules that cannot import each other. It is
// pinned by golden vectors in services/mpc-party/authorizer_test.go and
// services/temporal-worker/activities/ceremony_authorization_test.go; if
// this copy drifts, the drill fails with a 403 that says the signature
// does not verify.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type authorizationRequest struct {
	Operation   string `json:"operation"`
	CeremonyID  string `json:"ceremony_id"`
	MessageHash string `json:"message_hash,omitempty"`
	IssuedAt    int64  `json:"issued_at"`
}

func canonical(r authorizationRequest) []byte {
	return []byte(strings.Join([]string{
		"openfireblocks-authorization-v1",
		r.Operation,
		r.CeremonyID,
		r.MessageHash,
		fmt.Sprintf("%d", r.IssuedAt),
	}, "\n"))
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr,
			"usage: authorize <ed25519-seed-or-key-hex> <operation> <ceremony-id> [message-hash-hex]")
		os.Exit(2)
	}
	keyHex, operation, ceremonyID := os.Args[1], os.Args[2], os.Args[3]
	messageHash := ""
	if len(os.Args) > 4 {
		messageHash = os.Args[4]
	}

	raw, err := hex.DecodeString(strings.TrimSpace(strings.TrimPrefix(keyHex, "0x")))
	if err != nil {
		fmt.Fprintln(os.Stderr, "the key is not hex:", err)
		os.Exit(2)
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	default:
		fmt.Fprintf(os.Stderr, "an ed25519 key is %d or %d bytes, got %d\n",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(raw))
		os.Exit(2)
	}

	req := authorizationRequest{
		Operation:   operation,
		CeremonyID:  ceremonyID,
		MessageHash: messageHash,
		IssuedAt:    time.Now().Unix(),
	}
	body, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Two lines: the authorisation JSON, then the signature. The caller
	// reads them with `read -r`, which is simpler to get right in shell
	// than parsing a JSON object out of a JSON object.
	fmt.Println(string(body))
	fmt.Println(hex.EncodeToString(ed25519.Sign(priv, canonical(req))))
}
