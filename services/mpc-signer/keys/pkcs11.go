//go:build pkcs11

package keys

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/miekg/pkcs11"
)

// PKCS11Supported reports whether this binary was built with -tags pkcs11.
const PKCS11Supported = true

// secp256k1's OID, 1.3.132.0.10, DER-encoded as CKA_EC_PARAMS carries it.
var secp256k1Params = []byte{0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x0a}

// PKCS11Signer signs with a secp256k1 key held inside a hardware security
// module. The private key never enters this process: C_Sign runs on the
// token and returns R || S, which RecoverableSignature turns into the
// [R || S || V] layout every chain here consumes.
//
// What it guarantees at open, and refuses to start without:
//   - exactly one private and one public key object carry the label, so a
//     duplicate or a typo cannot pick "some" key;
//   - the curve is secp256k1;
//   - the private key is CKA_SENSITIVE and not CKA_EXTRACTABLE -- a key
//     the token will hand out is not a key in hardware in any sense that
//     matters, and this mode exists to make that claim true;
//   - the private key actually belongs to the public key: a self-test
//     signature must recover to it. Two pairs sharing a label, or a public
//     object left over from a rotated key, would otherwise produce
//     signatures for an address nobody holds funds at.
//
// Safe for concurrent use. A PKCS#11 session is not, so signing is
// serialised on one session; an HSM signs a secp256k1 digest in well under
// a millisecond, which is not the bottleneck of anything this service does.
type PKCS11Signer struct {
	cfg    PKCS11Config
	module *module

	mu      sync.Mutex
	slot    uint
	session pkcs11.SessionHandle
	priv    pkcs11.ObjectHandle
	open    bool

	pub      []byte
	describe string
}

// OpenPKCS11 logs in to the token and locates the key. The key must
// already exist; see GeneratePKCS11Key. Starting a signing service never
// creates a key, because a mistyped label would then silently mint a new
// address and the first anyone would hear of it is a deposit sent to the
// old one.
func OpenPKCS11(cfg *PKCS11Config) (KeySigner, error) {
	if cfg == nil {
		return nil, errors.New("no PKCS#11 configuration")
	}
	m, err := loadModule(cfg.Library)
	if err != nil {
		return nil, err
	}
	s := &PKCS11Signer{cfg: *cfg, module: m}
	if err := s.connect(); err != nil {
		m.release()
		return nil, err
	}

	info, err := m.ctx.GetTokenInfo(s.slot)
	model := ""
	if err == nil {
		model = fmt.Sprintf(", %s %s", strings.TrimSpace(info.ManufacturerID), strings.TrimSpace(info.Model))
	}
	s.describe = fmt.Sprintf("PKCS#11 key %q on %s%s", cfg.KeyLabel, cfg.token(), model)
	return s, nil
}

func (s *PKCS11Signer) PublicKey() []byte { return append([]byte(nil), s.pub...) }

func (s *PKCS11Signer) Describe() string { return s.describe }

// SignDigest signs on the token.
//
// If the session has gone -- the HSM restarted, a network HSM dropped the
// connection -- it reconnects once and retries, and on reconnecting checks
// the key is still the same key. Signing is idempotent in the sense that
// matters (a signature for a digest is only useful for that digest), so a
// retry cannot do harm the first attempt could not.
func (s *PKCS11Signer) SignDigest(_ context.Context, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("a digest is 32 bytes, got %d", len(digest))
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rs, err := s.signLocked(digest)
	if err != nil && recoverable(err) {
		if rerr := s.reconnectLocked(); rerr != nil {
			return nil, fmt.Errorf("HSM session lost (%v) and reconnecting failed: %w", err, rerr)
		}
		rs, err = s.signLocked(digest)
	}
	if err != nil {
		return nil, fmt.Errorf("HSM signing failed: %w", err)
	}
	return RecoverableSignature(digest, rs, s.pub)
}

// Close logs out and releases the session.
func (s *PKCS11Signer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
	s.module.release()
	return nil
}

func (s *PKCS11Signer) signLocked(digest []byte) ([]byte, error) {
	if !s.open {
		return nil, pkcs11.Error(pkcs11.CKR_SESSION_HANDLE_INVALID)
	}
	ctx := s.module.ctx
	if err := ctx.SignInit(s.session, []*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_ECDSA, nil)}, s.priv); err != nil {
		return nil, err
	}
	return ctx.Sign(s.session, digest)
}

func recoverable(err error) bool {
	var perr pkcs11.Error
	if !errors.As(err, &perr) {
		return false
	}
	switch uint(perr) {
	case pkcs11.CKR_SESSION_HANDLE_INVALID, pkcs11.CKR_SESSION_CLOSED,
		pkcs11.CKR_USER_NOT_LOGGED_IN, pkcs11.CKR_OBJECT_HANDLE_INVALID,
		pkcs11.CKR_KEY_HANDLE_INVALID, pkcs11.CKR_DEVICE_ERROR,
		pkcs11.CKR_DEVICE_REMOVED, pkcs11.CKR_TOKEN_NOT_PRESENT:
		return true
	}
	return false
}

func (s *PKCS11Signer) reconnectLocked() error {
	previous := s.pub
	s.closeLocked()
	if err := s.connectLocked(); err != nil {
		return err
	}
	if !bytes.Equal(previous, s.pub) {
		// Someone replaced the key under this label while the session
		// was down. Refuse to sign with it: every address this service
		// has handed out belongs to the old one.
		s.closeLocked()
		s.pub = previous
		return fmt.Errorf("the key labelled %q changed while the HSM session was down; refusing to sign "+
			"with a different key from the one this service started with", s.cfg.KeyLabel)
	}
	return nil
}

func (s *PKCS11Signer) closeLocked() {
	if !s.open {
		return
	}
	_ = s.module.ctx.Logout(s.session)
	_ = s.module.ctx.CloseSession(s.session)
	s.open = false
}

func (s *PKCS11Signer) connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connectLocked()
}

func (s *PKCS11Signer) connectLocked() error {
	ctx := s.module.ctx
	slot, err := findSlot(ctx, &s.cfg)
	if err != nil {
		return err
	}
	s.slot = slot

	session, err := openAndLogin(ctx, slot, s.cfg.PIN)
	if err != nil {
		return fmt.Errorf("%s: %w", s.cfg.token(), err)
	}
	fail := func(err error) error {
		_ = ctx.Logout(session)
		_ = ctx.CloseSession(session)
		return err
	}

	priv, err := findOne(ctx, session, pkcs11.CKO_PRIVATE_KEY, s.cfg.KeyLabel)
	if err != nil {
		return fail(err)
	}
	pubObj, err := findOne(ctx, session, pkcs11.CKO_PUBLIC_KEY, s.cfg.KeyLabel)
	if err != nil {
		return fail(err)
	}

	if err := checkPrivateKey(ctx, session, priv, pubObj); err != nil {
		return fail(fmt.Errorf("key %q: %w", s.cfg.KeyLabel, err))
	}
	pub, err := readECPoint(ctx, session, pubObj)
	if err != nil {
		return fail(fmt.Errorf("key %q: %w", s.cfg.KeyLabel, err))
	}

	s.session, s.priv, s.pub, s.open = session, priv, pub, true

	// The self-test: the private object must sign for the public object.
	probe := sha256.Sum256([]byte("openfireblocks-pkcs11-self-test-v1"))
	rs, err := s.signLocked(probe[:])
	if err != nil {
		s.closeLocked()
		return fmt.Errorf("key %q: the self-test signature failed: %w", s.cfg.KeyLabel, err)
	}
	if _, err := RecoverableSignature(probe[:], rs, pub); err != nil {
		s.closeLocked()
		return fmt.Errorf("key %q: the private key does not match the public key with the same label "+
			"(are two key pairs sharing a label?): %w", s.cfg.KeyLabel, err)
	}
	return nil
}

// GeneratePKCS11Key creates a secp256k1 key pair on the token, inside it:
// the private half is generated there, marked sensitive and
// non-extractable, and never exists anywhere else. There is no backup
// through this path by design -- backup is the HSM's own mechanism
// (cloning between CloudHSM cluster members, a Luna backup HSM), which
// keeps the key in hardware.
//
// Refuses if a key with the label already exists, rather than create a
// second one beside it.
func GeneratePKCS11Key(cfg *PKCS11Config) error {
	m, err := loadModule(cfg.Library)
	if err != nil {
		return err
	}
	defer m.release()
	ctx := m.ctx

	slot, err := findSlot(ctx, cfg)
	if err != nil {
		return err
	}
	session, err := openAndLogin(ctx, slot, cfg.PIN)
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.token(), err)
	}
	defer func() { _ = ctx.Logout(session); _ = ctx.CloseSession(session) }()

	for _, class := range []uint{pkcs11.CKO_PRIVATE_KEY, pkcs11.CKO_PUBLIC_KEY} {
		found, err := findAll(ctx, session, class, cfg.KeyLabel)
		if err != nil {
			return err
		}
		if len(found) > 0 {
			return fmt.Errorf("a key labelled %q already exists on %s; refusing to create another", cfg.KeyLabel, cfg.token())
		}
	}

	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	pubTemplate := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PUBLIC_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_EC),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
		pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, secp256k1Params),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, cfg.KeyLabel),
		pkcs11.NewAttribute(pkcs11.CKA_ID, id),
	}
	privTemplate := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_EC),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, cfg.KeyLabel),
		pkcs11.NewAttribute(pkcs11.CKA_ID, id),
	}
	_, _, err = ctx.GenerateKeyPair(session,
		[]*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_EC_KEY_PAIR_GEN, nil)},
		pubTemplate, privTemplate)
	if err != nil {
		return fmt.Errorf("generating a secp256k1 key pair on %s: %w (does this HSM support secp256k1?)", cfg.token(), err)
	}
	return nil
}

// ---------------------------------------------------------------------------

func findSlot(ctx *pkcs11.Ctx, cfg *PKCS11Config) (uint, error) {
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		return 0, fmt.Errorf("listing PKCS#11 slots: %w", err)
	}
	if cfg.TokenLabel == "" {
		for _, s := range slots {
			if s == cfg.Slot {
				return s, nil
			}
		}
		return 0, fmt.Errorf("no token present in slot %d", cfg.Slot)
	}
	var match []uint
	for _, s := range slots {
		info, err := ctx.GetTokenInfo(s)
		if err != nil {
			continue
		}
		if strings.TrimRight(info.Label, " \x00") == cfg.TokenLabel {
			match = append(match, s)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return 0, fmt.Errorf("no PKCS#11 token labelled %q", cfg.TokenLabel)
	default:
		return 0, fmt.Errorf("%d PKCS#11 tokens are labelled %q; set %s to choose one", len(match), cfg.TokenLabel, EnvPKCS11Slot)
	}
}

func openAndLogin(ctx *pkcs11.Ctx, slot uint, pin string) (pkcs11.SessionHandle, error) {
	session, err := ctx.OpenSession(slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		return 0, fmt.Errorf("opening a session: %w", err)
	}
	if err := ctx.Login(session, pkcs11.CKU_USER, pin); err != nil {
		var perr pkcs11.Error
		if !(errors.As(err, &perr) && uint(perr) == pkcs11.CKR_USER_ALREADY_LOGGED_IN) {
			_ = ctx.CloseSession(session)
			// Never echo the PIN, not even its length.
			return 0, fmt.Errorf("logging in: %w", err)
		}
	}
	return session, nil
}

func findAll(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, class uint, label string) ([]pkcs11.ObjectHandle, error) {
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, class),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
	}
	if err := ctx.FindObjectsInit(session, template); err != nil {
		return nil, err
	}
	defer func() { _ = ctx.FindObjectsFinal(session) }()
	objs, _, err := ctx.FindObjects(session, 8)
	return objs, err
}

func findOne(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, class uint, label string) (pkcs11.ObjectHandle, error) {
	kind := "private"
	if class == pkcs11.CKO_PUBLIC_KEY {
		kind = "public"
	}
	objs, err := findAll(ctx, session, class, label)
	if err != nil {
		return 0, fmt.Errorf("searching for the %s key %q: %w", kind, label, err)
	}
	switch len(objs) {
	case 1:
		return objs[0], nil
	case 0:
		return 0, fmt.Errorf("no %s key labelled %q on the token; create one with `hsm-key generate`", kind, label)
	default:
		return 0, fmt.Errorf("%d %s keys are labelled %q; refusing to guess which one signs", len(objs), kind, label)
	}
}

func boolAttr(a *pkcs11.Attribute) (bool, bool) {
	if a == nil || len(a.Value) != 1 {
		return false, false
	}
	return a.Value[0] != 0, true
}

func checkPrivateKey(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, priv, pub pkcs11.ObjectHandle) error {
	attrs, err := ctx.GetAttributeValue(session, priv, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, nil),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, nil),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, nil),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, nil),
	})
	if err != nil {
		return fmt.Errorf("reading the private key's attributes: %w", err)
	}
	byType := map[uint]*pkcs11.Attribute{}
	for _, a := range attrs {
		byType[a.Type] = a
	}
	if kt := byType[pkcs11.CKA_KEY_TYPE]; kt == nil || len(kt.Value) == 0 || kt.Value[0] != byte(pkcs11.CKK_EC) {
		return errors.New("not an elliptic-curve key")
	}
	if v, ok := boolAttr(byType[pkcs11.CKA_SENSITIVE]); !ok || !v {
		return errors.New("the private key is not CKA_SENSITIVE; the token would reveal it, " +
			"so it is not a hardware-held key and this mode will not use it")
	}
	if v, ok := boolAttr(byType[pkcs11.CKA_EXTRACTABLE]); !ok || v {
		return errors.New("the private key is CKA_EXTRACTABLE; it can be wrapped out of the token, " +
			"so it is not a hardware-held key and this mode will not use it")
	}
	if v, ok := boolAttr(byType[pkcs11.CKA_SIGN]); !ok || !v {
		return errors.New("the private key does not permit signing (CKA_SIGN is false)")
	}

	// Curve: some tokens expose CKA_EC_PARAMS only on the public half.
	for _, h := range []pkcs11.ObjectHandle{priv, pub} {
		a, err := ctx.GetAttributeValue(session, h, []*pkcs11.Attribute{pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, nil)})
		if err != nil || len(a) == 0 || len(a[0].Value) == 0 {
			continue
		}
		if !bytes.Equal(a[0].Value, secp256k1Params) {
			return fmt.Errorf("the key is not on secp256k1 (CKA_EC_PARAMS %x); every chain this service "+
				"signs for through this path needs secp256k1", a[0].Value)
		}
		return nil
	}
	return errors.New("the token does not report the key's curve")
}

// readECPoint returns the 65-byte uncompressed point. PKCS#11 v2.20+
// says CKA_EC_POINT is a DER OCTET STRING wrapping it; some tokens return
// the bare point. Both are accepted, and whatever comes out is checked to
// be on the curve.
func readECPoint(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, pub pkcs11.ObjectHandle) ([]byte, error) {
	a, err := ctx.GetAttributeValue(session, pub, []*pkcs11.Attribute{pkcs11.NewAttribute(pkcs11.CKA_EC_POINT, nil)})
	if err != nil || len(a) == 0 {
		return nil, fmt.Errorf("reading CKA_EC_POINT: %w", err)
	}
	raw := a[0].Value
	point := raw
	if !(len(raw) == 65 && raw[0] == 0x04) {
		var inner []byte
		rest, err := asn1.Unmarshal(raw, &inner)
		if err != nil || len(rest) != 0 {
			return nil, fmt.Errorf("CKA_EC_POINT is neither a bare point nor a DER OCTET STRING (%d bytes)", len(raw))
		}
		point = inner
	}
	if _, _, err := coordinates(point); err != nil {
		return nil, err
	}
	return point, nil
}

// ---------------------------------------------------------------------------
// One Cryptoki instance per library, reference counted.
//
// C_Initialize and C_Finalize are process-wide for a given module. Two
// signers on the same library that each finalised on Close would pull the
// module out from under each other.

type module struct {
	path string
	ctx  *pkcs11.Ctx
	refs int
	// owned is false when something else in the process had already
	// initialised the module; finalising it would be pulling it out from
	// under that owner.
	owned bool
}

var (
	modulesMu sync.Mutex
	modules   = map[string]*module{}
)

func loadModule(path string) (*module, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	if m, ok := modules[path]; ok {
		m.refs++
		return m, nil
	}
	ctx := pkcs11.New(path)
	if ctx == nil {
		return nil, fmt.Errorf("cannot load the PKCS#11 module %s", path)
	}
	owned := true
	if err := ctx.Initialize(); err != nil {
		var perr pkcs11.Error
		if !(errors.As(err, &perr) && uint(perr) == pkcs11.CKR_CRYPTOKI_ALREADY_INITIALIZED) {
			ctx.Destroy()
			return nil, fmt.Errorf("initialising %s: %w", path, err)
		}
		owned = false
	}
	m := &module{path: path, ctx: ctx, refs: 1, owned: owned}
	modules[path] = m
	return m, nil
}

func (m *module) release() {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	m.refs--
	if m.refs > 0 {
		return
	}
	delete(modules, m.path)
	if m.owned {
		_ = m.ctx.Finalize()
	}
	m.ctx.Destroy()
}
