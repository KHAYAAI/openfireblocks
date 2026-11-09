# Signing parties inside AWS Nitro Enclaves

**Status: proposal, nothing built.** Written 2026-11-09. This document assesses the plan to run each
threshold signing party (`services/mpc-party`) inside an AWS Nitro Enclave, checks the proposed stack
against the code and the libraries' own sources, and turns it into a phased plan with a file-level
first step. The paste-ready brief for Claude Code is [`nitro/CLAUDE-CODE-BRIEF.md`](nitro/CLAUDE-CODE-BRIEF.md).

## Why

Today a party's key share lives in that party's process memory while it signs, and at rest in Vault's
key-value store, protected by Vault's storage encryption (or an AES-256-GCM file when there is no
Vault). Anyone with root on the party's host can read the process memory. Separate hosts under separate
operators ([`../deployment/SEPARATE-HOSTS-PLAYBOOK.md`](../deployment/SEPARATE-HOSTS-PLAYBOOK.md)) make
that one operator's host per share; an enclave removes the host from the trust boundary altogether. The
share is decrypted only inside a measured enclave image, and the cloud key that decrypts it is released
only to that image.

This is the "hardware isolation for the threshold shares" item the README lists as blocking material
balances. It complements separate hosts; it does not replace them.

## 1. Recommended stack

Keep the cryptography; add the thinnest enclave layer the ecosystem offers. Each row says how far it was
checked on 2026-11-09.

| Component | Choice | Role | Checked |
|---|---|---|---|
| Threshold protocol | `bnb-chain/tss-lib` v2 (already in the repo) | DKG, signing, refresh; ECDSA and EdDSA | In use; 2-of-3 proven on four public test networks |
| Language | Go | Same as `mpc-party` today | n/a |
| Nitro Security Module client | [`github.com/hf/nsm`](https://github.com/hf/nsm) | Attestation documents, PCRs, entropy from inside the enclave | Exists; MIT; small and lightly maintained (no releases). A fork exists at `github.com/0xsequence/nsm`. Pin by commit and vendor it |
| Attestation verification | [`github.com/hf/nitrite`](https://github.com/hf/nitrite), or [`github.com/0xsequence/tee-verifier`](https://github.com/0xsequence/tee-verifier) | Verify a peer enclave's attestation document (certificate chain to the AWS Nitro root, PCRs) | `hf/nitrite` exists; `tee-verifier` exists and supports Nitro only |
| Higher-level SDK (optional) | [`github.com/edgebitio/nitro-enclaves-sdk-go`](https://pkg.go.dev/github.com/edgebitio/nitro-enclaves-sdk-go) | KMS calls with attestation from inside the enclave; uses `hf/nsm` | Exists. Read before adopting; do not take a dependency for one helper |
| Patterns only | `0xsequence/nitrocontrol`, `brave/nitriding` | Reference for enclave bootstrap, KMS envelope encryption, vsock plumbing | `nitrocontrol` seen only through a mirror, **not confirmed on github.com**; `nitriding` exists. Read for patterns, do not depend on them |
| Ground truth | AWS Nitro CLI, `aws-nitro-enclaves-nsm-api`, `vsock-proxy` | Build the enclave image (EIF), read PCRs, proxy KMS traffic | Official AWS tooling |
| Key release | AWS KMS with `Recipient` attestation | `Decrypt` returns `CiphertextForRecipient`, encrypted to the enclave's own key; the key policy's `kms:RecipientAttestation:ImageSha384` / `:PCR<n>` conditions pin the image | [AWS KMS docs](https://docs.aws.amazon.com/kms/latest/developerguide/conditions-nitro-enclaves.html) |

Dropped from the original brief: `forhead/nitro-examples` and an Anchorage `awsnitroverifier`. Neither
could be found; do not plan around them.

**Avoid in v1:** replacing tss-lib, writing our own NSM client, putting the Temporal worker or the
gateway inside the enclave, and SGX.

## 2. Architecture

```
 Customer AWS account, one party per account (separate-hosts playbook)
 ┌──────────────────────────────────────────────────────────────────────────┐
 │  EC2 parent instance (host)                                             │
 │  ┌──────────────────────────────┐   vsock    ┌─────────────────────────┐ │
 │  │ mpc-party-host               │◄──────────►│ Nitro Enclave            │ │
 │  │  · HTTPS listener for the    │  framed,   │ mpc-party-enclave        │ │
 │  │    orchestrator and peers    │  opaque    │  · tss-lib DKG/sign/     │ │
 │  │  · relays SEALED envelopes;  │  bytes     │    refresh               │ │
 │  │    cannot read them          │            │  · share only in here    │ │
 │  │  · stores sealed share blobs │            │  · verifies co-signer    │ │
 │  │    (Vault / file)            │            │    authorisation         │ │
 │  │  · vsock-proxy → KMS         │            │  · peer channel keys,    │ │
 │  │  · health, metrics, logs     │            │    attested              │ │
 │  └──────────────────────────────┘            │  · NSM attestation       │ │
 │                                              └─────────────────────────┘ │
 │  KMS key (this account): Decrypt allowed only with a Recipient           │
 │  attestation whose ImageSha384 matches the released enclave image        │
 └──────────────────────────────────────────────────────────────────────────┘
          ▲  peer envelopes: encrypted and signed enclave-to-enclave
          ▼
   other parties (same pattern, other accounts)
```

**What lives where**

| Inside the enclave | On the host |
|---|---|
| tss-lib state machines and the plaintext share | The network: HTTPS from the orchestrator and peers |
| Peer channel keys and the code that seals and opens every protocol message | Relaying sealed envelopes it cannot read |
| Ceremony authorisation check (co-signer public key built into the measured image) | Storing sealed share blobs in Vault or the file store |
| Unsealing the share: KMS `Decrypt` with `Recipient`, through vsock-proxy, TLS terminating inside | vsock-proxy to KMS; health, metrics, audit logs |
| Pre-parameter (safe prime) generation, entropy from the NSM | Lifecycle: start, stop, restart the enclave |

## 3. Five corrections to the original brief

1. **The peer channel must end inside the enclave, not at the host.** The brief has the host doing
   "mTLS to peers". tss-lib's key-generation round 2 sends each recipient its share of the secret in the
   clear (`NewKGRound2Message1(to, from, share, proof)`), and refresh does the same; tss-lib's README
   requires end-to-end encryption between parties. Today that is the mutual TLS in `mtls.go`, with the
   sender taken from the client certificate (`peer_identity.go`). If the host terminated that TLS, the
   host would read the shares during every DKG and refresh, and the enclave would protect nothing. Each
   enclave needs its own channel key pair, bound to its attestation; every protocol message is sealed
   (AEAD) to the recipient enclave and signed by the sender inside the enclave. The host only relays.
   `peer_identity.go` already names per-message signatures as "the right next step".
2. **Ceremony authorisation moves inside.** `authorizer.go` refuses any ceremony the co-signer has not
   signed. On the host, a compromised host would skip it. The check, and the co-signer public key, belong
   in the measured enclave image.
3. **Recovery without the vendor has to survive PCR binding.** A share that only an exact image can
   decrypt is lost if that image cannot be rebuilt, the KMS key is deleted, or the region is gone. The
   plan needs reproducible enclave builds (same source, same image hash), a multi-region KMS key with
   deletion protection, and a documented break-glass path held by the customer's recovery-key holders
   (`../deployment/KEY-RECOVERY.md`). Phase 1 includes a recovery drill that rebuilds the image from
   source and unseals with it.
4. **Today's isolation is per process, in one cluster.** The three parties run as separate pods on one
   staging cluster. Separate AWS accounts are built in Terraform and not yet applied. Enclaves build on
   that work; the rehearsal comes first.
5. **Shares at rest are Vault-encrypted, not envelope-encrypted.** They sit in Vault KV v2 under Vault's
   storage encryption, or in an AES-256-GCM file. Enclave mode adds an envelope: a data key wrapped by
   the party's KMS key, so Vault (and the off-site backup) only ever hold ciphertext the host cannot open.

## 4. Where FORGE stands

| Area | Today | Gap to an enclave party |
|---|---|---|
| Threshold signing (tss-lib) | DKG, signing and refresh working; 2-of-3 proven on four public test networks; recovery drilled in CI | Run inside the enclave |
| Party isolation | Separate pods in one cluster; three-account Terraform built, not applied | Enclave boundary per party, on top of separate accounts |
| Peer channel | Mutual TLS, sender bound to the client certificate | End-to-end sealed and signed messages, terminating in the enclave |
| Ceremony authorisation | Co-signer check in every party, on by default | Same check, inside the measured image |
| Share at rest | Vault KV v2 (Vault storage encryption) or AES-256-GCM file | KMS-wrapped envelope released only to the measured image |
| Single-key HSM (PKCS#11) | Built; tested against SoftHSM2; not run on hardware | Independent; keep it |
| Recovery | 41 min 42 s rebuild from the off-site backup; 23 of 23 keys restored in one command | Re-attest and unseal after a rebuild; reproducible image |
| Attestation, Nitro | None | Greenfield |

## 5. Phased plan

Estimates assume one engineer with Claude Code; they are planning figures, not commitments.

| Phase | What | Done when | Estimate |
|---|---|---|---|
| **0. Spike** | Enclave returns an attestation document; host ↔ enclave framed messages over vsock; KMS `Decrypt` with `Recipient` succeeds for the right image and is refused for a changed one | All three demonstrated on an enclave-enabled EC2 instance, with the negative cases | 1–2 weeks |
| **1. Share lifecycle** | Envelope-seal a share in the enclave; restart → re-attest → unseal; reproducible image build; recovery drill from source | Restart and rebuild both unseal; wrong image, wrong account and revoked image all refused | 3–5 weeks |
| **2. Ceremonies** | Peer channel keys and sealed, signed envelopes; tss-lib DKG, signing and refresh inside; authorisation inside; host relays only | A 2-of-3 key made and used across three enclaves signs for its address; a host-memory capture during DKG contains no share | 5–8 weeks |
| **3. Hardening** | Published image measurements, feature flag (enclave off by default), metrics, recovery drill (enclave edition), customer documentation in the style of `PKCS11-HSM-SIGNING.md` | Independent review of the design; drill passes in CI where possible and on AWS | 3–5 weeks |

The original brief put Phase 2 at 4–7 weeks; it is larger once the peer channel moves inside.

## 6. Phase 0, file by file

New code lives beside the existing service and changes nothing in the non-enclave path.

```
services/mpc-party/enclave/
  frame.go          length-prefixed frames: 4-byte big-endian length, 1-byte type, payload; max 16 MiB
  frame_test.go     round trip, truncated frame, oversize frame refused, unknown type refused
  vsock.go          Dial/Listen over AF_VSOCK (golang.org/x/sys/unix or github.com/mdlayher/vsock)
  attest.go         interface Attester { Attest(userData, nonce, publicKey []byte) ([]byte, error) }
                    NSMAttester (hf/nsm, enclave only) and FakeAttester (tests, never in a release build)
  attest_test.go    FakeAttester output parses; NSMAttester build-tagged out of normal tests
  kms.go            interface Unsealer { Unseal(ciphertext []byte) ([]byte, error) }
                    KMSUnsealer: ephemeral RSA key in the enclave, Decrypt with Recipient, opens
                    CiphertextForRecipient; never returns the plaintext to the host
cmd/mpc-party-enclave/main.go   enclave entrypoint: ping, attest, unseal-demo; no tss-lib yet
cmd/mpc-party-host/main.go      host entrypoint: starts the enclave, sends frames, runs vsock-proxy for KMS
infrastructure/nitro/
  Dockerfile.enclave             static Go build → EIF via nitro-cli build-enclave
  kms-key-policy.json            Decrypt allowed only with kms:RecipientAttestation:ImageSha384 = <measured>
  spike.sh                       build EIF, print PCRs, run enclave, ping, attest, unseal, then
                                 rebuild with one byte changed and show KMS refuses
docs/evidence/YYYY-MM-DD-nitro-spike.md   PCRs, outputs, the refusal, instance type, cost
```

**Interfaces Phase 1 and 2 build on:** `Attester`, `Unsealer`, the frame types (`Ping`, `Attest`,
`Unseal`, later `CeremonyStart`, `PeerEnvelope`, `Result`), and a `ShareStore` that holds only sealed
blobs on the host.

**Phase 0 negative tests:** a frame over the size limit, an unknown frame type, KMS `Decrypt` without an
attestation, and with an attestation from a rebuilt image whose hash differs. Each must be refused.

**Infrastructure for the spike:** an enclave-enabled EC2 instance (for example `m6i.xlarge` with
`--enclave-options Enabled=true`), the Nitro CLI and allocator, and a KMS key in the same account.
A few dollars a day; tear it down after.

## 7. Risks and open questions

| Risk | Handling |
|---|---|
| `hf/nsm` is small and unmaintained | Pin by commit, vendor it, keep it behind the `Attester` interface so it can be swapped |
| Losing the ability to unseal (image drift, deleted key, region loss) | Reproducible builds; multi-region KMS key with deletion protection; break-glass path with the customer's recovery-key holders; drill it in Phase 1 |
| Enclave CPU and memory for tss-lib pre-parameters | Size the enclave (start at 2 vCPU, 4 GiB); keep the pre-parameter pool; measure in Phase 2 |
| Side channels and host-level denial of service | Out of scope for Nitro's guarantees: the host can stop the enclave, not read it. Say so in customer docs |
| Debug mode | Images run with `--debug-mode` have zeroed PCRs; the KMS policy must never allow PCR0 of all zeros |
| Lock-in to AWS | Documented; the `Attester`/`Unsealer` interfaces leave room for another TEE later |

**Decision point after Phase 1:** proceed to Phase 2 at full speed, or adjust. Separate hosts under
separate operators remain the higher priority for capped mainnet.

## Sources

- tss-lib README, "How to use this securely", and `ecdsa/keygen/messages.go` (`NewKGRound2Message1`), in
  the module cache for `github.com/bnb-chain/tss-lib/v2@v2.0.0`
- [`hf/nsm`](https://github.com/hf/nsm); [`hf/nitrite`](https://github.com/hf/nitrite);
  [`0xsequence/tee-verifier`](https://github.com/0xsequence/tee-verifier);
  [`edgebitio/nitro-enclaves-sdk-go`](https://pkg.go.dev/github.com/edgebitio/nitro-enclaves-sdk-go);
  [`brave/nitriding`](https://github.com/brave/nitriding)
- AWS: [KMS condition keys for Nitro Enclaves](https://docs.aws.amazon.com/kms/latest/developerguide/conditions-nitro-enclaves.html);
  [Using cryptographic attestation with KMS](https://docs.aws.amazon.com/enclaves/latest/user/kms.html);
  [How to call KMS APIs for a Nitro enclave](https://docs.aws.amazon.com/kms/latest/developerguide/how-nitro-enclaves.html)
- This repository: `services/mpc-party/{mtls.go,peer_identity.go,authorizer.go,vault_seal.go,file_seal.go}`
