#!/bin/bash
set -e

# Update system
apt-get update
apt-get install -y \
  curl \
  jq \
  wget \
  unzip \
  awscli

# Download and install Vault.
# NOTE: this whole file is rendered through Terraform's templatefile(), which
# treats every dollar-brace sequence as its own interpolation syntax -- even
# inside a bash comment -- so bash-only variable references below use the
# doubled-dollar escape to come through as literal bash, not a Terraform var.
VAULT_VERSION="${vault_version}"
wget https://releases.hashicorp.com/vault/$${VAULT_VERSION}/vault_$${VAULT_VERSION}_linux_amd64.zip
unzip vault_$${VAULT_VERSION}_linux_amd64.zip
mv vault /usr/local/bin/
rm vault_$${VAULT_VERSION}_linux_amd64.zip

# Create vault user
useradd -r -d /var/lib/vault vault || true

# Create Vault directories
mkdir -p /var/lib/vault /etc/vault/tls
chown vault:vault /var/lib/vault /etc/vault

# Write the listener's TLS cert + key (shared across the cluster; see main.tf
# for why this is a tls-provider self-signed cert rather than ACM). Key is
# 0600 root:vault, readable only by the vault user the daemon runs as.
cat > /etc/vault/tls/vault.crt <<'EOF'
${tls_cert_pem}
EOF

cat > /etc/vault/tls/vault.key <<'EOF'
${tls_private_key_pem}
EOF

chown vault:vault /etc/vault/tls/vault.crt /etc/vault/tls/vault.key
chmod 644 /etc/vault/tls/vault.crt
chmod 600 /etc/vault/tls/vault.key

# Each node must advertise its own private IP, not localhost -- with
# 127.0.0.1 every node in the Raft cluster tries to reach every other node's
# own loopback instead of that node's real address, so the cluster never
# actually forms. IMDSv2 (token-based) is required since IMDSv1 may be
# disabled on the launch template.
IMDS_TOKEN=$(curl -sX PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 60")
LOCAL_IP=$(curl -sH "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/local-ipv4)

# Create Vault configuration. Unquoted heredoc so $LOCAL_IP expands; the
# dollar-brace references below were already substituted by Terraform's
# templatefile() before this script became user_data, so they are plain
# text by the time bash sees this file -- not bash variables.
cat > /etc/vault/vault.hcl <<EOF
ui = true

storage "s3" {
  bucket         = "${s3_bucket_name}"
  key            = "vault"
  region         = "${aws_region}"
  kms_key_id     = "${kms_key_id}"
  path           = "/openfireblocks-vault"
}

listener "tcp" {
  address       = "0.0.0.0:8200"
  tls_cert_file = "/etc/vault/tls/vault.crt"
  tls_key_file  = "/etc/vault/tls/vault.key"
}

seal "awskms" {
  region     = "${aws_region}"
  kms_key_id = "${kms_key_id}"
}

ha_storage "raft" {
  path = "/var/lib/vault/raft"
}

api_addr      = "https://$LOCAL_IP:8200"
cluster_addr  = "https://$LOCAL_IP:8201"
EOF

# Configure systemd service
cat > /etc/systemd/system/vault.service <<'EOF'
[Unit]
Description=Vault
Requires=network-online.target
After=network-online.target
ConditionFileNotEmpty=/etc/vault/vault.hcl

[Service]
Type=notify
ProtectSystem=full
ProtectHome=yes
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
SecureBits=keep-caps
AmbientCapabilities=CAP_IPC_LOCK
Capabilities=CAP_SYSLOG+ep CAP_IPC_LOCK+ep
LimitNOFILE=65536
LimitNPROC=512
KillMode=process
KillSignal=SIGINT
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
StartLimitBurst=3
StartLimitInterval=60s
ExecStart=/usr/local/bin/vault server -config=/etc/vault/vault.hcl
ExecReload=/bin/kill -HUP $MAINPID
User=vault
Group=vault

[Install]
WantedBy=multi-user.target
EOF

# Enable and start Vault
systemctl daemon-reload
systemctl enable vault
systemctl start vault

# Log status
echo "Vault startup initiated at $(date)" >> /var/log/vault-startup.log
