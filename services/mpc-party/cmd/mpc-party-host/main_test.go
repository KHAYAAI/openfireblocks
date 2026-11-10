package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"openfireblocks.com/services/mpc-party/enclave"
)

// fakeVsock maps vsock ports to loopback TCP listeners, so the host's relays
// can be exercised without AF_VSOCK.
type fakeVsock struct {
	mu    sync.Mutex
	ports map[uint32]string // host-side listeners
	enc   map[uint32]string // enclave-side listeners, by port
}

func (f *fakeVsock) listen(port uint32) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.ports[port] = ln.Addr().String()
	f.mu.Unlock()
	return ln, nil
}

func (f *fakeVsock) dialHost(ctx context.Context, port uint32) (net.Conn, error) {
	f.mu.Lock()
	addr, ok := f.ports[port]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("nothing on host vsock port %d", port)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
}

func (f *fakeVsock) dialEnclave(ctx context.Context, cid, port uint32) (net.Conn, error) {
	if cid != 16 {
		return nil, fmt.Errorf("no enclave with cid %d", cid)
	}
	f.mu.Lock()
	addr, ok := f.enc[port]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("enclave port %d closed", port)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
}

func echoServer(t *testing.T, prefix string) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				b, _ := io.ReadAll(c)
				_, _ = c.Write(append([]byte(prefix), b...))
				c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn, msg string) string {
	t.Helper()
	_, _ = c.Write([]byte(msg))
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	b, _ := io.ReadAll(c)
	return string(b)
}

type creds struct{}

func (creds) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "AKIAHOST", SecretAccessKey: "s"}, nil
}

func freePort(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestHostRelaysEverything(t *testing.T) {
	vs := &fakeVsock{ports: map[uint32]string{}, enc: map[uint32]string{}}
	vs.enc[5000] = echoServer(t, "enclave:")
	vault := echoServer(t, "vault:")
	listen, health := freePort(t), freePort(t)
	cfg := hostConfig{
		enclaveCID: 16, enclavePort: 5000, listen: listen, controlPort: 9000,
		egress:       map[uint32]string{7200: vault},
		runtime:      map[string]string{"PARTY_ID": "2"},
		healthListen: health,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := run(ctx, cfg, hostDeps{
			listenVsock: vs.listen, dialVsock: vs.dialEnclave,
			dialTCP: func(ctx context.Context, a string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", a)
			},
			creds: creds{}, logf: t.Logf,
		}); err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	waitFor(t, func() bool {
		c, err := net.Dial("tcp", listen)
		if err == nil {
			c.Close()
		}
		return err == nil
	})

	// Inbound TCP reaches the enclave.
	c, _ := net.Dial("tcp", listen)
	if got := roundTrip(t, c, "hello"); got != "enclave:hello" {
		t.Fatalf("inbound: %q", got)
	}
	// Enclave egress reaches the fixed destination.
	e, err := vs.dialHost(ctx, 7200)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, e, "kv"); got != "vault:kv" {
		t.Fatalf("egress: %q", got)
	}
	// An unlisted vsock port reaches nothing.
	if _, err := vs.dialHost(ctx, 7300); err == nil {
		t.Fatal("unlisted egress port open")
	}
	// Control: config and credentials.
	dialCtrl := func(ctx context.Context) (net.Conn, error) { return vs.dialHost(ctx, 9000) }
	rc, err := enclave.FetchRuntimeConfig(ctx, dialCtrl)
	if err != nil || rc["PARTY_ID"] != "2" {
		t.Fatalf("runtime config: %v %v", rc, err)
	}
	cr, err := enclave.HostCredentials{Dial: dialCtrl}.Retrieve(ctx)
	if err != nil || cr.AccessKeyID != "AKIAHOST" {
		t.Fatalf("credentials: %v %v", cr, err)
	}
	// Health follows the enclave.
	if resp, err := http.Get("http://" + health + "/health"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v", err)
	}
	vs.mu.Lock()
	delete(vs.enc, 5000)
	vs.mu.Unlock()
	if resp, err := http.Get("http://" + health + "/health"); err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("health with the enclave down: %v %v", resp, err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestHostConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "rt.json")
	_ = os.WriteFile(good, []byte(`{"PARTY_ID":"1","VAULT_ADDR":"https://vault.internal:8200"}`), 0o600)
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"PARTY_ID":"1","ENCLAVE_KMS_KEYS":"1=arn"}`), 0o600)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	base := map[string]string{"ENCLAVE_CID": "16", "HOST_EGRESS": "7200=vault.internal:8200", "HOST_RUNTIME_CONFIG": good}
	c, err := configFromEnv(env(base))
	if err != nil || c.enclaveCID != 16 || c.egress[7200] != "vault.internal:8200" || c.listen != ":7000" {
		t.Fatalf("%+v %v", c, err)
	}
	for name, mut := range map[string]func(m map[string]string){
		"no cid":                 func(m map[string]string) { delete(m, "ENCLAVE_CID") },
		"bad egress":             func(m map[string]string) { m["HOST_EGRESS"] = "x=y" },
		"egress on control":      func(m map[string]string) { m["HOST_EGRESS"] = "9000=a:1" },
		"no runtime config":      func(m map[string]string) { delete(m, "HOST_RUNTIME_CONFIG") },
		"measured key in config": func(m map[string]string) { m["HOST_RUNTIME_CONFIG"] = bad },
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		mut(m)
		if _, err := configFromEnv(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
