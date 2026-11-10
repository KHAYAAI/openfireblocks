package enclave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// RuntimeKeys are the settings the host may supply at start-up. They decide
// where the party connects and which party number it claims, and none of them
// can expose a share on its own:
//
//   - a wrong PARTY_ID is caught by the peers, which check the claimed party
//     against the IAM role (PCR3) pinned for it, and by KMS, whose per-party
//     key the host's role cannot use for another party;
//   - a wrong address only sends TLS to a server that must still attest as
//     the party it was dialled as;
//   - Vault holds only KMS envelopes, so a hostile Vault learns nothing.
//
// Everything that does protect a share (the KMS key for each party, the
// IAM-role pins, the ceremony co-signer's public key, the orchestrator CA) is
// baked into the image, where it is covered by the measurement, and the host
// is refused if it tries to send it.
var RuntimeKeys = map[string]bool{
	"PARTY_ID":             true,
	"ENCLAVE_EGRESS":       true,
	"VAULT_ADDR":           true,
	"VAULT_TOKEN":          true,
	"VAULT_KV_MOUNT":       true,
	"VAULT_KEY_SHARE_PATH": true,
	"VAULT_CACERT_PEM":     true,
	"TSS_PREPARAMS_POOL":   true,
}

// ParseRuntimeConfig decodes and checks a config payload from the host.
func ParseRuntimeConfig(body []byte) (map[string]string, error) {
	var cfg map[string]string
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, errors.New("enclave config: host sent malformed configuration")
	}
	var refused []string
	for k := range cfg {
		if !RuntimeKeys[k] {
			refused = append(refused, k)
		}
	}
	if len(refused) > 0 {
		sort.Strings(refused)
		return nil, fmt.Errorf("enclave config: the host may not set %s; these are fixed in the measured image",
			strings.Join(refused, ", "))
	}
	return cfg, nil
}

// FetchRuntimeConfig asks the host for the runtime settings.
func FetchRuntimeConfig(ctx context.Context, dial func(context.Context) (net.Conn, error)) (map[string]string, error) {
	conn, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("enclave config: reaching the host: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := WriteFrame(conn, FrameConfigRequest, nil); err != nil {
		return nil, err
	}
	typ, body, err := ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("enclave config: %w", err)
	}
	if typ == FrameError {
		return nil, fmt.Errorf("enclave config: host: %s", body)
	}
	if typ != FrameConfigResponse {
		return nil, fmt.Errorf("enclave config: unexpected frame type %d", typ)
	}
	return ParseRuntimeConfig(body)
}

// Control is the host's side of the control channel.
type Control struct {
	// Credentials supplies the instance role's credentials. Nil refuses
	// credential requests.
	Credentials aws.CredentialsProvider
	// Config is sent in answer to a config request. Nil refuses them.
	Config map[string]string
	Logf   func(string, ...any)
	// Reports, if set, receives each report the enclave sends.
	Reports func(body []byte)
}

// Serve answers ping, config and credential requests on ln until ctx ends.
func (c *Control) Serve(ctx context.Context, ln net.Listener) error {
	if c.Config != nil {
		raw, _ := json.Marshal(c.Config)
		if _, err := ParseRuntimeConfig(raw); err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go c.handle(ctx, conn)
	}
}

func (c *Control) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Control) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	typ, reportBody, err := ReadFrame(conn)
	if err != nil {
		_ = WriteFrame(conn, FrameError, []byte("bad request frame"))
		return
	}
	switch typ {
	case FramePing:
		_ = WriteFrame(conn, FramePong, nil)
	case FrameReport:
		c.logf("enclave report: %s", reportBody)
		if c.Reports != nil {
			c.Reports(reportBody)
		}
		_ = WriteFrame(conn, FramePong, nil)
	case FrameConfigRequest:
		if c.Config == nil {
			_ = WriteFrame(conn, FrameError, []byte("no configuration on this host"))
			return
		}
		body, _ := json.Marshal(c.Config)
		_ = WriteFrame(conn, FrameConfigResponse, body)
	case FrameCredentialsRequest:
		if c.Credentials == nil {
			_ = WriteFrame(conn, FrameError, []byte("no credentials on this host"))
			return
		}
		creds, err := c.Credentials.Retrieve(ctx)
		if err != nil {
			c.logf("credentials for the enclave: %v", err)
			_ = WriteFrame(conn, FrameError, []byte("the host could not obtain credentials"))
			return
		}
		p := credentialsPayload{AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey, SessionToken: creds.SessionToken}
		if creds.CanExpire {
			p.Expires = creds.Expires
		}
		body, _ := json.Marshal(p)
		_ = WriteFrame(conn, FrameCredentialsResponse, body)
	default:
		_ = WriteFrame(conn, FrameError, []byte("unsupported request"))
	}
}

// Ping checks that the other end of the control channel answers.
func Ping(ctx context.Context, dial func(context.Context) (net.Conn, error)) error {
	conn, err := dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := WriteFrame(conn, FramePing, nil); err != nil {
		return err
	}
	typ, _, err := ReadFrame(conn)
	if err != nil {
		return err
	}
	if typ != FramePong {
		return fmt.Errorf("enclave: expected pong, got frame type %d", typ)
	}
	return nil
}

// Report is what an enclave tells the host about itself.
type Report struct {
	Event    string `json:"event"` // started, failed, selftest
	OK       bool   `json:"ok"`
	Party    int    `json:"party,omitempty"`
	ImagePCR string `json:"image_pcr0,omitempty"`
	// RolePCR string is PCR3, the parent instance's IAM role measurement:
	// the value to pin for this party in ENCLAVE_PARTY_PCR3.
	RolePCR string `json:"role_pcr3,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// EncryptedKey is the KMS ciphertext of the self-test data key (not
	// secret), so the spike can show KMS refusing it to the host.
	EncryptedKey []byte `json:"encrypted_key,omitempty"`
}

// SendReport delivers a report to the host's control channel. Best effort.
func SendReport(ctx context.Context, dial func(context.Context) (net.Conn, error), r Report) error {
	conn, err := dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body, _ := json.Marshal(r)
	if err := WriteFrame(conn, FrameReport, body); err != nil {
		return err
	}
	_, _, err = ReadFrame(conn)
	return err
}
