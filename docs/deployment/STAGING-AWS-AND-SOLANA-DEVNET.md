# Staging on AWS, then the Solana devnet run

A single EC2 instance running a kind cluster (the full chart), a public HTTPS URL for the
gateway, and then a real Solana devnet transfer.

**What this is, and is not.** A staging environment for demos, integration testing and a later
penetration test. It is one machine: it proves the software runs at full size and is
reachable, not that it is highly available or has isolated signing hosts. It uses development
credentials for the in-cluster Postgres, Vault and Temporal. **No real funds, no real personal
data.** Prices are approximate; check the AWS pricing page for your region.

Not yet run by us: these steps were written from the repo's scripts and have not been
executed on AWS. Expect to fix small things, and send the output when you do.

## Part A: AWS account and safety (30 minutes)

1. Create or sign in to an AWS account. Turn on MFA for the root user.
2. Do daily work as an IAM or Identity Center user, not root.
3. **Set a budget alert:** Billing -> Budgets -> create a monthly cost budget (for example
   US$100) with email alerts at 50% and 80%.
4. Pick a region and use it for everything. Closer to you means lower latency (for example
   `af-south-1` Cape Town, which must be enabled first and is dearer, or `eu-west-1`
   Ireland).

## Part B: Network and instance (30 minutes)

1. **EC2 -> Security Groups -> Create.** Name `ofb-staging`, in the default VPC.
   - Inbound: SSH (22) from **My IP** only; HTTPS (443) from **My IP** for now (open it to
     partners later); HTTP (80) from anywhere (needed for certificate issuance).
   - Nothing else. Do **not** open 3000 or any Kubernetes port.
2. **EC2 -> Key pairs -> Create** (RSA, `.pem`). Download it and `chmod 400` it.
3. **EC2 -> Launch instance.**
   - AMI: Ubuntu Server 24.04 LTS, x86_64.
   - Type: **`m6i.2xlarge`** (8 vCPU, 32 GiB). Roughly US$0.40-0.55 an hour depending on
     region.
   - Storage: **100 GiB gp3**.
   - Security group: `ofb-staging`. Key pair: the one you made.
4. **Elastic IPs -> Allocate -> Associate** with the instance, so the address survives
   stop/start. (A small hourly charge applies for the public IPv4 address.)

## Part C: Domain and DNS (15 minutes plus propagation)

1. Use a domain you own, or register one (Route 53 or elsewhere; about US$13+ a year for a
   `.com`).
2. Create an **A record**, for example `staging.yourdomain.com`, pointing to the Elastic IP.
   In Route 53: Hosted zones -> your domain -> Create record.
3. Check from your laptop: `dig +short staging.yourdomain.com` returns the Elastic IP.

(No domain? Cloudflare Tunnel gives a public URL for free, but needs a Cloudflare account and
a different proxy setup. Ask and I will write it.)

## Part D: Prepare the machine (20 minutes)

```
ssh -i ~/path/key.pem ubuntu@<ELASTIC_IP>
sudo apt update && sudo apt install -y docker.io git curl jq
sudo usermod -aG docker $USER
exit
```
SSH in again so the Docker group applies, then:
```
curl -Lo kind https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-amd64 && sudo install kind /usr/local/bin/kind
curl -LO "https://dl.k8s.io/release/v1.29.14/bin/linux/amd64/kubectl" && sudo install kubectl /usr/local/bin/kubectl
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
sudo snap install go --classic          # the smoke test's last step needs Go
docker info | head -3 && kind version && kubectl version --client && helm version --short
```

## Part E: Install the full platform with real secrets (30 minutes)

The default admin key and JWT secret in `up.sh` are published development values. **Set your
own before the gateway is reachable from any network.**

```
git clone https://github.com/KHAYAAI/openfireblocks && cd openfireblocks
git checkout claude/platform-explanation-h0st5y
umask 077
export ADMIN_API_KEY=$(openssl rand -hex 24)
export JWT_SECRET=$(openssl rand -hex 32)
printf 'ADMIN_API_KEY=%s\nJWT_SECRET=%s\n' "$ADMIN_API_KEY" "$JWT_SECRET" > ~/ofb-secrets.txt
infrastructure/kind/up.sh          # full profile; first run builds everything (15-30 min)
```
Keep `~/ofb-secrets.txt` private. Do not paste it anywhere, including this project's chat.

Wait 5-10 minutes after it finishes, then:
```
kubectl -n openfireblocks get pods         # all Running or Completed, RESTARTS 0
```

## Part F: Keep the gateway reachable (10 minutes)

Create `/etc/systemd/system/ofb-forward.service`:
```
[Unit]
Description=Port-forward the OpenFireblocks gateway to localhost
After=docker.service

[Service]
User=ubuntu
ExecStart=/usr/local/bin/kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000 --address 127.0.0.1
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```
Then `sudo systemctl daemon-reload && sudo systemctl enable --now ofb-forward`.

## Part G: HTTPS with Caddy (10 minutes)

```
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy
```
Copy `infrastructure/caddy/Caddyfile` from the repo to `/etc/caddy/Caddyfile` and replace
`staging.example.com` with the real hostname, then `sudo systemctl reload caddy`. Caddy
obtains the certificate automatically.

The admin and metrics routes are **not** exposed publicly. Use them only on the machine
itself, through `http://127.0.0.1:3000`.

**Known gap: no WAF at this edge.** This Caddy config is a reverse proxy, not a WAF --
it filters nothing except the admin/metrics paths above. `infrastructure/terraform/waf.tf`
defines real AWS WAF rules (rate limiting, managed SQLi/known-bad-input rule sets, IP
reputation), but they only attach to an AWS ALB, and this single-VM deployment has none.
A pentest confirmed SQLi payloads, known-bad-input probes and a request flood all reach
the origin unfiltered here. See the comment at the top of `infrastructure/caddy/Caddyfile`
for the real options (a proxying CDN in front, a Caddy build with the rate-limit plugin,
or moving behind a real ALB) -- none are set up by default, and this stays a known,
accepted gap for a single-VM staging box until one is.

### Tell the gateway about Caddy (client IP for rate limits and login tracking)

Behind Caddy every request reaches the gateway from the proxy, so per-client rate limits and
per-address login tracking would otherwise treat all clients as one. After the install:
```
helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
  --set apiGateway.trustProxyHops=1 --wait
```
`up.sh` resets chart values, so re-run this after any `up.sh`.

## Part H: Verify (10 minutes)

From your laptop (your IP is allowed):
```
curl -s https://staging.yourdomain.com/health/ready      # {"status":"ready",...}
curl -s -o /dev/null -w '%{http_code}\n' https://staging.yourdomain.com/admin/customers   # 404
```
On the VM:
```
cd ~/openfireblocks
source ~/ofb-secrets.txt
ADMIN_KEY="$ADMIN_API_KEY" infrastructure/kind/smoke-test.sh
```
It should end with `PASS`. Save the output (redact secrets) as evidence for the full profile.

If something fails, run `infrastructure/kind/diagnose.sh` and send the file.

## Part I: Stop it when idle

- **Stop** the instance in the EC2 console when you are not using it. You then pay only for
  the disk (about US$8 a month for 100 GiB) and the Elastic IP.
- After starting it again, wait a few minutes and check `kubectl -n openfireblocks get pods`.
  If the cluster did not come back cleanly, run `infrastructure/kind/up.sh` again (it detects
  the existing cluster).
- **Terminate** the instance and release the Elastic IP when you no longer need staging.

---

# Solana devnet run

Needs Part E to be done. Run on the VM.

## 1. Choose an RPC endpoint
- Free public: `https://api.devnet.solana.com` (rate-limited, fine for one test), or
- A free-tier devnet endpoint from a provider (Helius, QuickNode, Alchemy). If the URL
  contains an API key, treat it as a secret: the chart stores it as a plain value, so do not
  commit it.

## 2. Point the signer at it
```
cd ~/openfireblocks
helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks \
  --reuse-values --set mpcSigner.solana.rpcUrl=https://api.devnet.solana.com --wait
```
Check the signer restarted: `kubectl -n openfireblocks get pods | grep mpc-signer`.

## 3. Create a customer and a Solana key
```
source ~/ofb-secrets.txt
API=http://127.0.0.1:3000
customer=$(curl -s -X POST $API/admin/customers -H 'Content-Type: application/json' \
  -H "x-admin-key: $ADMIN_API_KEY" \
  -d '{"email":"devnet@example.com","name":"devnet-test","tier":"enterprise"}')
API_KEY=$(echo "$customer" | jq -r .api_key)
key=$(curl -s -X POST $API/keys -H 'Content-Type: application/json' -H "x-api-key: $API_KEY" \
  -d '{"blockchain":"solana","threshold":2,"total_parties":3,"name":"devnet-1"}')
KEY_ID=$(echo "$key" | jq -r .id)
curl -s -H "x-api-key: $API_KEY" $API/keys/$KEY_ID | jq .      # repeat until status is "active"
```
The `address` field is the key's Solana address (base58).

## 4. Fund it with devnet SOL
```
ADDR=<the key's address>
curl -s https://api.devnet.solana.com -X POST -H 'Content-Type: application/json' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"requestAirdrop\",\"params\":[\"$ADDR\",1000000000]}"
```
(1,000,000,000 lamports = 1 SOL.) If you are rate-limited, use https://faucet.solana.com. Then
check: `curl -s -H "x-api-key: $API_KEY" $API/keys/$KEY_ID/balances | jq .`

## 5. Get a destination
Create a second Solana key the same way and use its address, or use any wallet you control set
to devnet. A first transfer to an unfunded account must be at least the rent-exempt minimum
(about 890,880 lamports), so send **1,000,000 lamports** or more.

## 6. Send a transfer
```
curl -s -X POST $API/keys/$KEY_ID/solana-transactions \
  -H 'Content-Type: application/json' -H "x-api-key: $API_KEY" \
  -d '{"destination":"<DEST_ADDRESS>","amount":"1000000"}' | jq .
```
It builds the transaction, fetches a recent blockhash, runs a 2-of-3 signing ceremony and
relays it. If you get a 403, read the reason (policy or approval); if a 503, the RPC is not set.

Check status with `GET $API/keys/$KEY_ID/solana-transactions/<signature>`, and look the
signature up at `https://explorer.solana.com/tx/<signature>?cluster=devnet`.

## 7. Save the evidence
Save the signature, the explorer link, and the commands' output (redact the API key and the
secrets). Send it and the date; the readiness document can then say that a Solana signature
from a threshold key was accepted by devnet, which has never been shown from this repo.
