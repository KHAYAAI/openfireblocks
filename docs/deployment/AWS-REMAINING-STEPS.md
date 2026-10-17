# The remaining AWS work, step by step

These are the seven items that need an AWS account and someone with access to it. Do them in this order:
each one builds on the one before.

| # | Item | Time | Rough monthly cost |
|---|---|---|---|
| 1 | Staging redeploy | ½ day | Existing staging instance |
| 2 | Cosign signing on a real registry | ½ day | Under US$5 (ECR storage) |
| 3 | Real off-site backups | ½ day | Under US$5 (S3) |
| 4 | Disaster-recovery test | 1 day | A second instance for one day |
| 5 | Pentest round 4 | 1–2 days | The pentest tool's own fee |
| 6 | Separate hosts for the signing parties | 1–2 weeks | 3 small instances in 3 accounts |
| 7 | CloudHSM | 1 week | The largest item: billed per HSM-hour, two HSMs for availability. **Check current pricing first** |

Rules that apply throughout:
- **Use one region**, with MFA on every account.
- **Never paste a secret into a chat, a ticket or the repository.**
- **Save each run's output under `docs/evidence/`**, named `YYYY-MM-DD-<item>.md`. That file is what an auditor
  or a bank reads.

---

## 1. Staging redeploy

**Why:** staging still runs code from before the round-3 pentest fixes. It also lacks this week's work:
retention, SPL tokens and user wallets.

1. **Unblock the push first** (GitHub → Settings → Rules → Rulesets → *Neo-rules*). Do one of these:
   - change *Target branches* from "All branches" to "Include default branch"; or
   - add yourself as a bypass actor.

   Then push the branch, and merge it to `claude/openfireblocks-main` through a pull request.
2. **Connect to the instance:** `ssh -i key.pem ubuntu@<ELASTIC_IP>`, then `cd openfireblocks && git pull`.
3. **Rebuild:** `infrastructure/kind/up.sh`. It reapplies all the migrations, including 037–039.
4. **Reapply the settings that `up.sh` resets.** They are all in one command, so none is forgotten:
   ```bash
   helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
     --set apiGateway.trustProxyHops=1 \
     --set mpcSigner.solana.rpcUrl=https://api.devnet.solana.com \
     --set mpcSigner.cosmos.lcdUrl=<your Osmosis testnet LCD> \
     --set mpcSigner.cosmos.chainId=osmo-test-5 --set mpcSigner.cosmos.gasPrice=0.025 \
     --set-json 'apiGateway.evmRpc={"11155111":"<your Sepolia RPC>"}' --wait
   ```
   - Put this command in a file on the instance, for example `~/reapply.sh`, so it is the same every time.
   - Check each key name against `values.yaml` the first time you run it.
5. **Check it:** run `infrastructure/kind/services-smoke-test.sh`, then
   `curl https://<staging-host>/health`.
6. **Prove the new features on public test networks.** Each one is a few minutes once staging is up.
   - **SPL tokens:**
     1. Register devnet USDC (mint `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU`) as chain 501.
     2. Run verification.
     3. Fund a Solana key with devnet SOL and devnet USDC from Circle's faucet.
     4. Send with `"token": "USDC"`.
     5. Record the signature in `docs/evidence/`.
   - **User wallets:**
     1. `POST /wallets` for a test reference.
     2. Wait until its keys are `active`.
     3. Fund it, then send through `POST /wallets/:id/transfers`.
     4. Freeze it and confirm that the next transfer is refused.

## 2. Cosign signing on a real registry

**Why:** images are signed with cosign keyless signing in `deploy.yaml`, but this has never run against a
real registry. SOC 2 CC6.8 is marked "partial" until it has.

1. **Create one private ECR repository per service:**
   ```bash
   for s in $(scripts/build-images.sh --list); do
     aws ecr create-repository --repository-name "$s" \
       --image-scanning-configuration scanOnPush=true --image-tag-mutability IMMUTABLE
   done
   ```
   `IMMUTABLE` means a tag can't be overwritten later.
2. **Let GitHub Actions in without a stored key.**
   - In IAM, add an identity provider:
     - type: OpenID Connect
     - provider URL: `https://token.actions.githubusercontent.com`
     - audience: `sts.amazonaws.com`
   - Create the role `ofb-github-build`. Trust only this repository's default branch:
     `repo:KHAYAAI/openfireblocks:ref:refs/heads/claude/openfireblocks-main`.
   - Give the role ECR push and pull on these repositories only.
3. **Add these in GitHub → Settings → Secrets and variables → Actions:**
   - secret `AWS_ROLE_ARN_BUILD` = the role's ARN;
   - secret `ECR_REGISTRY` = `<account>.dkr.ecr.<region>.amazonaws.com`;
   - variable `AWS_REGION`.
   - The workflow now logs in with that role. It previously had no AWS credentials, and it pointed at ECR
     Public; both are fixed in `deploy.yaml`.
4. **Run it once:** Actions → *Deploy* → *Run workflow*.
5. **Verify it from your laptop:**
   ```bash
   REPO=KHAYAAI/openfireblocks scripts/verify-image.sh \
     <account>.dkr.ecr.<region>.amazonaws.com/api-gateway@sha256:<digest>
   ```
   It should print "signed by this repository's deploy workflow" and a package count from the SBOM. Save
   the output as evidence.

## 3. Real off-site backups

**Why:** risk R-10. Backups are encrypted before they leave, but this has only been run against a local
stand-in for S3.

1. **Create a bucket in a different account or region from staging**, for example `ofb-backups-<random>`:
   - turn on versioning;
   - turn on **Object Lock in compliance mode**, with a retention period (for example 35 days);
   - block all public access.
2. **Create an IAM user (or role) limited to this bucket**, allowed `s3:PutObject` and `s3:GetObject`
   only, with no delete. Staging runs kind on one EC2 instance, so pods can't use a service-account role.
   Use an access key stored in a Kubernetes Secret:
   ```bash
   kubectl -n openfireblocks create secret generic backup-offsite-creds \
     --from-literal=aws-access-key-id=... --from-literal=aws-secret-access-key=...
   ```
3. **Create the encryption key:**
   ```bash
   openssl rand -hex 32 > offsite.key
   kubectl -n openfireblocks create secret generic backup-offsite \
     --from-file=backup-offsite-key=offsite.key
   ```
   **Store `offsite.key` in your password manager, then delete the file.** Without it, the backups can't
   be read.
4. **Turn it on:**
   ```bash
   helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
     --set backup.enabled=true --set backup.offsite.enabled=true \
     --set backup.offsite.bucket=ofb-backups-<random> --set backup.offsite.region=<region> \
     --set backup.offsite.keySecret=backup-offsite \
     --set backup.offsite.credentialsSecret=backup-offsite-creds --wait
   ```
5. **Run one now instead of waiting for 02:17:**
   `kubectl -n openfireblocks create job --from=cronjob/<backup-cronjob> backup-now`.
6. **Check:**
   - `aws s3 ls s3://ofb-backups-<random>/openfireblocks/ --recursive` shows `postgres.enc`, `vault.enc` and
     `manifest.enc`.
   - Save the run's output; it holds the SHA-256 tags you need for a restore.
7. **Prove that a delete is refused:** `aws s3api delete-object` on one object version should fail
   because of Object Lock.

## 4. Disaster-recovery test

**Why:** ISO A.5.30 and SOC 2 A1.3. The recovery objectives are only real once they have been measured.
Follow `docs/compliance/isms/exercises/DR-TEST.md`. In short:

1. **Write down the starting point:** the newest backup time, plus a marker transfer and approval on
   testnet.
2. **Launch a second, empty instance** using the staging guide, parts B and D, and run `up.sh` on it.
3. **Restore from the off-site copy, not the local one.**
   - Call `POST /offsite/fetch` with the backup id and SHA-256 tags from step 3.
   - Then restore Postgres and Vault from the fetched files.
4. **Check the restore:**
   - the marker approval is there;
   - row counts match;
   - the immutability triggers exist (`\dS+ travel_rule_records` shows the guard).
5. **Run the drills against the new instance:**
   - `node-failure-drill.sh`: a transfer still signs with only 2 of 3 parties;
   - `recovery-drill.sh`: the recovered key signs, and its address is unchanged;
   - `postgres-failover-drill.sh`.
6. **Freeze and unfreeze a test organisation and a test user wallet.** Signing must be refused while
   frozen.
7. **Fill in the record table in `DR-TEST.md`**, including the RPO and RTO you achieved.
8. **Clean up:** terminate the second instance and rotate any secrets used during the test.

## 5. Pentest round 4

**Why:**
- Round 3's two low findings are fixed in code but not yet redeployed.
- The MCP server, agent routes, retention, SPL tokens and user wallets have never been tested by an
  attacker.

1. **Do step 1 first,** so staging runs the current commit. Record the commit SHA.
2. **Create a dedicated test organisation with a fresh API key.** Use a real (testnet) setup with an AI
   agent key, a user wallet and a registered SPL token, so the new routes have something to attack.
3. **Run Shannon** (Keygraph) as in rounds 1–3:
   - authenticated as that tenant;
   - in exploit mode;
   - against `https://<staging-host>`.
   - Scope it to the API **plus** these routes:
     - `/wallets/*`
     - `/organisations/:id/wallets/*`
     - `/keys/:id/solana-transactions` with `token`
     - `/admin/retention/*`
     - the MCP server's endpoint
4. **Triage:**
   - Write every finding into `docs/security/PENTEST-ROUND-4-<date>.md`, using the same table format as
     round 2.
   - Fix high and medium findings before any pilot.
   - Lows can be scheduled.
5. **Redeploy the fixes, then re-run Shannon on the affected routes only.** Record that the findings are
   closed.
6. **Note:** this is still an AI-driven test. A bank will also want the independent human pentest,
   budgeted separately.

## 6. Separate hosts for the signing parties

**Why:** today all three parties run on one machine, so one administrator could reach all of them. This
is the step between bounded pilots and real money.

The full procedure, with every command, is in [SEPARATE-HOSTS.md](SEPARATE-HOSTS.md). The outline:

1. **Name three operators**, one per party. None of them holds another's credentials or recovery keys.
2. **Create three AWS accounts**, ideally in an AWS Organization with SCPs that deny leaving the region and
   disabling CloudTrail. Each operator:
   - creates their own Terraform state bucket and lock table;
   - pushes the `mpc-party` image to their own ECR, **pinned by digest**. Verify it first with
     `verify-image.sh` from step 2.
3. **Each operator applies `infrastructure/terraform/party`** with a placeholder peer address, then sends
   their `public_ip` to the others.
4. **Each operator re-applies** with the real peer addresses.
5. **Set up DNS and mTLS.**
   - Create `party-1/2/3.<domain>`.
   - Issue each party a certificate from one CA.
   - Store each certificate in that party's own Secrets Manager entry **with the AWS CLI, not Terraform**,
     so it never lands in Terraform state.
6. **Each operator runs `party-bootstrap.sh`** over SSM. The Vault recovery keys are PGP-encrypted to named
   holders, and the root token is revoked.
7. **Point the platform at the external parties** (`mpcParty.external` in the chart).
8. **Check the isolation:** run `infrastructure/kind/party-isolation-check.sh` with `OFB_PARTY_HOSTS` set.
9. **Generate a brand-new key on the separated parties, and sign with it.**
   - Old keys stay where they were made. Move funds to new keys, then have each party retire its old share
     (`POST /tss/shares/retire`).
10. **Record:** which operators, which accounts, the isolation check output, and the first signature.

## 7. CloudHSM

**What it covers:**
- **Covered: the `mpc-signer` single-key signing path.** Its private key is generated inside the HSM
  and never leaves it.
- **Not covered: the threshold key shares.** The parties keep those in their own Vaults.
- Read `docs/engineering/PKCS11-HSM-SIGNING.md` before buying.

**Cost:** CloudHSM is billed per HSM per hour, and the availability setup needs two HSMs. Check the current
price for your region on the AWS pricing page before creating anything, and delete the cluster when you're
not using it.

1. **Network:** the HSM cluster needs a VPC with subnets in two availability zones. The machine running
   `mpc-signer` must be in that VPC and allowed to reach the HSMs on TCP 2223–2225.
2. **Create the cluster:** CloudHSM → Create cluster → choose the two subnets. Then create **one** HSM
   first.
3. **Initialise it. This is the step that makes it yours:**
   1. Download the cluster's certificate signing request.
   2. Sign it with your own CA, using OpenSSL on an offline machine.
   3. Upload the signed certificate and the CA certificate.
   4. Keep the CA key offline.
4. **Activate it:**
   - Install the CloudHSM CLI (Client SDK 5) on the `mpc-signer` host.
   - Run `cluster activate` and set the admin password.
   - Create a **crypto user** for the signer. Its password becomes the PKCS#11 PIN.
5. **Add the second HSM** in the other availability zone.
6. **Install the PKCS#11 library** on the host or into the image. It goes to
   `/opt/cloudhsm/lib/libcloudhsm_pkcs11.so`. Configure it with the cluster's address and CA certificate.
7. **Build the HSM variant of the signer:** `docker build --target pkcs11 services/mpc-signer`. Push and sign
   it as in step 2.
8. **Store the PIN in a Secret**, under the key `hsm-pkcs11-pin` in `openfireblocks-secrets`. Then set
   `mpcSigner.hsm.*` in the chart:
   - `library`
   - `tokenLabel`
   - `keyLabel`
   - `pinSecret`

   The image tag becomes `<tag>-pkcs11`.
9. **Create the key once, inside the HSM:** `kubectl exec ... -- hsm-key generate`. The service itself never
   creates keys, so a mistyped label fails instead of silently making a new key.
10. **Prove it:**
    - Sign a Sepolia transaction through `POST /sign`.
    - Then show that the key can't be exported. CloudHSM refuses to export a non-extractable key.
    - Record both.

---

## When all seven are done

Update `LAUNCH-READINESS.md`, `docs/assurance/SOC2-CONTROL-MATRIX.md` (CC6.8, A1.2, A1.3) and risk
register items R-10 and R-11. Link each change to its evidence file.
