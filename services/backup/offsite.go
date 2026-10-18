package backup

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Off-site copies of a backup.
//
// Until this existed every backup landed on a volume in the same cluster, so
// losing the cluster lost the backups with it. A deployment can now send each
// completed backup to an S3-compatible bucket in another account or region.
//
// The dumps hold the whole database and a Vault snapshot, so nothing leaves
// the cluster unencrypted. Each file is encrypted here, with a key the bucket
// owner never sees (BACKUP_OFFSITE_KEY_FILE), so a stolen bucket yields
// ciphertext. AES-256-GCM in 1 MiB chunks: each chunk is authenticated and
// bound to its position and to whether it is the last one, so reordering,
// altering or truncating a backup fails to decrypt instead of restoring a
// damaged database.

// ObjectStore is the part of an object store this needs. The S3 client
// implements it; tests use memory.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

const (
	offsiteMagic = "OFBBK1\x00\x00"
	chunkSize    = 1 << 20
	prefixLen    = 8
)

// OffsiteReceipt says what was stored and how to check it.
type OffsiteReceipt struct {
	Key             string `json:"key"`
	PlaintextSHA256 string `json:"plaintext_sha256"`
	PlaintextBytes  int64  `json:"plaintext_bytes"`
}

// OffsiteStore encrypts files and puts them in an ObjectStore under a prefix.
type OffsiteStore struct {
	objects ObjectStore
	prefix  string
	aead    cipher.AEAD
}

// NewOffsiteStore builds the store from a 32-byte key.
func NewOffsiteStore(objects ObjectStore, prefix string, key []byte) (*OffsiteStore, error) {
	if len(key) != 32 {
		return nil, errors.New("the off-site key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &OffsiteStore{objects: objects, prefix: strings.Trim(prefix, "/"), aead: aead}, nil
}

// LoadOffsiteKey reads the key from a file that only its owner can read, as
// 32 raw bytes or 64 hex characters.
func LoadOffsiteKey(file string) ([]byte, error) {
	if file == "" {
		return nil, errors.New("BACKUP_OFFSITE_KEY_FILE is not set: refusing to send backups off-site without encrypting them")
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("off-site key file: %w", err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		return nil, fmt.Errorf("off-site key file %s is readable by other users (mode %o); it must not be world-accessible (0640 or stricter; Kubernetes adds group read to mounted Secrets when fsGroup is set)", file, info.Mode().Perm())
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if t := strings.TrimSpace(string(raw)); len(t) == 64 {
		if k, e := hex.DecodeString(t); e == nil {
			return k, nil
		}
	}
	if len(raw) == 32 {
		return raw, nil
	}
	return nil, errors.New("off-site key must be 32 bytes, as raw bytes or 64 hex characters")
}

func (o *OffsiteStore) objectKey(backupID, name string) string {
	return path.Join(o.prefix, backupID, name)
}

func chunkNonce(prefix []byte, counter uint32) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[prefixLen:], counter)
	return n
}

func chunkAAD(counter uint32, last bool) []byte {
	aad := make([]byte, 5)
	binary.BigEndian.PutUint32(aad, counter)
	if last {
		aad[4] = 1
	}
	return aad
}

// Put encrypts the file and uploads it as <prefix>/<backupID>/<name>.enc.
func (o *OffsiteStore) Put(ctx context.Context, backupID, name, localPath string) (*OffsiteReceipt, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	nonce := make([]byte, prefixLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sum := sha256.New()
	var total int64
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(o.encrypt(io.TeeReader(f, countingWriter{sum, &total}), pw, nonce))
	}()
	key := o.objectKey(backupID, name+".enc")
	if err := o.objects.Put(ctx, key, pr); err != nil {
		pr.CloseWithError(err)
		return nil, fmt.Errorf("uploading %s: %w", key, err)
	}
	return &OffsiteReceipt{Key: key, PlaintextSHA256: hex.EncodeToString(sum.Sum(nil)), PlaintextBytes: total}, nil
}

type countingWriter struct {
	w io.Writer
	n *int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return c.w.Write(p)
}

func (o *OffsiteStore) encrypt(src io.Reader, dst io.Writer, noncePrefix []byte) error {
	if _, err := dst.Write([]byte(offsiteMagic)); err != nil {
		return err
	}
	if _, err := dst.Write(noncePrefix); err != nil {
		return err
	}
	r := bufio.NewReaderSize(src, chunkSize+1)
	var counter uint32
	for {
		buf := make([]byte, chunkSize)
		n, err := io.ReadFull(r, buf)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return err
		}
		buf = buf[:n]
		// The last chunk is the one after which nothing remains. Peeking
		// avoids marking a full-size final chunk as "not last".
		_, peekErr := r.Peek(1)
		last := peekErr == io.EOF
		sealed := o.aead.Seal(nil, chunkNonce(noncePrefix, counter), buf, chunkAAD(counter, last))
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(sealed)))
		if _, err := dst.Write(hdr[:]); err != nil {
			return err
		}
		if _, err := dst.Write(sealed); err != nil {
			return err
		}
		if last {
			return nil
		}
		counter++
		if counter == 0 {
			return errors.New("file too large for one backup object")
		}
	}
}

// Get downloads, decrypts and verifies one object into destPath. The file is
// written under a temporary name and moved into place only after the last
// chunk has authenticated, so a truncated or altered object never leaves a
// plausible-looking dump behind.
func (o *OffsiteStore) Get(ctx context.Context, backupID, name, destPath string, want *OffsiteReceipt) error {
	rc, err := o.objects.Get(ctx, o.objectKey(backupID, name+".enc"))
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".offsite-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	sum := sha256.New()
	if err := o.decrypt(rc, io.MultiWriter(tmp, sum)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if want != nil && want.PlaintextSHA256 != "" && hex.EncodeToString(sum.Sum(nil)) != want.PlaintextSHA256 {
		return errors.New("the decrypted backup does not match the checksum recorded when it was taken")
	}
	return os.Rename(tmp.Name(), destPath)
}

func (o *OffsiteStore) decrypt(src io.Reader, dst io.Writer) error {
	hdr := make([]byte, len(offsiteMagic)+prefixLen)
	if _, err := io.ReadFull(src, hdr); err != nil || string(hdr[:len(offsiteMagic)]) != offsiteMagic {
		return errors.New("not an OpenFireblocks off-site backup (bad header)")
	}
	prefix := hdr[len(offsiteMagic):]
	var counter uint32
	for {
		var lb [4]byte
		if _, err := io.ReadFull(src, lb[:]); err != nil {
			return errors.New("the backup is truncated: it ends before its final chunk")
		}
		n := binary.BigEndian.Uint32(lb[:])
		if n < uint32(o.aead.Overhead()) || n > chunkSize+uint32(o.aead.Overhead()) {
			return errors.New("the backup is corrupt: impossible chunk size")
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return errors.New("the backup is truncated inside a chunk")
		}
		// Try it as the last chunk, then as a middle one: which it is is
		// authenticated, not asserted by the file.
		plain, err := o.aead.Open(nil, chunkNonce(prefix, counter), sealed, chunkAAD(counter, true))
		last := err == nil
		if !last {
			plain, err = o.aead.Open(nil, chunkNonce(prefix, counter), sealed, chunkAAD(counter, false))
			if err != nil {
				return errors.New("the backup failed authentication: wrong key, altered, or reordered")
			}
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		if last {
			if _, err := src.Read(make([]byte, 1)); err != io.EOF {
				return errors.New("the backup has data after its final chunk")
			}
			return nil
		}
		counter++
	}
}
