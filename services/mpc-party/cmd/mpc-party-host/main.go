// Command mpc-party-host runs on the parent EC2 instance of a signing-party
// enclave. It is deliberately dumb: it moves bytes and hands over settings
// that cannot expose a share, and it never sees a share, a TLS key or a
// data key.
//
// It does four things:
//
//  1. Inbound: accepts TCP on HOST_LISTEN (default :7000), where peers and the
//     orchestrator connect, and relays each connection to the enclave's vsock
//     port. TLS ends inside the enclave.
//  2. Egress: for each entry in HOST_EGRESS, listens on a vsock port and
//     relays each connection to one fixed TCP destination (a peer's host,
//     Vault, the regional KMS endpoint). The enclave's matching
//     ENCLAVE_EGRESS entry decides which destination name maps to which port.
//  3. Control: on HOST_CONTROL_PORT (vsock, default 9000), answers the
//     enclave's request for its runtime settings (HOST_RUNTIME_CONFIG, a JSON
//     file) and for the instance role's AWS credentials, which the enclave
//     cannot fetch itself.
//  4. Health: HOST_HEALTH_LISTEN (default :8081) answers GET /health with 200
//     when the enclave's vsock port accepts a connection.
//
// Configuration (environment):
//
//	ENCLAVE_CID          the enclave's vsock context id (nitro-cli describe-enclaves)
//	ENCLAVE_VSOCK_PORT   default 5000, must match the image
//	HOST_LISTEN          default :7000
//	HOST_CONTROL_PORT    default 9000, must match the image
//	HOST_EGRESS          7102=party-2.internal:7000,7200=vault.internal:8200,8443=kms.af-south-1.amazonaws.com:443
//	HOST_RUNTIME_CONFIG  path to a JSON object of runtime settings (enclave.RuntimeKeys only)
//	HOST_HEALTH_LISTEN   default :8081
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"openfireblocks.com/services/mpc-party/enclave"
)

type hostConfig struct {
	enclaveCID   uint32
	enclavePort  uint32
	listen       string
	controlPort  uint32
	egress       map[uint32]string
	runtime      map[string]string
	healthListen string
}

type hostDeps struct {
	listenVsock func(port uint32) (net.Listener, error)
	dialVsock   func(ctx context.Context, cid, port uint32) (net.Conn, error)
	dialTCP     func(ctx context.Context, addr string) (net.Conn, error)
	creds       aws.CredentialsProvider
	logf        func(string, ...any)
}

func parseHostEgress(spec string) (map[uint32]string, error) {
	out := map[uint32]string{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		p, dest, ok := strings.Cut(entry, "=")
		port, err := strconv.ParseUint(strings.TrimSpace(p), 10, 32)
		if !ok || err != nil || port == 0 {
			return nil, fmt.Errorf("HOST_EGRESS %q: expected vsock-port=host:port", entry)
		}
		dest = strings.TrimSpace(dest)
		if h, pp, err := net.SplitHostPort(dest); err != nil || h == "" || pp == "" {
			return nil, fmt.Errorf("HOST_EGRESS %q: destination must be host:port", entry)
		}
		if _, dup := out[uint32(port)]; dup {
			return nil, fmt.Errorf("HOST_EGRESS: vsock port %d listed twice", port)
		}
		out[uint32(port)] = dest
	}
	return out, nil
}

func u32(getenv func(string) string, key string, def uint32, required bool) (uint32, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		if required {
			return 0, fmt.Errorf("%s is required", key)
		}
		return def, nil
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number", key, v)
	}
	return uint32(n), nil
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func configFromEnv(getenv func(string) string) (hostConfig, error) {
	var c hostConfig
	var err error
	if c.enclaveCID, err = u32(getenv, "ENCLAVE_CID", 0, true); err != nil {
		return c, err
	}
	if c.enclavePort, err = u32(getenv, "ENCLAVE_VSOCK_PORT", 5000, false); err != nil {
		return c, err
	}
	if c.controlPort, err = u32(getenv, "HOST_CONTROL_PORT", 9000, false); err != nil {
		return c, err
	}
	if c.egress, err = parseHostEgress(getenv("HOST_EGRESS")); err != nil {
		return c, err
	}
	if _, clash := c.egress[c.controlPort]; clash {
		return c, fmt.Errorf("HOST_EGRESS uses the control port %d", c.controlPort)
	}
	c.listen = or(getenv("HOST_LISTEN"), ":7000")
	c.healthListen = or(getenv("HOST_HEALTH_LISTEN"), ":8081")
	path := strings.TrimSpace(getenv("HOST_RUNTIME_CONFIG"))
	if path == "" {
		return c, fmt.Errorf("HOST_RUNTIME_CONFIG is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if c.runtime, err = enclave.ParseRuntimeConfig(raw); err != nil {
		return c, err
	}
	return c, nil
}

// run starts every relay and blocks until ctx ends or one of them fails.
func run(ctx context.Context, c hostConfig, d hostDeps) error {
	errs := make(chan error, 4+len(c.egress))

	// Control channel.
	ctrl, err := d.listenVsock(c.controlPort)
	if err != nil {
		return fmt.Errorf("control port %d: %w", c.controlPort, err)
	}
	go func() {
		errs <- (&enclave.Control{Credentials: d.creds, Config: c.runtime, Logf: d.logf}).Serve(ctx, ctrl)
	}()

	// Egress relays.
	ports := make([]int, 0, len(c.egress))
	for p := range c.egress {
		ports = append(ports, int(p))
	}
	sort.Ints(ports)
	for _, p := range ports {
		dest := c.egress[uint32(p)]
		ln, err := d.listenVsock(uint32(p))
		if err != nil {
			return fmt.Errorf("egress port %d: %w", p, err)
		}
		go func() {
			errs <- enclave.Serve(ctx, ln, func(ctx context.Context) (net.Conn, error) { return d.dialTCP(ctx, dest) }, d.logf)
		}()
		d.logf("egress: vsock %d -> %s", p, dest)
	}

	// Inbound relay.
	in, err := net.Listen("tcp", c.listen)
	if err != nil {
		return err
	}
	go func() {
		errs <- enclave.Serve(ctx, in, func(ctx context.Context) (net.Conn, error) {
			return d.dialVsock(ctx, c.enclaveCID, c.enclavePort)
		}, d.logf)
	}()
	d.logf("inbound: %s -> enclave %d:%d", c.listen, c.enclaveCID, c.enclavePort)

	// Health.
	if c.healthListen != "-" {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			conn, err := d.dialVsock(ctx, c.enclaveCID, c.enclavePort)
			if err != nil {
				http.Error(w, "enclave not reachable", http.StatusServiceUnavailable)
				return
			}
			conn.Close()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
		})
		hs := &http.Server{Addr: c.healthListen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			_ = hs.Close()
		}()
		go func() {
			if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errs <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errs:
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func main() {
	c, err := configFromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("mpc-party-host: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The instance role, from the instance metadata service.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("mpc-party-host: AWS credentials: %v", err)
	}
	d := hostDeps{
		listenVsock: enclave.ListenVsock,
		dialVsock:   enclave.DialVsock,
		dialTCP: func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
		},
		creds: awsCfg.Credentials,
		logf:  log.Printf,
	}
	if err := run(ctx, c, d); err != nil {
		log.Fatalf("mpc-party-host: %v", err)
	}
}
