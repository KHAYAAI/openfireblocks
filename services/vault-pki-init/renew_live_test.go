//go:build live

package main

// Live test for RENEW=true against a real Vault PKI engine.
//
// The unit tests in services/mpc-party prove the *consumer* side: a process
// picks up replacement material off disk without restarting. This proves
// the other half -- that something actually replaces it. Between them the
// claim "certificates rotate" is covered end to end; either alone leaves a
// hole that only shows up a day after deployment, when the first
// certificate expires and nobody is watching.
//
// Deliberately drives real renewals rather than asserting on a schedule
// calculation: the failure this guards against (reusing an expired Vault
// login token, so the first renewal fails a full day after anyone looked)
// is invisible to a test that only checks arithmetic.
//
//	vault server -dev -dev-root-token-id=dev-root-token &
//	VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=dev-root-token \
//	  go test -tags live -run TestRenewLoop -timeout 5m ./...

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireVault(t *testing.T) (addr, token string) {
	t.Helper()
	addr, token = os.Getenv("VAULT_ADDR"), os.Getenv("VAULT_TOKEN")
	if addr == "" || token == "" {
		t.Skip("VAULT_ADDR and VAULT_TOKEN required for the live renewal test")
	}
	return addr, token
}

// vaultCLI runs a vault command against the dev server, failing the test on
// error -- setup, not the thing under test.
func vaultCLI(t *testing.T, addr, token string, args ...string) {
	t.Helper()
	cmd := exec.Command("vault", args...)
	cmd.Env = append(os.Environ(), "VAULT_ADDR="+addr, "VAULT_TOKEN="+token)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vault %v: %v\n%s", args, err, out)
	}
}

func serialOf(t *testing.T, certPath string) string {
	t.Helper()
	data, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return parsed.SerialNumber.String()
}

func TestRenewLoopReplacesTheCertificateBeforeItExpires(t *testing.T) {
	addr, token := requireVault(t)

	const mount = "pki-renew-test"
	vaultCLI(t, addr, token, "secrets", "enable", "-path="+mount, "pki")
	defer vaultCLI(t, addr, token, "secrets", "disable", mount)

	vaultCLI(t, addr, token, "secrets", "tune", "-max-lease-ttl=87600h", mount)
	vaultCLI(t, addr, token, "write", mount+"/root/generate/internal",
		"common_name=Renewal Test Root", "ttl=87600h")
	// A very short max_ttl so the renewal schedule (two thirds of the
	// certificate's life) plays out in seconds instead of hours.
	vaultCLI(t, addr, token, "write", mount+"/roles/short",
		"allowed_domains=internal", "allow_subdomains=true",
		"max_ttl=30s", "require_cn=true", "client_flag=true", "server_flag=true")

	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")

	bin := filepath.Join(t.TempDir(), "vault-pki-init")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"VAULT_ADDR="+addr,
		"VAULT_TOKEN="+token,
		"COMMON_NAME=party-1.internal",
		"VAULT_PKI_MOUNT="+mount,
		"VAULT_PKI_ROLE=short",
		"CERT_TTL=20s",
		"CERT_OUT_DIR="+dir,
		"RENEW=true",
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// First issuance.
	var first string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if first = serialOf(t, certPath); first != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if first == "" {
		t.Fatal("no certificate was ever written")
	}

	// Then wait for the process to replace it on its own. With a 20s TTL
	// the schedule puts the next issuance around 13s out; 90s is generous
	// enough to absorb a slow machine without making the failure ambiguous.
	var second string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if s := serialOf(t, certPath); s != "" && s != first {
			second = s
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if second == "" {
		t.Fatalf("certificate serial %s was never replaced -- the renewal loop is not renewing, "+
			"so the certificate's expiry is the pod's lifetime", first)
	}

	t.Logf("renewed: %s -> %s", first, second)

	// And again, because renewing exactly once is what a loop that
	// re-authenticates only at startup would also do.
	var third string
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if s := serialOf(t, certPath); s != "" && s != second {
			third = s
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if third == "" {
		t.Fatalf("certificate renewed once (%s -> %s) but not twice -- a loop that renews "+
			"only once is indistinguishable from one that cannot re-authenticate", first, second)
	}
	t.Logf("renewed again: %s -> %s", second, third)

	// The material on disk has to remain usable, not merely different.
	if _, err := os.Stat(filepath.Join(dir, "tls.key")); err != nil {
		t.Fatalf("private key missing after renewal: %v", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read renewed cert: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("renewed certificate is not valid PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("renewed certificate does not parse: %v", err)
	}
	if time.Now().After(parsed.NotAfter) {
		t.Fatalf("renewed certificate is already expired (NotAfter %s)", parsed.NotAfter)
	}
	if parsed.Subject.CommonName != "party-1.internal" {
		t.Fatalf("renewed certificate has CN %q, want party-1.internal", parsed.Subject.CommonName)
	}
}

// Guards the schedule itself: renewal has to happen strictly before expiry,
// with enough margin left to retry after a failure.
func TestRenewalDelayLeavesRoomToRetry(t *testing.T) {
	ttl := time.Hour
	expiry := time.Now().Add(ttl)

	delay := renewalDelay(expiry, "1h")
	if delay >= ttl {
		t.Fatalf("renewal delay %s is not shorter than the certificate lifetime %s", delay, ttl)
	}
	remainingAfter := time.Until(expiry) - delay
	if remainingAfter < ttl/4 {
		t.Fatalf("only %s left after the first renewal attempt; a single failure would run out the clock", remainingAfter)
	}

	// An unparseable expiry must not produce a zero or negative delay.
	if d := renewalDelay(time.Time{}, "24h"); d < minRenewalDelay {
		t.Fatalf("fallback delay %s is below the floor %s", d, minRenewalDelay)
	}
	// Nor should an already-expired certificate produce a busy loop.
	if d := renewalDelay(time.Now().Add(-time.Hour), "1h"); d < minRenewalDelay {
		t.Fatalf("expired-certificate delay %s would spin against Vault", d)
	}
}

// A root rotation must be noticed promptly, not at the next scheduled
// renewal.
//
// Rebuilding Vault underneath a running cluster produced exactly this: every
// service kept presenting leaves from a CA that no longer existed, and every
// connection failed with "certificate signed by unknown authority" -- an
// error that blames the peer for a purely local problem. With a 24h
// certificate, waiting for the renewal schedule would have meant most of a
// day of that.
func TestCAChangeIsDetected(t *testing.T) {
	addr, token := requireVault(t)

	const mount = "pki-ca-change-test"
	vaultCLI(t, addr, token, "secrets", "enable", "-path="+mount, "pki")
	defer vaultCLI(t, addr, token, "secrets", "disable", mount)
	vaultCLI(t, addr, token, "secrets", "tune", "-max-lease-ttl=87600h", mount)
	vaultCLI(t, addr, token, "write", mount+"/root/generate/internal",
		"common_name=CA Change Test Root", "ttl=87600h")
	vaultCLI(t, addr, token, "write", mount+"/roles/short",
		"allowed_domains=internal", "allow_subdomains=true",
		"max_ttl=24h", "require_cn=true", "client_flag=true", "server_flag=true")

	dir := t.TempDir()
	client := &http.Client{Timeout: 30 * time.Second}

	cert, err := issueCertificate(client, addr, token, mount, "short", "party-1.internal", "", "1h")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := writeCertFiles(dir, cert); err != nil {
		t.Fatalf("write: %v", err)
	}

	changed, err := caHasChanged(client, addr, mount, dir)
	if err != nil {
		t.Fatalf("caHasChanged: %v", err)
	}
	if changed {
		t.Fatal("reported a CA change immediately after writing the CA it just fetched")
	}

	// A real rotation. Generating a second root on the same mount is not
	// one: Vault creates an additional issuer and leaves the *default*
	// issuer -- which is what /ca/pem serves -- pointing at the original, so
	// nothing an existing client sees actually changes. Deleting the root
	// first is what replacing a CA looks like, and is what wiping and
	// reinstalling Vault does to a deployment.
	vaultCLI(t, addr, token, "delete", mount+"/root")
	vaultCLI(t, addr, token, "write", mount+"/root/generate/internal",
		"common_name=CA Change Test Root Two", "ttl=87600h")

	changed, err = caHasChanged(client, addr, mount, dir)
	if err != nil {
		t.Fatalf("caHasChanged after rotation: %v", err)
	}
	if !changed {
		t.Fatal("a root rotation went unnoticed; every leaf is now unverifiable " +
			"and nothing would re-issue until the scheduled renewal")
	}
}
