# PKCS#11 / HSM signing

What a hardware security module backend is, what it does for this
platform, what it does *not* do despite how it's usually pitched, and
how to run it.

**Status: built (Claim A below), tested against SoftHSM2 in CI.** Not yet
run against a physical HSM or a cloud HSM — see §7 for exactly what that
leaves open. §1–3 are the reasoning, unchanged from the scoping; §4
onwards describes what was built, including where it deliberately
departs from the scope.

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

## 4. What was built

```
services/mpc-signer/
  keys/keys.go            KeySigner interface, RawKeySigner, RecoverableSignature
  keys/pkcs11.go          PKCS11Signer, GeneratePKCS11Key       (//go:build pkcs11)
  keys/pkcs11_stub.go     the same names, refusing              (//go:build !pkcs11)
  keys/pkcs11_config.go   HSM_PKCS11_* parsing, in both builds
  chains/keysigned.go     SignMessageWithKey (ETH, BTC, Cosmos),
                          SignTransactionInputWithKey (BTC P2PKH),
                          SignerRouter.SignMultiChainWithKey
  keysource.go            chooses hardware or software at startup
  cmd/hsm-key             generate / show the key on the token
```

**The interface** is one operation, the one every signer here actually
needed:

```go
type KeySigner interface {
	PublicKey() []byte                                           // 0x04 || X || Y
	SignDigest(ctx context.Context, digest []byte) ([]byte, error) // [R || S || V], low S, V in {0,1}
	Describe() string                                            // "PKCS#11 key "treasury" on token ..."
}
```

`MPCSigner` (the `/sign` Ethereum path) now holds a `KeySigner` instead
of an `*ecdsa.PrivateKey`. Software mode wraps the same key it always
did in `RawKeySigner`; nothing about that path changed.

**The fiddly part — turning a token's bare `R || S` into something a
chain accepts — lives in exactly one function**, `RecoverableSignature`:

- **Low S.** ECDSA is malleable; Ethereum (EIP-2) and Bitcoin (BIP-62/146)
  reject the high-S twin. An HSM picks whichever S its arithmetic lands on,
  about half the time the one every node refuses. It is folded here.
- **The recovery id.** PKCS#11 has nowhere to report V. Both candidates
  are tried and the one that recovers the expected public key is kept —
  and if *neither* does, the token signed with a different key from the
  one it reported, and the signature is refused rather than returned.

**The PKCS#11 signer refuses to start** unless all of these hold:
exactly one private and one public key object carry the label (no
guessing between duplicates); the curve is secp256k1; the private key is
`CKA_SENSITIVE` and not `CKA_EXTRACTABLE` (a key the token will hand out
isn't a hardware key in any sense that matters); and a self-test
signature at startup recovers to the public key, which catches two key
pairs sharing a label or a public object left behind by a rotation. On a
lost session (HSM restart, network HSM drop) it reconnects once, and
refuses to continue if the key under the label changed while it was
away.

### Where the build departs from the scope, and why

- **Added alongside `SignMessage`, not replacing its signature.** The
  scope said change `ChainSigner.SignMessage(…, privKey string)` to take
  a signer. The build adds `SignMessageWithKey` on the three secp256k1
  chains instead. Two reasons: Solana (Ed25519) can't implement it and
  would have been dragged into a change it has no part in; and keeping
  the raw-key methods untouched makes them an oracle. For the same key
  and message, the new methods are tested **byte-identical** to the old
  ones across 21 keys × 3 chains, and Bitcoin spends signed through the
  new path are byte-identical *and* pass btcd's script engine with
  standard verification flags (low S, strict DER).
- **Solana is refused, not quietly signed in software.** In hardware
  mode `/sign-multi-chain` answers Solana requests with an error naming
  the chain. A deployment configured for hardware signing believes its
  keys are in hardware; signing one chain with a software key would make
  that false without telling anyone.
- **The service never creates a key.** A mistyped `HSM_PKCS11_KEY_LABEL`
  would otherwise mint a new key on first start, and the first anyone
  would hear of it is a deposit sent to the old address. `hsm-key
  generate` creates it, once, and refuses if the label exists.
- **Hardware signatures are not byte-identical to software ones.** The
  scoping implied they could be. They can't: the software path is RFC
  6979 deterministic, and `CKM_ECDSA` on most tokens (SoftHSM included)
  uses a random nonce. Both are valid ECDSA. Tests check what a chain
  checks — verifies, low S, recovers to the right sender — and that an
  imported known key gives the same address in hardware as in software.
- **HSM mode refuses a software key alongside it.** `VAULT_ADDR` or
  `MPC_SIGNER_PRIVATE_KEY` set together with `HSM_PKCS11_*` is fatal at
  startup, and the chart refuses to render it. Not because the software
  key would be used — it wouldn't — but because a deployment carrying
  both is one where somebody believes the key is in hardware while a key
  sits in Vault.
- **`distroless/cc`, not `distroless/base`.** Vendor PKCS#11 modules
  commonly link libstdc++; `cc` carries it and still has no shell or
  package manager.

## 5. Running it

```
HSM_PKCS11_LIBRARY      vendor module path inside the container
HSM_PKCS11_TOKEN_LABEL  token by label (preferred; slot numbers can move) ...
HSM_PKCS11_SLOT         ... or by slot number — exactly one of the two
HSM_PKCS11_PIN          user PIN; in the chart always a secretKeyRef
HSM_PKCS11_KEY_LABEL    the key pair's CKA_LABEL
```

None set: software mode, unchanged. All set: hardware mode. Anything in
between — or any of them handed to the default cgo-free image — is a
startup failure that names what to fix, never a fallback.

**Build:** `docker build --target pkcs11 services/mpc-signer` (cgo,
`distroless/cc`, ships `mpc-signer` and `hsm-key`). The default target
is unchanged: static, cgo-free.

**Chart** (`mpcSigner.hsm` in `values.yaml`):

```yaml
mpcSigner:
  hsm:
    enabled: true
    library: /opt/cloudhsm/lib/libcloudhsm_pkcs11.so
    tokenLabel: hsm1
    keyLabel: treasury
    pinSecret: openfireblocks-secrets   # key: hsm-pkcs11-pin
    volumes: [...]        # the vendor's module and its client config
    volumeMounts: [...]
```

The image tag defaults to `<tag>-pkcs11`. The vendor module is mounted,
not baked in: it's licensed, versioned and configured by the vendor.

**First key:**

```
kubectl exec deploy/<release>-mpc-signer -- hsm-key generate
kubectl exec deploy/<release>-mpc-signer -- hsm-key show
curl .../address     # {"address": "0x…", "keySource": "PKCS#11 key \"treasury\" on token \"hsm1\", …"}
```

`keySource` is how an operator confirms from outside the pod that a
hardware deployment is actually signing in hardware.

**Moving an existing software key into the HSM** is possible (the signer
accepts an imported key if it is sensitive and non-extractable) but not
recommended: the key existed in plaintext before it was imported, and
the HSM can't make that untrue. Generate a new key on the token and move
funds to its address.

## 6. How it's tested

`go test -tags pkcs11` against SoftHSM2 — the real PKCS#11 protocol
through the real `miekg/pkcs11` client, not a mock. The CI job
`hsm-pkcs11` sets `REQUIRE_SOFTHSM=1` so a runner without SoftHSM fails
instead of skipping. What's covered:

- a key generated on the token signs 64 digests, every one low-S and
  recovering to the token's public key;
- the private key's value cannot be read off the token;
- an imported known key has the same Ethereum address in hardware as in
  software, and hardware signatures verify under the software key;
- refusals: extractable key, missing key (and opening doesn't create
  one), duplicate labels, public/private mismatch, second `generate`
  over an existing label, wrong PIN (and the error never contains it),
  unknown token, non-32-byte digest;
- 8 goroutines × 25 signatures on one session;
- every session on the token closed underneath the signer — it
  reconnects and signs with the same key;
- end to end: all three chains through `SignMultiChainWithKey`, a
  two-input Bitcoin spend accepted by the script engine, and the service
  in hardware mode signing legacy and EIP-1559 Ethereum transactions,
  reporting `keySource`, and refusing Solana;
- in the default build: HSM configuration is refused with instructions
  to use the pkcs11 build; partial configuration is refused naming what
  is missing; the chart refuses HSM mode with Vault, without a key label,
  or with both token label and slot, and accepts slot 0.

Also run by hand here: the `pkcs11` service binary against a SoftHSM
token, `POST /sign` returning a signed Sepolia transaction from the
hardware key over HTTP.

## 7. What is not proven

- **A physical or cloud HSM.** SoftHSM proves this code speaks PKCS#11
  correctly. It can't prove a given vendor's quirks. The first
  deployment against real hardware should run `hsm-key generate`, `hsm-key
  show`, and one testnet transaction before anything else. Things that
  vary by vendor and are handled but unverified on hardware: secp256k1
  support (CloudHSM, Luna and YubiHSM 2 all document it; confirm on the
  specific firmware), `CKA_EC_POINT` as DER vs bare point (both
  accepted), `CKA_EC_PARAMS` exposed only on the public half (handled).
- **The Docker images.** The `pkcs11` target has not been built in this
  environment (no container runtime); the CI job builds it and checks
  the default image refuses HSM mode.
- **FIPS validation.** That is a property of the HSM, not of this code.
  Running against a FIPS 140-3 Level 3 device in FIPS mode is what a
  customer's compliance team will ask about; this code doesn't change
  it either way.

## 8. What is out of scope

- **Threshold shares inside an HSM.** Not possible with PKCS#11; see
  §2, Claim B. The route there is an SGX/Nitro enclave port of the
  ceremony code — a different, much larger project.
- **HSM-backed Vault auto-unseal.** Likely a Vault Enterprise licensing
  question rather than code this repository writes — confirm before
  committing to it.
- **`services/mpc-party`.** Zero changes to any ceremony, curve or
  drill.
- **Solana / Ed25519 on the HSM.** Refused in hardware mode (§4). Needs
  `CKM_EDDSA` (PKCS#11 v3.0) on the target device; revisit per vendor.

## 9. Where this sits

Unchanged from `docs/LAUNCH-THESIS.md`: **not the blocker.** Party
isolation is what stands between here and real money. This closes a
documented gap — "can our signing key live in hardware" now has the
answer "yes, for the single-key path on secp256k1 chains, with a
PKCS#11 HSM you supply" — before a design partner has asked.
