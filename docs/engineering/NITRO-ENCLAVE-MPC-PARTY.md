# Signing parties inside AWS Nitro Enclaves

**Status: built and tested in software; not yet run on Nitro hardware.** Updated 2026-11-10.

Enclave mode is implemented in `services/mpc-party` (`ENCLAVE_MODE=1`), with a host relay
(`cmd/mpc-party-host`), an attestation verifier shared with the orchestrator, and the AWS files in
`infrastructure/nitro/`. Every security path has a test against a software stand-in for the Nitro
Secure Module and for KMS. The one thing software cannot show is that real hardware and real KMS
behave the way the stand-ins do. [`infrastructure/nitro/spike.sh`](../../infrastructure/nitro/spike.sh)
shows that on an enclave-enabled EC2 instance and writes the evidence file. Until that file is
committed, treat enclave mode as unverified on hardware.

Off by default. With `ENCLAVE_MODE` unset, the party behaves exactly as before, and the existing tests
pass unchanged.

## Why

Without an enclave, a party's key share is in its process memory while it signs, and anyone with root on
its host can read it. Separate hosts under separate operators
([`../deployment/SEPARATE-HOSTS-PLAYBOOK.md`](../deployment/SEPARATE-HOSTS-PLAYBOOK.md)) limit that to one
share per operator. An enclave takes the host out of the trust boundary: the share is only ever in the
clear inside a measured enclave image, and the KMS key that decrypts it is released only to that image.

This is the "hardware isolation for the threshold shares" item the README lists as blocking material
balances. It complements separate hosts; it does not replace them.

## What it does

```
 One party per AWS account (separate-hosts playbook)
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │ EC2 parent instance                                                         │
 │ ┌──────────────────────────────┐  vsock  ┌──────────────────────────────────┐ │
 │ │ mpc-party-host               │◄───────►│ Nitro Enclave: mpc-party          │ │
 │ │  TCP :7000 ─────────────────►│  bytes  │  attested TLS server (vsock 5000) │ │
 │ │  vsock 7102 → party-2:7000   │  only   │  tss-lib DKG / sign / refresh     │ │
 │ │  vsock 7200 → vault:8200     │         │  co-signer check (key in image)   │ │
 │ │  vsock 8443 → kms.<region>   │         │  KMS envelope seal / open         │ │
 │ │  vsock 9000: settings, role  │         │  NSM attestation                  │ │
 │ │    credentials, reports      │         │                                   │ │
 │ └──────────────────────────────┘         └──────────────────────────────────┘ │
 │ KMS key for this party: Decrypt / GenerateDataKey only with a Recipient     │
 │ whose ImageSha384 is the released image, and only for forge:party=party-N   │
 └──────────────────────────────────────────────────────────────────────────────┘
```

1. **TLS ends inside the enclave.** tss-lib sends each party its share of the secret in the clear inside
   key-generation and refresh messages, and requires end-to-end encryption between parties. The host
   relays TCP byte for byte (`enclave/proxy.go`) and never holds a TLS key. The enclave generates its
   TLS key in enclave memory, in a self-signed certificate carrying an attestation document whose
   `public_key` is the certificate's key (`enclave/identity.go`). It mints a new key every hour.
2. **Peers accept only the same image, under the right role.** A party's own attestation tells it its
   PCR0 (the image hash). It requires every peer to present the same PCR0, never all zeros (debug
   mode), and the PCR3 (IAM role of the parent instance) pinned for the party number the peer claims
   (`nitroattest/`, `enclave/tls.go`). As a client, it also checks that the enclave it reached attests
   the party it dialled.
3. **Messages reach the party they are for.** In enclave mode a ceremony's peer list must give party N
   an address naming it (`https://party-N.<domain>`). With check 2, every connection then proves which
   party receives each share. Without this check, the orchestrator could list party 3's address for
   party 2, and party 3 would receive party 2's share. The co-signer's signature does not cover the
   peer list.
4. **Shares at rest are KMS envelopes.** On seal, the enclave asks KMS for a data key with a `Recipient`:
   an attestation document carrying an RSA key generated in the enclave. KMS checks the document
   against the key policy and returns the data key only encrypted to that RSA key. The share is
   encrypted with AES-256-GCM under it. Vault stores only the KMS-encrypted data key and the
   ciphertext. The encryption context (party, ceremony) is bound into both. A share that has sat in
   Vault in the clear is refused in enclave mode, and an envelope is refused outside it
   (`enclave/kms.go`, `vault_seal.go`).
5. **Settings that protect a share are in the image.** An enclave has no environment of its own, so
   everything in its environment comes from the image and is covered by PCR0:
   - the KMS key per party;
   - the IAM-role pins;
   - the co-signer public key;
   - the orchestrator CA.

   The host supplies only settings that cannot expose a share: the party number, peer and Vault
   addresses, and a Vault token (`enclave.RuntimeKeys`). The enclave refuses the host if it sends
   anything else.
6. **Nothing falls back.** Enclave mode refuses to start in any of these cases:
   - without the `nitro` build tag;
   - in debug mode;
   - without a co-signer key;
   - with `TSS_ALLOW_UNAUTHENTICATED_PEERS`, the file store or file-based mTLS set;
   - when the claimed party's role pin does not match the instance's PCR3.

   It also refuses to report a ceremony complete without Vault.
7. **The orchestrator verifies the enclaves.** In `services/temporal-worker`,
   `PARTY_ATTESTATION_POLICY_FILE` makes every connection to a `party-N` host require an attested
   enclave for party N. Other hosts keep their PKI check. The enclave accepts the orchestrator's PKI
   certificate, but refuses a PKI certificate that names a party.
8. **Start-up is reported.** A production enclave has no console. It reports started or failed, with the
   reason and its PCR0 and PCR3, to the host log over vsock. With `ENCLAVE_SELFTEST=1` baked in, it also
   seals and opens a test secret through KMS and reports the outcome.

## What it does not do

- **It does not make the image trustworthy.** PCR0 identifies an image. Whether that image came from a
  reviewed commit is answered by rebuilding it. The Go binary rebuilds byte for byte (checked: two
  clean builds, identical SHA-256). The EIF has not been checked for this, and the base images still
  have to be pinned by digest before production.
- **It does not stop whoever controls the KMS key policy.** An account principal with
  `kms:PutKeyPolicy` can rewrite the policy and decrypt. Put each party's key in that party's own
  account, restrict `PutKeyPolicy` to break-glass, and alert on it.
- **It does not stop the host from denying service.** The host can stop, starve or isolate the enclave.
  It cannot read the enclave's memory. Nitro does not claim side-channel resistance.
- **Migration does not undo past exposure.** Moving an existing key into enclave sealing (below) refreshes
  its shares and destroys the clear-text copies Vault holds. Old shares of the same key still
  reconstruct it, so anyone who already copied threshold-many of them still holds the key. These copies
  could come from Vault, a backup or a host's memory.
- **There is no image rollout or break-glass recovery tooling yet.** `ENCLAVE_EXTRA_IMAGE_PCR0` and a
  multi-value KMS condition allow two images at once. The procedure around them, a multi-region key,
  and recovery-key holders opening shares without the image are not built.
- **It is not deployed by Helm or Terraform.** Enclaves run on EC2, not in the Kubernetes pods the chart
  deploys. Instance, role and key provisioning are manual (see `spike.sh`).

## How to check

In software, which CI runs on every change:

```
cd services/mpc-party && go test ./nitroattest/... ./enclave/... ./cmd/... -run . && \
  go test -run 'Enclave' .
cd services/temporal-worker && go test ./internal/nitroattest/ ./activities/ -run 'Attest|CopyMatches'
```

| Test | Shows |
|---|---|
| `TestEnclaveModeKeygenSealRestoreSign` | Three simulated enclaves run a real Ed25519 DKG over attested TLS through the egress map; Vault holds only envelopes; every party restarts with empty memory, restores and signs; the signature verifies. A rebuilt image's sealer is refused by KMS; outside enclave mode an envelope is refused |
| `TestEnclaveModeRefusesAPartyUnderAnotherRole` | A party on an instance with another party's IAM role is refused at the handshake (PCR3) |
| `TestStartEnclaveRefusals` | Start-up refuses each of the following: debug mode, a party number that does not match its pinned role, an unpinned party, a missing co-signer, unauthenticated peers or the file store baked in, a host that sends the KMS key or co-signer key, a missing KMS key or egress map |
| `TestVerifyRefusals`, `TestVerifyCertificate` | The verifier refuses a debug-mode enclave, a rebuilt image, another kernel or application, another role, an unpinned party, stale or future documents, the wrong purpose, extra or missing claims, an untrusted root, tampered bytes, a certificate binding another key, and a common name that disagrees with the attestation |
| `TestServerClientPolicy` | The party server accepts attested parties and the orchestrator's PKI certificate, and refuses a PKI party certificate, another PKI name, another CA, and no certificate |
| `TestOpenRefusesAnotherImage`, `TestSealerRefusesAMisbehavingKMS` | KMS refuses a rebuilt or debug image; the sealer refuses a KMS answer with the data key in plaintext or without `CiphertextForRecipient` |
| `TestAttestedTransportRoutesByHostName` | The orchestrator refuses a party that presents only a PKI certificate, and still reaches other services by PKI |
| `TestCopyMatchesMPCParty` | The orchestrator's copy of the verifier is identical to the party's |

On hardware, which you run: `REGION=<region> infrastructure/nitro/spike.sh` on an enclave-enabled
instance. It builds the EIF and prints its PCRs, then shows:

1. KMS releasing a data key to the measured image;
2. KMS refusing the host that data key without attestation;
3. a one-byte rebuild with a new PCR0 being refused;
4. the debug-mode image refusing to start.

It then writes `docs/evidence/<date>-nitro-spike.md`.

## Moving existing keys into enclave sealing

A key generated before enclave mode has its shares in Vault in the clear (under Vault's storage
encryption). Enclave mode refuses to load such a share, with one exception: the migration.

```
forge-enclave-admin migrate -ceremony <id> -policy policy.json -cert orch.crt -key orch.key \
  -domain internal -port 7000 -parties 3 -authorizations auth.json
```

1. Every party loads its clear-text share into the enclave (`POST /tss/keygen/restore` with
   `migrate_legacy`). This needs a co-signer authorisation for the operation `migrate`, which is
   separate from `restore`, so a restore authorisation cannot be replayed to do it. The key is
   then blocked from signing.
2. Every party refreshes the key inside its enclave (`POST /tss/reshare/start`, reshare id
   `migrate-<ceremony>`, authorised `reshare`). The key and its address do not change. The new
   shares are sealed as KMS envelopes.
3. Each party then destroys every earlier version of its Vault entry (`vault kv destroy`), each of
   which is a clear-text share, and reports how many it destroyed. The refresh is not reported
   complete until this has run. If sealing or destruction fails, the party says so. If sealing
   failed, the key stays blocked.

Both authorisations are bound to the ceremony and reshare id, so the co-signer can sign them offline
with `infrastructure/kind/authorize` (`migrate <ceremony>`, `reshare migrate-<ceremony>`) and hand
over `auth.json`. The tool checks each party's attestation before sending anything.

Then purge every backup of Vault taken before the migration. For a key whose old shares may already
have been copied, migration is the wrong tool. Instead, generate a new key inside the enclaves and
move the funds to it on-chain.

Tested by `TestMigrateALegacyKeyIntoEnclaveSealing`:
1. A key is generated the old way.
2. Three simulated enclaves migrate it, and it is refused for signing until refreshed.
3. Vault ends with one live version per party, and it is an envelope.
4. The key signs, and the signature verifies against the original public key.
5. After a restart the parties restore from the envelopes and sign again.
6. A second migration is refused.

`TestMigrateLegacyNeedsItsOwnAuthorisation` checks that a `restore` authorisation does not authorise a
migration.

## Configuring a party

Baked into the image (build arguments to `infrastructure/nitro/Dockerfile.enclave`):

| Setting | Meaning |
|---|---|
| `ENCLAVE_KMS_REGION`, `ENCLAVE_KMS_KEYS` | Region, and `party=key ARN` for every party |
| `ENCLAVE_PARTY_PCR3` | `party=PCR3 hex` for every party; the enclave reports its own in its start-up report |
| `ENCLAVE_REQUIRE_PARTY_PIN` | `1` (default); `0` only for the spike |
| `ENCLAVE_EXTRA_IMAGE_PCR0` | Additional images peers may run, during a rollout |
| `CEREMONY_AUTHORIZER_PUBKEY` | Required |
| `ENCLAVE_ORCHESTRATOR_CA_FILE` | The orchestrator's CA, copied into the image |

From the host (`HOST_RUNTIME_CONFIG`, a JSON file): `PARTY_ID`, `ENCLAVE_EGRESS`
(`party-2.internal:7000=7102,vault.internal:8200=7200,kms.<region>.amazonaws.com:443=8443`),
`VAULT_ADDR`, `VAULT_TOKEN`, `VAULT_KV_MOUNT`, `VAULT_KEY_SHARE_PATH`, `VAULT_CACERT_PEM` and
`TSS_PREPARAMS_POOL`. The host's own `HOST_EGRESS` maps each vsock port to its destination.

The KMS key policy template is [`infrastructure/nitro/kms-key-policy.json`](../../infrastructure/nitro/kms-key-policy.json).
It lets the party's role use the key only with an attested recipient of the released image and the
party's encryption context. It denies all cryptographic use without attestation to every principal.

## Design decisions and corrections

1. **The peer channel ends inside the enclave.** The original brief had the host doing mTLS to peers,
   which would have let the host read every share during DKG and refresh. The first version of this
   document proposed a custom sealed-envelope protocol relayed by the host. The implementation instead
   ends standard TLS 1.3 inside the enclave and binds it to attestation. The host sees the same thing in
   both designs: ciphertext it cannot open. This design reuses the party's existing HTTP handlers and
   `peer_identity.go`, rather than adding a new protocol.
2. **Ceremony authorisation is inside.** `authorizer.go` already runs in the party process, so it runs
   in the enclave. Its public key is in the image, and enclave mode will not start without it.
3. **One image for every party.** Because the party number comes from the host, all parties share one
   PCR0, and a party can require its peers to match its own measurement. A per-party image would need
   each image to contain the others' hashes. The role pin (PCR3) and the per-party KMS key stop a host
   from claiming another party's number.
4. **Shares at rest are envelope-encrypted.** Vault, and its off-site backup, hold only ciphertext the
   host cannot open.
5. **Isolation is per process, in one cluster, until the separate-accounts rehearsal.** Enclaves build
   on that work; the rehearsal comes first.

## Remaining work

| Item | Size |
|---|---|
| Run `spike.sh` on hardware; commit the evidence | 1 day (you, on AWS) |
| Pin base images by digest; check EIF reproducibility | 1–2 days |
| ~~Migration of existing shares by refresh ceremony into enclave sealing~~ | Done (see above) |
| Image rollout procedure; multi-region key; break-glass recovery with recovery-key holders | 2–3 weeks |
| EC2 and KMS provisioning in Terraform for the three-account layout | 1 week |
| Independent review of this design | external |

## Sources

- tss-lib README, "How to use this securely", and `ecdsa/keygen/messages.go` (`NewKGRound2Message1`), in
  the module cache for `github.com/bnb-chain/tss-lib/v2@v2.0.0`
- Libraries used: [`hf/nsm`](https://github.com/hf/nsm) (attestation, `nitro` builds only),
  [`hf/nitrite`](https://github.com/hf/nitrite) (verification),
  [`edgebitio/nitro-enclaves-sdk-go`](https://pkg.go.dev/github.com/edgebitio/nitro-enclaves-sdk-go)
  (`crypto/cms` only, to open `CiphertextForRecipient`),
  [`mdlayher/vsock`](https://github.com/mdlayher/vsock), AWS SDK for Go v2
- AWS: [KMS condition keys for Nitro Enclaves](https://docs.aws.amazon.com/kms/latest/developerguide/conditions-nitro-enclaves.html);
  [Using cryptographic attestation with KMS](https://docs.aws.amazon.com/enclaves/latest/user/kms.html);
  [How to call KMS APIs for a Nitro enclave](https://docs.aws.amazon.com/kms/latest/developerguide/how-nitro-enclaves.html)
