# Brief for Claude Code: Nitro-enclave signing party (Phase 0)

Paste this into a Claude Code session on this repository. Background, the verified stack and the full
plan are in [`../NITRO-ENCLAVE-MPC-PARTY.md`](../NITRO-ENCLAVE-MPC-PARTY.md); read it first.

## Goal

Prove the three things every later phase depends on, on real AWS hardware:

1. a Nitro Enclave built from this repository returns a valid attestation document;
2. the host and the enclave exchange length-prefixed frames over vsock;
3. AWS KMS decrypts a test secret only for the measured enclave image, returning it encrypted to the
   enclave (`CiphertextForRecipient`), and refuses a rebuilt image whose hash differs.

No tss-lib code moves in this phase.

## Rules

- Do not change the behaviour of the existing `services/mpc-party` path. New code goes under
  `services/mpc-party/enclave/`, `services/mpc-party/cmd/`, and `infrastructure/nitro/`.
- Do not replace tss-lib, write a custom NSM client, or put Temporal or the gateway in the enclave.
- Libraries: `github.com/hf/nsm` (pinned by commit, behind an `Attester` interface); vsock through
  `golang.org/x/sys/unix` or `github.com/mdlayher/vsock`; AWS SDK for Go v2 for KMS. Read
  `github.com/edgebitio/nitro-enclaves-sdk-go` and `github.com/brave/nitriding` for patterns only.
- No silent fallback: in enclave mode, a failed attestation or unseal is an error, never a software path.
- Every security-relevant path gets a negative test.
- Small, reviewable commits. Documentation in the style of `docs/engineering/PKCS11-HSM-SIGNING.md`:
  say what it does, what it does not do, and how to check.

## Two constraints that shape later phases (do not implement yet, do not design against them)

- **The peer channel must end inside the enclave.** tss-lib's key generation sends each party its share
  of the secret in the clear inside a protocol message and requires end-to-end encryption. The host
  will only relay sealed, signed envelopes between enclaves (Phase 2). Do not build anything that has the
  host terminate peer TLS for protocol messages.
- **Ceremony authorisation (`authorizer.go`) moves inside the enclave** with the co-signer public key
  built into the measured image (Phase 2).

## Tasks

- [ ] `enclave/frame.go` + tests: 4-byte big-endian length, 1-byte type, payload; 16 MiB limit. Tests:
      round trip; truncated frame; oversize refused; unknown type refused.
- [ ] `enclave/vsock.go`: dial and listen on AF_VSOCK (enclave CID, host CID 3).
- [ ] `enclave/attest.go`: `type Attester interface { Attest(userData, nonce, publicKey []byte) ([]byte, error) }`;
      `NSMAttester` (build tag `nitro`) and `FakeAttester` (tests only; refuse to compile into a `nitro` build).
- [ ] `enclave/kms.go`: `type Unsealer interface { Unseal(ciphertext []byte) ([]byte, error) }`;
      `KMSUnsealer` generates an ephemeral RSA-2048 key in the enclave, calls `Decrypt` with
      `Recipient{AttestationDocument, KeyEncryptionAlgorithm: RSAES_OAEP_SHA_256}`, opens
      `CiphertextForRecipient` (PKCS#7 enveloped data) inside the enclave. The plaintext never crosses vsock.
- [ ] `cmd/mpc-party-enclave/main.go`: handles `Ping`, `Attest` (returns the document), `UnsealDemo`
      (returns only a SHA-256 of the plaintext as proof).
- [ ] `cmd/mpc-party-host/main.go`: starts or attaches to the enclave, sends the three frames, prints results;
      documents running `vsock-proxy` for `kms.<region>.amazonaws.com`.
- [ ] `infrastructure/nitro/Dockerfile.enclave`: static Go build; `nitro-cli build-enclave` → EIF; print PCR0/1/2.
- [ ] `infrastructure/nitro/kms-key-policy.json`: `kms:Decrypt` allowed only when
      `kms:RecipientAttestation:ImageSha384` equals the measured value; never all zeros (debug mode).
- [ ] `infrastructure/nitro/spike.sh`: build, run, ping, attest, unseal; then change one byte, rebuild,
      show the new PCR0 and that KMS refuses. Also show `Decrypt` without `Recipient` refused by the policy.
- [ ] `docs/evidence/YYYY-MM-DD-nitro-spike.md`: instance type, PCRs, outputs, both refusals, cost, teardown.

## Done when

All three goals pass on an enclave-enabled EC2 instance, the two refusals are shown, the existing
`services/mpc-party` tests still pass unchanged, and the evidence file is committed.

## Out of scope for this phase

tss-lib inside the enclave; peer envelopes; share sealing for real keys; Helm and Terraform changes;
SGX or other TEEs.
