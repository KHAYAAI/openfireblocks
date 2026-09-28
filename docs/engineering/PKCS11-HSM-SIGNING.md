# PKCS#11 / HSM signing: scoped

What a hardware security module backend actually is, what it would do
for this platform, what it would *not* do despite how it's usually
pitched, and the concrete increment of work to build it.

**Status: scoped, not started.** Written because it was asked for by
name. Nothing in this document has been built; every code reference
below is to what exists today, not to anything new.

---

## 1. What an HSM is, in plain terms

A hardware security module is a piece of hardware (a PCIe card, a USB
device, or a cloud service that presents the same interface — AWS
CloudHSM, Google Cloud HSM, Azure Managed HSM) whose one job is:
**a private key goes in once, and never comes back out.**

You ask it to generate a key pair. It gives you back the public half.
The private half lives inside the device's protected memory, encrypted
at rest by a master key that is itself generated and held inside the
device, never exported. From then on, you don't hand the HSM your key —
you hand it *data to sign*, and it hands back a *signature*. The private
key never transits your application's memory, your OS, your disk, or
the network. A `root` shell on the box the HSM is plugged into does not
get the key; at most it gets the ability to *ask the HSM to sign
something*, which is a materially smaller blast radius and one an HSM
can further restrict (PINs, quorum authentication, rate limits, audit
logging of every signing operation).

**PKCS#11** is the standard interface for talking to one. It's a C API
(`Cryptoki`) that every major HSM vendor implements as a shared library
(`.so` / `.dll`) you load and call: `C_GenerateKeyPair`, `C_Sign`,
`C_Encrypt`, and so on — a fixed, small catalogue of operations. That
fixed catalogue is the whole story of what this scoping document is
about.

## 2. What it is *for*, here specifically

Two different claims get bundled together whenever "HSM support" comes
up for a custody platform, and they need to be pulled apart because one
is easy and one is not possible with PKCS#11 at all.

### Claim A — "a signing key never touches software"

This is the classic PKCS#11 use case and it is exactly what PKCS#11
does. It applies to **any signer that holds one whole private key** and
signs with it directly.

That's `services/mpc-signer` today. `ResolveSigningKey`
(`services/mpc-signer/vault.go:35`) reads a **plaintext hex private
key** from Vault's KV store — or generates one and writes it back the
same way — and `NewMPCSigner` (`services/mpc-signer/signer.go:44`)
parses that hex string into an in-memory `*ecdsa.PrivateKey`. The raw
key is a Go string, `s.privKeyHex` (`services/mpc-signer/main.go:36`),
threaded through `SignMultiChain(ctx, signReq, s.privKeyHex)`
(`services/mpc-signer/main.go:134`) on every signing call. Vault
encrypts it at rest and TLS protects it in transit, but on the wire
between Vault and this process, and for the lifetime of the process,
it's a plaintext key in RAM. Anyone with a heap dump or a debugger
attached to that process has the whole key.

A PKCS#11 backend for this path is straightforward, well-trodden, and
what the rest of this document scopes.

### Claim B — "the threshold shares live in the HSM"

This is how the request usually gets phrased, and it does not work the
way it sounds. `services/mpc-party` runs real GG18/20 threshold ECDSA
via `bnb-chain/tss-lib` (see
[`docs/security/TSS-LIB-ADVISORY-REVIEW.md`](../security/TSS-LIB-ADVISORY-REVIEW.md)
for what's actually running). A threshold ceremony is not "hold a key,
sign with it" — during every DKG and every signing round, a party does
arithmetic *on its own raw share value*: Paillier encryption and
decryption with a locally-generated keypair, modular exponentiation
against another party's ciphertext (the MtA sub-protocol), Schnorr and
range-proof generation that consume the share as an exponent. This is
custom multiparty computation over a secret scalar, recomputed fresh in
every ceremony.

PKCS#11's catalogue does not have an operation for any of that. It has
`C_Sign` — hand me a digest, get back a signature, using the *whole* key
this token already holds. It does not have `C_MultiplyMyShareByYourNTilde`.
There is no vendor extension in general use that exposes raw scalar
arithmetic on an arbitrary secret from inside a PKCS#11 token, because
that's not what the standard, or the class of hardware behind it, is
built to do.

So: **"drop the threshold shares inside a PKCS#11 HSM" is not a smaller
version of hardware isolation for this platform — it's a different
project that PKCS#11 cannot do at all.** The two ways this actually gets
solved at custody vendors that claim it:

- **Run the ceremony code inside a hardware or firmware-enforced secure
  enclave** (Intel SGX, AWS Nitro Enclaves) rather than ordinary process
  memory. The share still does raw arithmetic in software — `tss-lib`
  doesn't change — but the *process* it runs in is attested, isolated
  from the host OS, and its memory cannot be read even by someone with
  root on the host. This is what Fireblocks' SGX-based architecture
  does. It's a real, large, separate piece of engineering: porting a Go
  ceremony to run inside an SGX enclave or a Nitro Enclave, with all the
  constraints that implies (no direct network access from inside a
  Nitro enclave without a vsock proxy; SGX's memory limits and the
  ecosystem's general unfriendliness to a large Go runtime). It is not
  scoped here, and building it well is genuinely months of work, which
  is exactly why `docs/LAUNCH-THESIS.md` lists it separately from this
  item.
- **Use the HSM to protect the thing that protects the shares at
  rest**, rather than the shares themselves during a ceremony — i.e.
  make it the root of Vault's own encryption rather than a party in the
  MPC math. Vault already supports auto-unseal against a hardware-backed
  key management service (`seal "awskms"`, `"gcpckms"`, `"azurekeyvault"`
  stanzas today). Vault OSS does not — as far as this repository's
  authors could establish without a HashiCorp sales conversation —
  support a generic on-prem PKCS#11 seal; that capability has
  historically been a Vault Enterprise HSM feature.
  **Not verified against current HashiCorp licensing; confirm before
  quoting it to a buyer**, the same caveat this repo applies to every
  claim it can't check from source. If it holds, "HSM-backed Vault
  auto-unseal" — which appears as an open item elsewhere in this
  platform's docs — is a Vault *deployment/licensing* question, not
  something this codebase needs to write Go for.

**What this document scopes is Claim A only**: a PKCS#11 `Signer` for
`services/mpc-signer`'s single-key path. It is real hardware isolation,
it is buildable now, and it should not be sold as "the MPC shares are in
an HSM" — because they aren't, and can't be with this interface.

## 3. Who wants Claim A, and why it's still worth building

A customer running `mpc-signer` in single-key mode today — no threshold
ceremony, one key per chain — currently has that key as plaintext in
Vault and in the signer's process memory. Some customers will not accept
that regardless of Vault's own protections, either because their
internal policy requires FIPS 140-2/3 Level 3 hardware for any signing
key, or because their regulator does. For those customers the answer
today is "no," and Claim A turns it into "yes, at extra cost, for keys
that don't need threshold signing." It does not touch, weaken, or
replace threshold signing for customers who use `mpc-party`; the two
paths stay independent, which is a design property worth keeping rather
than something to unify prematurely.

## 4. Where this actually plugs in, and why it's narrower than it looks

The obvious guess — that `s.privKeyHex` gets threaded through a couple
of call sites in `main.go` — is wrong, and it's worth showing the real
shape because it changes both the interface and the honest scope of
what "Ethereum, Bitcoin, Solana, Cosmos" means for hardware signing.

`chains.SignerRouter.SignMultiChain` (`services/mpc-signer/chains/router.go:26`)
dispatches by chain to one of four `ChainSigner` implementations
(`chains/ethereum.go`, `chains/bitcoin.go`, `chains/solana.go`,
`chains/cosmos.go`), all satisfying one interface
(`services/mpc-signer/chains/types.go:19`):

```go
type ChainSigner interface {
	SignMessage(ctx context.Context, messageHash []byte, privKey string) (*Signature, error)
	// ...
}
```

Every implementation is handed the same raw hex (or, for Solana,
hex-or-base58) private key **on every call** and independently parses
it: `ethcrypto.HexToECDSA` for Ethereum and Cosmos,
`decodeBitcoinPrivKey` into a `btcec.PrivateKey` for Bitcoin,
`solanaPrivateKey` into an `ed25519.PrivateKey` for Solana. None of them
hold key state; the router is effectively stateless and re-derives the
key from a string every signing call.

That's actually the good news for this scope: it means the real
interface change is **one method signature**, not four independent call
sites doing unrelated things. `SignMessage`'s `privKey string` parameter
becomes a `Signer`, and each implementation stops parsing hex and starts
calling `Sign` on whatever it was handed.

**The genuine complication is the curve, not the plumbing.** Ethereum,
Bitcoin and Cosmos are all secp256k1 here — three different signature
*encodings* (Ethereum's `[R||S||V]`, Bitcoin's DER-or-compact via
`btcec`, Cosmos's own convention) over the same curve, which a
PKCS#11-backed secp256k1 signer can serve uniformly: sign the digest,
re-encode per chain, same as the software path does today. **Solana is
Ed25519**, a different curve family, signing the raw message rather
than a hash of it (`chains/solana.go`'s own comment is explicit about
this). PKCS#11 v3.0 added standard Ed25519 mechanisms
(`CKM_EDDSA`); PKCS#11 v2.40 — what a meaningful share of deployed HSMs
and cloud HSM services still speak — did not, and even where a
mechanism exists, vendor firmware support for it is inconsistent enough
that it has to be checked per target device rather than assumed.

So: **scope the secp256k1 path (Ethereum, Bitcoin, Cosmos) as the
buildable increment. Treat Solana/Ed25519-over-PKCS#11 as a per-vendor
question to answer when a specific HSM is in hand**, not a blocking
unknown for this estimate. A customer who needs Solana in hardware today
gets an honest "confirm your HSM speaks `CKM_EDDSA`" rather than a
silent gap.

```go
// services/mpc-signer/signer.go — new.

// Signer produces a secp256k1 signature over a pre-hashed digest for a
// key it controls, without necessarily exposing that key. Deliberately
// this narrow: it's the one operation every backend (raw key, PKCS#11,
// someday a cloud KMS) implements identically, and per-chain signature
// encoding stays exactly where it already lives -- in each ChainSigner.
type Signer interface {
	PublicKey() *ecdsa.PublicKey
	SignDigest(ctx context.Context, digest [32]byte) ([]byte, error) // raw (r, s), no recovery id yet
}
```

Two implementations satisfy it: the existing raw-key path, extracted
rather than rewritten, and the new PKCS#11 path.

```go
// services/mpc-signer/signer.go — existing parsing logic, same math,
// now behind Signer instead of being the only option each ChainSigner
// duplicates.
type rawKeySigner struct{ privKey *ecdsa.PrivateKey }

// services/mpc-signer/pkcs11_signer.go — new.
type pkcs11Signer struct {
	ctx      *pkcs11.Ctx // github.com/miekg/pkcs11
	session  pkcs11.SessionHandle
	keyLabel string
	pub      *ecdsa.PublicKey // cached at construction; PKCS#11 exposes this cheaply
}

func (s *pkcs11Signer) SignDigest(ctx context.Context, digest [32]byte) ([]byte, error) {
	// C_SignInit + C_Sign against the token-resident private key,
	// mechanism CKM_ECDSA. The digest is pre-hashed on our side --
	// PKCS#11's CKM_ECDSA mechanism signs exactly 32 bytes, it does not
	// hash for you, which is correct since Ethereum's hash (Keccak-256)
	// is not what a generic HSM mechanism would apply anyway.
	//
	// PKCS#11 returns a raw (r, s) pair, not Ethereum's compact
	// [R||S||V] form -- V (the recovery id) has to be computed on our
	// side by trying both candidate recovery values against the known
	// public key, since the token does not report it. This is the one
	// genuinely fiddly part of the implementation and needs a test
	// vector, not just a happy-path check.
}
```

`ResolveSigningKey` becomes `ResolveSigner`, returning a `Signer`
instead of a hex string; it picks the PKCS#11 path when
`HSM_PKCS11_LIBRARY` (and the label/PIN env vars below) are set, and
falls back to the existing Vault-or-generate path otherwise — matching
this codebase's established convention of an env-var-gated fallback
that fails loudly on partial configuration rather than silently picking
the weaker path (see `services/mpc-party/authorizer.go`'s
`AuthorizerFromEnv` for the pattern this should copy: a config knob
that's either fully off or fully validated at startup, never
half-configured and permissive).

`main.go:134`'s `s.signerRouter.SignMultiChain(ctx, signReq,
s.privKeyHex)` passes the resolved `Signer` through instead of the raw
hex string; `SignMultiChain` (`chains/router.go:26`) passes it straight
to whichever `ChainSigner.SignMessage` it dispatches to, same as it
passes the string today. `EthereumSigner`, `BitcoinSigner` and
`CosmosSigner` each replace their `HexToECDSA`-or-equivalent parsing
with a call to `Signer.SignDigest` and keep their existing per-chain
encoding of the result unchanged. `SolanaSigner` is untouched by this
scope — see §2's curve caveat — and keeps taking a raw key exactly as
it does now, so a deployment with no Ed25519-capable HSM loses nothing
it has today.

## 5. Configuration

```
HSM_PKCS11_LIBRARY   # path to the vendor's .so, e.g. /usr/lib/softhsm/libsofthsm2.so
HSM_PKCS11_SLOT       # numeric slot id, or HSM_PKCS11_TOKEN_LABEL to look it up by label
HSM_PKCS11_PIN        # the token's user PIN — a secret, mounted the same way
                      # VAULT_TOKEN is: never a plain env var in the chart, always
                      # a Secret volume or secretKeyRef, matching the precedent
                      # ceremonyAuthorizer.keySecret already set in this chart
HSM_PKCS11_KEY_LABEL  # which key pair on the token to use
```

Same posture as `CEREMONY_AUTHORIZER_PUBKEY` and Vault's own
`VAULT_ADDR`: absent means the feature is off and the existing behaviour
is unchanged; present-but-broken (library won't load, PIN rejected, key
label not found) is `log.Fatalf` at startup, not a fallback to the
software key. A deployment that believes its key is in hardware and
isn't is a worse failure than one that knows it configured nothing.

## 6. What this costs the build, and why it's contained

**Every Dockerfile in this repository builds `CGO_ENABLED=0`** — 12 of
12, checked directly. That's deliberate: it's what makes
`gcr.io/distroless/static` possible, which is what satisfies this
chart's `readOnlyRootFilesystem` / `runAsNonRoot` posture with no
package manager and no shell in the running image (see
`services/policy-service/Dockerfile`'s own comment on exactly this).

PKCS#11 breaks that, unavoidably. `miekg/pkcs11` (the only mature,
actively used Go PKCS#11 binding) works by `dlopen`-ing the vendor's
`.so` at runtime via cgo — there is no pure-Go PKCS#11 client, because
PKCS#11 is a C ABI and the vendor library is the only thing that speaks
to the actual hardware or cloud HSM endpoint. This is not a library
choice this project can route around.

The containment: **this changes `services/mpc-signer`'s Dockerfile
only**, not the other 11. It becomes `CGO_ENABLED=1`, built against a
base image with `libc` (not distroless-static — distroless has a
`:base` variant with glibc but no package manager, which still works
for a cgo binary that only needs `libdl`/`libc`, so it stays close to
the existing security posture rather than jumping to a full Debian
image). The vendor's PKCS#11 `.so` — or SoftHSM2's, for anything that
isn't real hardware — has to be present in the image or mounted in,
which is itself vendor-specific and belongs in that vendor's own
deployment doc, not this one.

Everything downstream of this service — the chart, the other services,
CI's `go mod why` LGPL gate — is unaffected. `mpc-party`'s threshold
ceremonies do not import this package and are not rebuilt differently.

## 7. Testing, without a real HSM

[SoftHSM2](https://github.com/opensc/SoftHSM2) is a software PKCS#11
token used industry-wide for exactly this: it speaks the real PKCS#11
protocol against a real `miekg/pkcs11` client, so a test against it
exercises the actual `pkcs11Signer` code path — the same argument this
repository already made for why `services/mpc-party/vault_fake_test.go`
exists instead of every Vault-touching test skipping everywhere. It is
not a mock; it's a different implementation of the same standard,
running in software.

CI adds one job: install `softhsm2` + `libsofthsm2`, initialize a
token, generate a test EC keypair on it, run
`services/mpc-signer/pkcs11_signer_test.go` against it — sign a known
digest, verify the signature against the public key PKCS#11 reports,
confirm the recovery-id computation picks the right one of the two
candidates. That last part is the one piece of this whole feature most
likely to have a subtle bug, so it gets the most explicit test coverage,
the same way this repository's Ethereum signature work already pins 25
golden signature vectors rather than trusting one happy-path test (see
`docs/engineering/GO-ETHEREUM-REMOVAL.md`).

Real hardware or a cloud HSM (CloudHSM, Cloud HSM, Managed HSM) gets
exercised only by whichever customer deploys against one — the same
posture `stripe_live_test.go` already takes toward real Stripe: proven
against the standard in CI, proven against the real thing only where a
credential for the real thing exists, and the gap between those two
stated rather than implied.

## 8. What is explicitly out of scope here

- **Threshold shares inside an HSM.** Not possible with PKCS#11; see
  §2, Claim B. If this is what's actually wanted, the scoping work
  needed is an SGX/Nitro enclave port of the ceremony code, which is a
  different document and a much larger estimate.
- **HSM-backed Vault auto-unseal.** Likely a Vault Enterprise licensing
  question rather than code this repository writes — confirm before
  committing to it.
- **Touching `services/mpc-party` at all.** Zero changes to any
  ceremony, any curve, any drill. This is additive to `mpc-signer`'s
  existing single-key path only.
- **Key generation policy** (does the HSM generate the keypair, or is
  an existing key imported into it) — vendor- and compliance-dependent,
  belongs in the eventual customer-facing runbook, not this scope.
- **Solana / Ed25519 signing on the HSM.** `SolanaSigner` keeps taking a
  raw key under this scope; see §2 and §4. Revisit per target vendor.

## 9. Estimate and sequencing

**~1–2 weeks** for the `Signer` interface, the PKCS#11 implementation,
rewiring `EthereumSigner`/`BitcoinSigner`/`CosmosSigner` off raw-hex
parsing and onto it, the SoftHSM2 CI job, and the golden-vector-style
recovery-id tests. This is firmer than a first guess would be, because
§4 above is the actual read of `chains/router.go` and every
`ChainSigner` implementation, not an assumption about them — the one
real unknown going in (how many places independently touch the raw key)
turned out to be answerable by reading four files, and the answer is
"one interface method, three of four implementations."

Where it sits against everything else already queued, unchanged from
`docs/LAUNCH-THESIS.md`: **not the blocker.** Party isolation is the one
item standing between here and real money and costs nothing but
deployment time; the cryptographic review has the longest lead time and
should be commissioned regardless of what else happens. This item is
real, buildable, and closes a documented gap — but it answers a question
("can our signing key live in hardware") that, per that same document,
no design partner has asked yet. Build it when one does, or now if
there's a specific reason to have the answer ready before being asked.
