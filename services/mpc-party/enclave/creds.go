package enclave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// credentialsPayload is what the host sends in a FrameCredentialsResponse.
type credentialsPayload struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token,omitempty"`
	Expires         time.Time `json:"expires,omitempty"`
}

// HostCredentials fetches AWS credentials from the host over vsock.
//
// An enclave cannot reach the instance metadata service, so the host hands
// it the instance role's temporary credentials. They are the host's own
// credentials, so this gives the enclave nothing the host does not already
// have; what stops the host from using them to decrypt a share is the KMS
// key policy, which requires an attestation document from the measured
// image.
type HostCredentials struct {
	Dial func(ctx context.Context) (net.Conn, error)
}

// Retrieve implements aws.CredentialsProvider.
func (h HostCredentials) Retrieve(ctx context.Context) (aws.Credentials, error) {
	conn, err := h.Dial(ctx)
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("enclave credentials: reaching the host: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if err := WriteFrame(conn, FrameCredentialsRequest, nil); err != nil {
		return aws.Credentials{}, err
	}
	typ, body, err := ReadFrame(conn)
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("enclave credentials: %w", err)
	}
	switch typ {
	case FrameCredentialsResponse:
	case FrameError:
		return aws.Credentials{}, fmt.Errorf("enclave credentials: host: %s", body)
	default:
		return aws.Credentials{}, fmt.Errorf("enclave credentials: unexpected frame type %d", typ)
	}
	var p credentialsPayload
	if err := json.Unmarshal(body, &p); err != nil || p.AccessKeyID == "" || p.SecretAccessKey == "" {
		return aws.Credentials{}, errors.New("enclave credentials: host sent malformed credentials")
	}
	return aws.Credentials{
		AccessKeyID:     p.AccessKeyID,
		SecretAccessKey: p.SecretAccessKey,
		SessionToken:    p.SessionToken,
		Source:          "mpc-party-host",
		CanExpire:       !p.Expires.IsZero(),
		Expires:         p.Expires,
	}, nil
}

// ServeCredentials answers credential requests on ln with credentials from
// provider (on the host, the instance role), until ctx ends.
func ServeCredentials(ctx context.Context, ln net.Listener, provider aws.CredentialsProvider, logf func(string, ...any)) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(15 * time.Second))
			typ, _, err := ReadFrame(c)
			if err != nil || typ != FrameCredentialsRequest {
				_ = WriteFrame(c, FrameError, []byte("expected a credentials request"))
				return
			}
			creds, err := provider.Retrieve(ctx)
			if err != nil {
				if logf != nil {
					logf("credentials for the enclave: %v", err)
				}
				_ = WriteFrame(c, FrameError, []byte("the host could not obtain credentials"))
				return
			}
			p := credentialsPayload{AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey, SessionToken: creds.SessionToken}
			if creds.CanExpire {
				p.Expires = creds.Expires
			}
			body, _ := json.Marshal(p)
			_ = WriteFrame(c, FrameCredentialsResponse, body)
		}()
	}
}
