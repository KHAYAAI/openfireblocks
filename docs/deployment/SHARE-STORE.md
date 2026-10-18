# Where a signing party keeps its key share

A party must keep its share across restarts, and must never write it in the
clear. Two stores, chosen by environment:

| Store | Set | Notes |
|---|---|---|
| **Vault KV** (preferred) | `VAULT_ADDR`, `VAULT_TOKEN` | Encrypted by Vault's storage backend. Used if `VAULT_ADDR` is set. |
| **Encrypted file** (fallback, no Vault) | `SHARE_STORE_DIR`, `SHARE_STORE_KEY_FILE` | AES-256-GCM. One file per party per ceremony. |
| neither | | Shares live in memory only and are lost on restart. A warning, not an error. |

## The encrypted file store

- The key is 32 bytes, raw or as 64 hex characters, in a file that is mode
  0600 or stricter and **outside** `SHARE_STORE_DIR`. The party refuses to
  write otherwise.
- The authenticated data names the party and the ceremony: a file copied to
  another party's directory, or renamed to another ceremony, does not open.
  Any change to the file fails authentication.
- Retiring a share overwrites and removes its file.

What it does not protect against: someone with a shell on the party's host
can read the key file and the shares. Put the key on a separate secret
mount, a tmpfs filled at boot, or unwrap it from an HSM or KMS, and keep each
party on its own host and owner (`SEPARATE-HOSTS.md`). The Helm chart does
not provision this store yet (it needs a persistent volume per party); use
it on separately deployed parties.

## How parties trust each other

Unchanged and already in place: mutual TLS, with the party id bound to the
certificate's common name so a party cannot speak as another; an optional
ceremony authoriser (a signature from a key the platform's own hosts cannot
reach) over operation, ceremony and message hash with a bounded age. The
threshold must be a majority (`t+1 >= floor(n/2)+1`), enforced where the
shares are made.
