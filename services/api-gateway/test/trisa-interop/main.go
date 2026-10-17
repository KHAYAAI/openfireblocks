// A thin command-line wrapper around TRISA's own reference implementation
// (github.com/trisacrypto/trisa), used by the gateway's tests to prove that what the
// gateway seals, TRISA's code opens, and the other way round. It is a test tool, not
// part of the shipped gateway.
//
//	go run . unseal <private-key.pem>            envelope on stdin  -> JSON payload on stdout
//	go run . seal <public-key.pem> <envelope-id> payload JSON on stdin -> envelope on stdout
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/trisacrypto/trisa/pkg/ivms101"
	api "github.com/trisacrypto/trisa/pkg/trisa/api/v1beta1"
	generic "github.com/trisacrypto/trisa/pkg/trisa/data/generic/v1beta1"
	"github.com/trisacrypto/trisa/pkg/trisa/envelope"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func readPEM(path string) any {
	raw, err := os.ReadFile(path)
	if err != nil {
		die("read %s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		die("no PEM in %s", path)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return k
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return k
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k
	}
	die("unrecognised key in %s", path)
	return nil
}

type payloadJSON struct {
	Identity    json.RawMessage `json:"identity"`
	Transaction json.RawMessage `json:"transaction"`
	SentAt      string          `json:"sentAt"`
}

func main() {
	if len(os.Args) < 3 {
		die("usage: unseal <key.pem> | seal <pub.pem> <id>")
	}
	in, _ := io.ReadAll(os.Stdin)
	switch os.Args[1] {
	case "unseal":
		priv, ok := readPEM(os.Args[2]).(*rsa.PrivateKey)
		if !ok {
			die("not an RSA private key")
		}
		msg := &api.SecureEnvelope{}
		if err := proto.Unmarshal(in, msg); err != nil {
			die("decode envelope: %v", err)
		}
		env, reject, err := envelope.Open(msg, envelope.WithRSAPrivateKey(priv))
		if err != nil || reject != nil {
			die("open: %v / %v", err, reject)
		}
		payload, err := env.Payload()
		if err != nil {
			die("payload: %v", err)
		}
		identity := &ivms101.IdentityPayload{}
		if err := payload.Identity.UnmarshalTo(identity); err != nil {
			die("identity: %v", err)
		}
		if err := identity.Validate(); err != nil {
			die("the IVMS101 identity does not validate under the reference rules: %v", err)
		}
		tx := &generic.Transaction{}
		if err := payload.Transaction.UnmarshalTo(tx); err != nil {
			die("transaction: %v", err)
		}
		idJSON, _ := protojson.Marshal(identity)
		txJSON, _ := protojson.Marshal(tx)
		out, _ := json.Marshal(map[string]any{
			"envelopeId":    env.ID(),
			"transferState": env.TransferState().String(),
			"sentAt":        payload.SentAt,
			"identity":      json.RawMessage(idJSON),
			"transaction":   json.RawMessage(txJSON),
		})
		fmt.Println(string(out))
	case "seal":
		if len(os.Args) < 4 {
			die("seal needs an envelope id")
		}
		pub, ok := readPEM(os.Args[2]).(*rsa.PublicKey)
		if !ok {
			die("not an RSA public key")
		}
		var pj payloadJSON
		if err := json.Unmarshal(in, &pj); err != nil {
			die("payload json: %v", err)
		}
		identity := &ivms101.IdentityPayload{}
		if err := protojson.Unmarshal(pj.Identity, identity); err != nil {
			die("identity json: %v", err)
		}
		tx := &generic.Transaction{}
		if err := protojson.Unmarshal(pj.Transaction, tx); err != nil {
			die("transaction json: %v", err)
		}
		payload := &api.Payload{SentAt: time.Now().UTC().Format(time.RFC3339)}
		var err error
		if payload.Identity, err = anypb.New(identity); err != nil {
			die("%v", err)
		}
		if payload.Transaction, err = anypb.New(tx); err != nil {
			die("%v", err)
		}
		msg, reject, err := envelope.SealPayload(payload,
			envelope.WithEnvelopeID(os.Args[3]),
			envelope.WithTransferState(api.TransferStarted),
			envelope.WithRSAPublicKey(pub))
		if err != nil || reject != nil {
			die("seal: %v / %v", err, reject)
		}
		raw, _ := proto.Marshal(msg)
		os.Stdout.Write(raw)
	default:
		die("unknown command %q", os.Args[1])
	}
}
