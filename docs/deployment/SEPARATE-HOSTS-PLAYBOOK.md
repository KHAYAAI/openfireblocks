# Separate hosts: the playbook

How to move the three signing parties onto three separate hosts in three separate AWS accounts, in order, with a
checkpoint after every phase. It is the practical companion to [SEPARATE-HOSTS.md](SEPARATE-HOSTS.md) (the design and
what was tested) and [PARTY-ISOLATION.md](PARTY-ISOLATION.md) (why it matters).

**Status, 2026-10-18: written, not yet executed.** The Terraform, the chart's external-party mode and the isolation check
exist and are tested as far as a machine with no AWS access allows (`terraform fmt`; CI validates both roots). **Nothing
has ever been applied to a real account.** Expect the first run to find faults, as the first disaster-recovery rebuild
found four. Budget for that, and record what breaks.

---

## 0. Pick a track

| | **A. Rehearsal** | **B. Real separation** |
|---|---|---|
| Who | You, alone, with three AWS accounts you create | Three different people or organisations, one account each |
| What it proves | The Terraform applies, the parties reach each other across accounts, a key generates and signs, the isolation check passes, recovery works | All of A, **plus** that no single person can reach more than one share |
| What it cannot claim | Separation of duties: you administer all three accounts, so you are the single administrator the design removes | Nothing further is blocked on this item |
| Time | 1 to 2 days | 1 to 2 weeks (mostly waiting for people) |
| Cost | A few dollars for a day of testing; destroy everything afterwards | About $70 a month per party on demand, plus each operator's time |
| Record it as | "multi-account, single operator" | "multi-account, three operators" |

**Recommendation: do A first, then B.** A finds the faults while nobody else's time is being spent. B then goes quickly
because the commands are proven. Never describe A as separation to a customer, a bank or an auditor.

The two tracks use the same steps. Where they differ, a **Track A:** or **Track B:** note says how.

---

## 1. Who does what (Track B)

| Role | Holds | Must not hold |
|---|---|---|
| **Operator 1, 2, 3** (one per party) | Their own AWS account; their own state bucket; the root/admin credentials of that account; their party's TLS private key; the PGP **private** keys of their Vault recovery holders | Anything belonging to another operator |
| **Platform operator** (you) | The platform cluster; the ceremony **co-signer** (authorising) key's custody arrangement; the CA for the ceremony network (or delegates it, see 5) | Any party's account, TLS private key or Vault recovery key |
| **Recovery key holders** (at least 3 per party, ideally different people each) | One PGP-encrypted Vault recovery key share each | A holder in one party should not be a holder in another |

Write the names down. An auditor will ask, and the control is only as good as the list is true.

Agree these before anything is applied: the **domain** the parties will answer on (e.g. `custody.yourdomain.com`), the
**regions**, and who is on call for each party.

**Track A:** you play all roles. Use three different browsers or profiles and three CLI profiles (`ofb-party-1`, `-2`,
`-3`) so you cannot accidentally apply party 2 into party 1's account. The Terraform refuses that anyway
(`expected_account_id`), which is the point of using it.

---

## 2. What you need before starting

On each operator's machine: `terraform` 1.5 or newer, the `aws` CLI v2, `docker`, `cosign`, `jq`, `openssl`, `gpg`.

| Item | Why | Track A shortcut |
|---|---|---|
| A **domain** you control, with DNS you can edit | Each party must answer on `party-N.<domain>` with a certificate for that name; the parties also resolve each other by name from their hosts | Register a cheap domain in Route 53 (a few dollars a year), or use a subdomain of one you own |
| A **platform** to drive the ceremonies | The temporal worker and gateway must reach the parties | Build a throwaway platform with the DR runbook's Phases 2 to 4 (`DR-REBUILD-RUNBOOK.md`); do **not** point the existing staging at external parties, because turning its in-cluster parties off hides every key they hold |
| The platform's **egress IPs** | Parties only accept ceremony traffic from named addresses | The throwaway instance's Elastic IP (allocate one and attach it) |
| The `mpc-party` **image**, signed | The parties run it | `c585a2e` or later (contains the signing-driver fix), 14 of 14 verified |
| 3 **PGP public keys** per party | The Vault recovery keys are encrypted to them | Make three throwaway keys per party with `gpg --quick-generate-key` |

**Do not use an image older than `c585a2e`.** Earlier images can hang a signing ceremony intermittently.

---

## 3. Phase 1: accounts, state and images (each operator, about 1 hour)

Each operator, in their **own** account, signed in with MFA on the root user and a named admin identity:

1. Create the Terraform state bucket and lock table, in that account only:
   ```bash
   export AWS_PROFILE=ofb-party-N; R=af-south-1   # their region
   ACCT=$(aws sts get-caller-identity --query Account --output text)
   aws s3api create-bucket --bucket ofb-party-N-tfstate-$ACCT --region $R \
     --create-bucket-configuration LocationConstraint=$R
   aws s3api put-bucket-versioning --bucket ofb-party-N-tfstate-$ACCT --versioning-configuration Status=Enabled
   aws s3api put-public-access-block --bucket ofb-party-N-tfstate-$ACCT \
     --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
   aws dynamodb create-table --table-name ofb-party-N-tflock --attribute-definitions AttributeName=LockID,AttributeType=S \
     --key-schema AttributeName=LockID,KeyType=HASH --billing-mode PAY_PER_REQUEST --region $R
   ```
   (For `us-east-1` omit `--create-bucket-configuration`.) Never share a state bucket between parties: the state names
   every resource, so whoever administers the bucket could then reach all of them.
2. Create `backend-party-N.hcl` from `infrastructure/terraform/party/backend.hcl.example` with those names.
3. Create an ECR repository `mpc-party` in the same account.
4. **Pull, verify, push the image.** The platform operator grants each operator's account read access to the platform's
   `mpc-party` repository (a repository policy with `ecr:BatchGetImage`, `ecr:GetDownloadUrlForLayer`,
   `ecr:BatchCheckLayerAvailability`). Each operator then:
   ```bash
   cosign verify <platform-registry>/mpc-party@sha256:<digest> \
     --certificate-identity-regexp '^https://github.com/KHAYAAI/openfireblocks/\.github/workflows/deploy\.yaml@' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   docker pull <platform-registry>/mpc-party@sha256:<digest>
   docker tag  <platform-registry>/mpc-party@sha256:<digest> $ACCT.dkr.ecr.$R.amazonaws.com/mpc-party:v1
   docker push $ACCT.dkr.ecr.$R.amazonaws.com/mpc-party:v1
   aws ecr describe-images --repository-name mpc-party --image-ids imageTag=v1 --query 'imageDetails[0].imageDigest'
   ```
   Use that digest, `...mpc-party@sha256:<digest>`, as `party_image`. A digest, never a tag: nobody can change what
   runs without changing the file.

**Checkpoint 1:** each operator can show `cosign verify` passing, an image digest in their own ECR, and a state bucket
in their own account. Nothing else exists yet.

---

## 4. Phase 2: the ceremony co-signer key (platform operator, 20 minutes)

A party on the public internet should not sign for the orchestrator alone. The platform operator holds a second key that
each party checks (`ceremony_authorizer_pubkey`). Generate it and decide where it lives:

```bash
openssl genpkey -algorithm ed25519 -out cosigner.key        # PRIVATE: see below
openssl pkey -in cosigner.key -pubout -outform DER | tail -c 32 | xxd -p -c 64   # the public key, 64 hex characters
```

- The 64-character output is the `ceremony_authorizer_pubkey` for every party, with algorithm `ed25519` (the default).
  **Check before relying on it:** `terraform.tfvars.example` shows an uncompressed secp256k1 key, but the party refuses
  secp256k1 for the authorising key by design (`values.yaml`, `ceremonyAuthorizer.algorithm`). Use ed25519. The example
  file is wrong and should be fixed. Run the isolation check and a ceremony (Phase 7) before trusting any of this.
- Where the private key lives is the control. If it sits in the platform cluster, compromising the cluster yields
  ceremonies, which defeats the point. Better: a separate signer service or KMS-backed key
  (`ceremonyAuthorizer.signerUrl`), held by someone who is not the cluster administrator. **Track A:** keep the file on
  your laptop and sign by hand with `infrastructure/kind/authorize`; record that this is a rehearsal shortcut.

**Checkpoint 2:** the public key is written down; the private key's custodian is named.

---

## 5. Phase 3: the ceremony CA and certificates (about 1 hour)

All parties and the platform's worker must trust one certificate authority, and every party needs a certificate with the
common name `party-N.internal` (the party takes its identity from that name) and a DNS name `party-N.<domain>` (what
peers dial).

**Principle: each operator generates their own private key and sends the CA only a certificate signing request.** A
party's TLS private key must never leave its operator's hands.

1. **Platform operator creates the CA** (once). Keep `ca.key` offline and tightly held:
   ```bash
   openssl ecparam -name prime256v1 -genkey -noout -out ca.key
   openssl req -x509 -new -key ca.key -sha256 -days 825 -subj "/CN=OpenFireblocks ceremony CA" -out ca.crt
   ```
   Distribute **only `ca.crt`**.
2. **Each operator creates a key and a request** for their party:
   ```bash
   N=2; D=custody.yourdomain.com
   openssl ecparam -name prime256v1 -genkey -noout -out party-$N.key
   openssl req -new -key party-$N.key -subj "/CN=party-$N.internal" \
     -addext "subjectAltName=DNS:party-$N.$D,DNS:party-$N.internal" -out party-$N.csr
   ```
   They send `party-$N.csr` to the platform operator.
3. **Platform operator signs it** with server and client usage, and returns `party-$N.crt`:
   ```bash
   printf 'subjectAltName=DNS:party-%s.%s,DNS:party-%s.internal\nextendedKeyUsage=serverAuth,clientAuth\n' $N $D $N > ext.cnf
   openssl x509 -req -in party-$N.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 -sha256 -extfile ext.cnf -out party-$N.crt
   ```
4. **The platform's worker also needs a client certificate** signed by the same CA (`CN=temporal-worker.internal`, usage
   `clientAuth`). Do the same for it, and later put it in the secret the chart reads.
5. Each operator keeps `party-N.key` and `party-N.crt` and `ca.crt`; they are loaded into that account's Secrets
   Manager in Phase 5, **with the AWS CLI, never through Terraform** (Terraform would write them into its state).

**Checkpoint 3:** `openssl verify -CAfile ca.crt party-N.crt` says OK for all three, and no private key has left its owner.
Calendar a renewal for 30 days before the certificates expire (365 days above).

**Not yet proven:** that the parties and the worker accept exactly these certificates. The in-cluster path issues them
from Vault; this external path is untested. Phase 7 is where it is proven.

---

## 6. Phase 4: apply each party (each operator, about 30 minutes each)

The first apply cannot know the other parties' addresses, so it is done twice (as in SEPARATE-HOSTS.md).

1. **First apply** with a placeholder peer. In `infrastructure/terraform/party`:
   ```bash
   cp terraform.tfvars.example party-N.tfvars        # edit: region, expected_account_id, party_id, party_image (digest),
                                                     # platform_cidrs (the platform's EIP /32), ceremony_authorizer_pubkey
   terraform init -backend-config=backend-party-N.hcl
   terraform plan  -var-file=party-N.tfvars -var='peer_party_cidrs=["192.0.2.1/32"]'
   terraform apply -var-file=party-N.tfvars -var='peer_party_cidrs=["192.0.2.1/32"]'
   terraform output public_ip
   ```
   **Read the plan before applying.** Check it creates one VPC, one security group with no SSH rule, one encrypted
   volume, two KMS keys, an Elastic IP and an instance, and nothing outside this account. The apply refuses any account
   other than `expected_account_id`.
2. **Exchange the addresses.** Each operator sends only their `public_ip` to the other two and to the platform operator.
3. **Second apply** with the real peers in `peer_party_cidrs` (the two *other* addresses, as `/32`s):
   ```bash
   terraform apply -var-file=party-N.tfvars        # with peer_party_cidrs set to the other two
   ```
4. **DNS.** Create `party-1.<domain>`, `party-2.<domain>`, `party-3.<domain>` as A records pointing at the three
   `public_ip` values.

**Checkpoint 4:** three instances exist, each in its own account; `dig +short party-N.<domain>` returns each
address; the security group of each allows the ceremony port only from the two peers and the platform.

---

## 7. Phase 5: each party's Vault and secrets (each operator, about 45 minutes)

Each operator, on **their own** host, through SSM (there is no SSH):

1. Put the party's certificate in Secrets Manager (the value is JSON with `cert`, `key`, `ca`):
   ```bash
   aws secretsmanager put-secret-value --secret-id "$(terraform output -raw tls_secret_arn)" \
     --secret-string "$(jq -n --rawfile c party-$N.crt --rawfile k party-$N.key --rawfile a ca.crt '{cert:$c,key:$k,ca:$a}')"
   ```
2. Open a session and initialise the party's own Vault:
   ```bash
   aws ssm start-session --target "$(terraform output -raw instance_id)"
   sudo bash party-bootstrap.sh <vault_token_secret_arn> alice.asc,bob.asc,carol.asc
   ```
   The Vault auto-unseals with this account's KMS key, so there are no unseal keys to hand out. `vault operator init`
   still makes **recovery keys**; they are encrypted to the PGP keys given, written to
   `/root/vault-recovery-keys.pgp.json`. **Copy that file off the host, give each holder only their own share, then delete
   it from the host.** Holders keep their share offline and do not hold another party's.
3. Start the party now that its certificate and Vault token exist (the unit is `ofb-party.service`; it refuses to start
   until the token is stored), then check its health from outside:
   ```bash
   sudo systemctl restart ofb-party.service
   ```
   ```bash
   curl -s http://party-$N.<domain>:7001/health
   ```

**Checkpoint 5:** each party answers `/health`; its recovery shares are with their holders; the root token is revoked
(the script does this); the PGP-encrypted file is no longer on the host.

---

## 8. Phase 6: point the platform at the parties (platform operator, 30 minutes)

On the (throwaway) platform cluster, in `values`:

```yaml
mpcParty:
  enabled: false
  external:
    enabled: true
    endpointTemplate: https://party-{id}.custody.yourdomain.com:7000
    healthTemplate: http://party-{id}.custody.yourdomain.com:7001/health
temporalWorker:
  mtls:
    enabled: true
    autoIssue:
      enabled: false              # the certificate comes from the ceremony CA, not the cluster's Vault
    certSecret: openfireblocks-mtls   # keys tls.crt, tls.key, ca.crt: the worker's certificate from step 5.4
ceremonyAuthorizer:
  enabled: true
  publicKey: <the 64 hex characters from Phase 2>
external:
  partyIsolationLevel: multi-account
```

The chart refuses plaintext, a template without `{id}`, and running without worker mTLS. After `helm upgrade`, restart the
worker and gateway.

**Checkpoint 6:** the worker and gateway are Running; the gateway's readiness is healthy; no in-cluster party pods exist.

---

## 9. Phase 7: verify, then prove it with a key (about 2 hours)

1. **Isolation check** from the platform:
   ```bash
   OFB_PARTY_HOSTS=party-1.custody.yourdomain.com,party-2.custody.yourdomain.com,party-3.custody.yourdomain.com \
   OFB_PARTY_ACCOUNTS=<acct-1>,<acct-2>,<acct-3> \
   REQUIRE=multi-account ./infrastructure/kind/party-isolation-check.sh
   ```
   It must pass at `multi-account`. If the parties are in the same account it fails: that is it working.
2. **Generate a brand-new key** on the separated parties through the API and **sign with it** (`smoke-test.sh` does
   both). Old keys stay where they were made. Time the ceremony: cross-region latency is real, and nobody should be
   promised a signing time before it is measured.
3. **Recovery drill** against these hosts (`KEY-RECOVERY.md`): stop one party's host, confirm 2 of 3 still signs; restore
   it from its Vault; then `restore-all-keys.sh`. Record the times.
4. **Negative checks** (these are the point): from the platform operator's side confirm they cannot read another party's
   Secrets Manager, Vault or disk; confirm each operator cannot sign alone (one party refuses, two are needed).
5. **Retire** any old in-cluster shares by moving funds to new keys and having each party retire its old share
   (`POST /tss/shares/retire`), per SEPARATE-HOSTS.md.

**Checkpoint 7 and the evidence to keep** (`docs/evidence/YYYY-MM-DD-separate-hosts.md`): which operators and accounts
(account ids are fine; no keys or tokens), the `terraform plan` summaries, the isolation check output, the first
signature and its address, the measured ceremony and recovery times, and every fault found with its fix.

---

## 10. What goes wrong (probable first-run faults)

| Symptom | Likely cause | First thing to check |
|---|---|---|
| `terraform apply` refuses | Wrong profile: `expected_account_id` does not match | `aws sts get-caller-identity` |
| A party never becomes healthy | Image pull failed, or the TLS secret is empty or malformed | Instance system log through SSM; the secret's JSON keys are `cert`, `key`, `ca` |
| Ceremony starts but never completes | Security group missing a peer, or DNS for a peer wrong | The peer `/32`s are the other parties' current Elastic IPs; `dig` from each host |
| `x509: certificate is valid for …, not …` | The certificate lacks the DNS name the peer dialled | The SAN list in Phase 3, step 2; the name in `endpointTemplate` |
| HTTP 403 on every ceremony call | The co-signer public key or algorithm does not match the signing key | Phase 2 note on ed25519 and the 64-character form |
| Keys generate but signing hangs | An image older than `c585a2e` | `kubectl`/instance image digest against the verified one |
| Cannot reach the party's Vault after a reboot | KMS auto-unseal permission | The instance role and the KMS key policy in that account |

---

## 11. Teardown (Track A) and ongoing care (Track B)

**Track A, when finished:** `terraform destroy` in each account (the share volume has `prevent_destroy`; remove that line
first on purpose and only on rehearsal accounts), delete the ECR images and state buckets, release the Elastic IPs,
terminate the throwaway platform, close the accounts you no longer need, and **delete the CA and co-signer keys**: they
were generated for a rehearsal.

**Track B, standing duties** (add to the go-live runbook and the ISMS):
- Rotate party certificates before expiry; calendar it.
- Each operator patches their own host and keeps their account's CloudTrail on.
- Re-run the isolation check and a recovery drill at least yearly, and after any change of operator.
- Record a change of operator or recovery-key holder; re-key if one leaves.
- The co-signer key's custody is reviewed as often as the parties' are.

---

## 12. Decisions

**Decided 2026-10-19:** Track A (the rehearsal, one operator) first, then Track B (three operators). Regions as
suggested: party 1 `eu-west-1`, party 2 `eu-central-1`, party 3 `eu-west-2`. Co-signer key on the operator's laptop for
the rehearsal. Still to settle: the domain, the three account ids, and (Track B only) operators 2 and 3.

The questions, as they were asked:

1. **Track A first, then B?** (recommended) or go straight to B.
2. **Domain:** which domain will the parties answer on?
3. **Regions:** one per party is better. Suggested: `eu-west-1` (staging is here), `eu-central-1` (the backup bucket is
   here), and a third.
4. **Track B only:** who are operators 2 and 3, and who holds the recovery shares?
5. **Co-signer custody:** where does the private key live?
