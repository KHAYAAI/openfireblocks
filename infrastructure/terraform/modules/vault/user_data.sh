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

# Download and install Vault
VAULT_VERSION="${vault_version}"
wget https://releases.hashicorp.com/vault/${VAULT_VERSION}/vault_${VAULT_VERSION}_linux_amd64.zip
unzip vault_${VAULT_VERSION}_linux_amd64.zip
mv vault /usr/local/bin/
rm vault_${VAULT_VERSION}_linux_amd64.zip

# Create vault user
useradd -r -d /var/lib/vault vault || true

# Create Vault directories
mkdir -p /var/lib/vault /etc/vault/tls
chown vault:vault /var/lib/vault /etc/vault

# Create Vault configuration
cat > /etc/vault/vault.hcl <<'EOF'
ui = true

storage "s3" {
  bucket         = "${s3_bucket_name}"
  key            = "vault"
  region         = "aws_region"
  kms_key_id     = "${kms_key_id}"
  path           = "/openfireblocks-vault"
}

listener "tcp" {
  address       = "0.0.0.0:8200"
  tls_cert_file = "/etc/vault/tls/vault.crt"
  tls_key_file  = "/etc/vault/tls/vault.key"
}

seal "awskms" {
  region     = "aws_region"
  kms_key_id = "${kms_key_id}"
}

ha_storage "raft" {
  path = "/var/lib/vault/raft"
}

api_addr      = "https://127.0.0.1:8200"
cluster_addr  = "https://127.0.0.1:8201"
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
