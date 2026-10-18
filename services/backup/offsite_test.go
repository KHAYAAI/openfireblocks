package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memObjects struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMem() *memObjects { return &memObjects{m: map[string][]byte{}} }
func (o *memObjects) Put(_ context.Context, key string, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.m[key] = b
	o.mu.Unlock()
	return nil
}
func (o *memObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.m[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func key32(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func writeFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	p := filepath.Join(t.TempDir(), "dump")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// Sizes that straddle the chunk boundary are where an off-by-one in "which
// chunk is last" would hide.
func TestAnyBackupSizeComesBackIdentical(t *testing.T) {
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			objs := newMem()
			st, _ := NewOffsiteStore(objs, "p", key32(t))
			src, data := writeFile(t, size)
			rec, err := st.Put(context.Background(), "b1", "postgres", src)
			if err != nil || rec.PlaintextBytes != int64(size) {
				t.Fatalf("put: %+v %v", rec, err)
			}
			dest := filepath.Join(t.TempDir(), "restored")
			if err := st.Get(context.Background(), "b1", "postgres", dest, rec); err != nil {
				t.Fatalf("get: %v", err)
			}
			got, _ := os.ReadFile(dest)
			if !bytes.Equal(got, data) {
				t.Fatal("restored bytes differ")
			}
		})
	}
}

func TestWhatLeavesTheClusterIsCiphertext(t *testing.T) {
	objs := newMem()
	st, _ := NewOffsiteStore(objs, "p", key32(t))
	src := filepath.Join(t.TempDir(), "d")
	secret := "PGDMP-customer-table-row-ACME-CORP-balance"
	_ = os.WriteFile(src, []byte(strings.Repeat(secret, 100)), 0o600)
	if _, err := st.Put(context.Background(), "b1", "postgres", src); err != nil {
		t.Fatal(err)
	}
	for k, v := range objs.m {
		if bytes.Contains(v, []byte("ACME-CORP")) {
			t.Fatalf("plaintext found in %s", k)
		}
	}
}

func TestAlteredTruncatedOrWrongKeyBackupsNeverRestore(t *testing.T) {
	ctx := context.Background()
	objs := newMem()
	k := key32(t)
	st, _ := NewOffsiteStore(objs, "p", k)
	src, _ := writeFile(t, 2*chunkSize+5)
	rec, _ := st.Put(ctx, "b1", "postgres", src)
	key := rec.Key
	orig := append([]byte(nil), objs.m[key]...)
	dest := func() string { return filepath.Join(t.TempDir(), "out") }

	// one flipped bit
	flipped := append([]byte(nil), orig...)
	flipped[len(flipped)/2] ^= 1
	objs.m[key] = flipped
	if err := st.Get(ctx, "b1", "postgres", dest(), rec); err == nil {
		t.Error("an altered backup restored")
	}
	// cut at a chunk boundary: every remaining chunk is individually valid
	first := 8 + 8 + 4 + chunkSize + 16
	objs.m[key] = orig[:first]
	d := dest()
	if err := st.Get(ctx, "b1", "postgres", d, rec); err == nil {
		t.Error("a truncated backup restored")
	}
	if _, err := os.Stat(d); err == nil {
		t.Error("a half-restored file was left behind")
	}
	// trailing garbage
	objs.m[key] = append(append([]byte(nil), orig...), 0)
	if err := st.Get(ctx, "b1", "postgres", dest(), rec); err == nil {
		t.Error("a backup with trailing data restored")
	}
	// the wrong key
	objs.m[key] = orig
	other, _ := NewOffsiteStore(objs, "p", key32(t))
	if err := other.Get(ctx, "b1", "postgres", dest(), rec); err == nil {
		t.Error("restored with the wrong key")
	}
	// a checksum that does not match what was recorded
	bad := *rec
	bad.PlaintextSHA256 = strings.Repeat("0", 64)
	if err := st.Get(ctx, "b1", "postgres", dest(), &bad); err == nil {
		t.Error("restored despite a checksum mismatch")
	}
	// and the untouched object still restores
	if err := st.Get(ctx, "b1", "postgres", dest(), rec); err != nil {
		t.Errorf("good backup refused: %v", err)
	}
}

func TestChunksCannotBeReordered(t *testing.T) {
	ctx := context.Background()
	objs := newMem()
	st, _ := NewOffsiteStore(objs, "p", key32(t))
	src, _ := writeFile(t, 3*chunkSize)
	rec, _ := st.Put(ctx, "b1", "x", src)
	b := objs.m[rec.Key]
	hdr := 16
	sz := 4 + chunkSize + 16
	c1, c2 := append([]byte(nil), b[hdr:hdr+sz]...), append([]byte(nil), b[hdr+sz:hdr+2*sz]...)
	copy(b[hdr:], c2)
	copy(b[hdr+sz:], c1)
	if err := st.Get(ctx, "b1", "x", filepath.Join(t.TempDir(), "o"), rec); err == nil {
		t.Fatal("reordered chunks restored")
	}
}

func TestKeyHandling(t *testing.T) {
	if _, err := LoadOffsiteKey(""); err == nil {
		t.Error("no key file accepted")
	}
	f := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(f, []byte(strings.Repeat("ab", 32)), 0o644)
	if _, err := LoadOffsiteKey(f); err == nil || !strings.Contains(err.Error(), "other users") {
		t.Errorf("world-readable key accepted: %v", err)
	}
	_ = os.Chmod(f, 0o600)
	if k, err := LoadOffsiteKey(f); err != nil || len(k) != 32 {
		t.Errorf("hex key: %v", err)
	}
	_ = os.WriteFile(f, []byte("short"), 0o600)
	if _, err := LoadOffsiteKey(f); err == nil {
		t.Error("a short key was accepted")
	}
	if _, err := NewOffsiteStore(newMem(), "p", []byte("short")); err == nil {
		t.Error("a short key built a store")
	}
}

// --- the manager

type fakeDump struct{ id, source, dir string }

func (f fakeDump) BackupFull(_ context.Context, _ string) (*BackupMetadata, error) {
	p := filepath.Join(f.dir, f.source+".dump")
	_ = os.WriteFile(p, []byte("contents of "+f.source), 0o600)
	return &BackupMetadata{ID: f.id, Source: f.source, Destination: p, Size: 20}, nil
}
func (f fakeDump) BackupIncremental(context.Context, string, time.Time) (*BackupMetadata, error) {
	return nil, nil
}
func (f fakeDump) Restore(context.Context, string) error            { return nil }
func (f fakeDump) RestoreIncremental(context.Context, string) error { return nil }

func TestACompletedBackupIsCopiedOffsiteAndCanBeFetchedBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := &FilesystemBackupStorage{Dir: filepath.Join(dir, "meta")}
	mgr := NewBackupManager(storage, fakeDump{"v1", "vault", dir}, fakeDump{"p1", "postgres", dir})
	objs := newMem()
	st, _ := NewOffsiteStore(objs, "ofb", key32(t))
	mgr.SetOffsite(st)

	meta, err := mgr.ExecuteFullBackup(ctx, "local")
	if err != nil || meta.Status != BackupStatusCompleted {
		t.Fatalf("backup: %+v %v", meta, err)
	}
	for _, name := range []string{"postgres", "vault", "manifest"} {
		if meta.Tags["offsite_"+name+"_sha256"] == "" || meta.Tags["offsite_"+name+"_key"] == "" {
			t.Errorf("no receipt for %s: %v", name, meta.Tags)
		}
	}
	if len(objs.m) != 3 {
		t.Fatalf("%d objects uploaded, want 3", len(objs.m))
	}

	// The cluster is gone: only the bucket, the key and the checksums remain.
	files, err := mgr.FetchOffsite(ctx, meta.ID, meta.Tags, filepath.Join(t.TempDir(), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(files["postgres"])
	if string(got) != "contents of postgres" {
		t.Errorf("restored %q", got)
	}
	// Without the recorded checksums it refuses rather than restoring unchecked.
	if _, err := mgr.FetchOffsite(ctx, meta.ID, nil, t.TempDir()); err == nil {
		t.Error("fetched with no checksums to verify against")
	}
}

type failingObjects struct{ memObjects }

func (*failingObjects) Put(context.Context, string, io.Reader) error {
	return errors.New("bucket unreachable")
}

func TestAFailedOffsiteCopyFailsTheRun(t *testing.T) {
	dir := t.TempDir()
	mgr := NewBackupManager(&FilesystemBackupStorage{Dir: dir + "/meta"}, fakeDump{"v1", "vault", dir}, fakeDump{"p1", "postgres", dir})
	st, _ := NewOffsiteStore(&failingObjects{*newMem()}, "ofb", key32(t))
	mgr.SetOffsite(st)
	meta, err := mgr.ExecuteFullBackup(context.Background(), "local")
	if err == nil || meta.Status != BackupStatusFailed || !strings.Contains(meta.ErrorMessage, "off-site copy failed") {
		t.Fatalf("a backup that could not be copied off-site was reported fine: %+v %v", meta, err)
	}
}

// --- the S3 client, against a small S3-compatible server

func TestS3ObjectStoreSpeaksS3(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	var sawSSE string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
			sawSSE = r.Header.Get("X-Amz-Server-Side-Encryption")
			w.Header().Set("ETag", `"x"`)
		case http.MethodGet:
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(b)
		}
	}))
	defer srv.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	s3, err := NewS3ObjectStore(context.Background(), S3Config{Bucket: "bk", Region: "af-south-1", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := NewOffsiteStore(s3, "ofb", key32(t))
	src, data := writeFile(t, chunkSize+100)
	rec, err := st.Put(context.Background(), "b1", "postgres", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := objects["/bk/ofb/b1/postgres.enc"]; !ok {
		t.Fatalf("object not at the expected path; have %v", keysOf(objects))
	}
	if sawSSE != "AES256" {
		t.Errorf("server-side encryption header = %q", sawSSE)
	}
	dest := filepath.Join(t.TempDir(), "r")
	if err := st.Get(context.Background(), "b1", "postgres", dest, rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Fatal("round trip through the S3 client differs")
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
