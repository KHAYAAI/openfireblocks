# TRISA: direct Travel Rule exchange

TRISA (travelrule.io) is the open protocol by which virtual-asset service providers
exchange Travel Rule information directly: mutual-TLS gRPC, and a `SecureEnvelope` whose
contents (an IVMS101 identity payload and a generic transaction record) only the
recipient can decrypt. The gateway implements it in `services/api-gateway/src/travel-rule/trisa`.

## What is built

| | |
|---|---|
| Wire protocol | The upstream `.proto` files, unchanged (`proto/`, MIT, see `proto/README.md`). `TRISANetwork.Transfer` and `KeyExchange` and `TRISAHealth.Status` are served; `TransferStream` and `ConfirmAddress` answer UNIMPLEMENTED. |
| Envelope | AES-256-GCM payload, HMAC-SHA256, key and secret sealed with RSA-OAEP-SHA512, key id as `SHA256:` + unpadded base64, exactly as TRISA's reference implementation. |
| Sending | When the beneficiary provider (by LEI, else exact name) is a **trusted counterparty**, the record is sent over mutual TLS. The envelope id is the record id, so a retry is the same transfer, never a second. The acknowledgement is opened and checked before the record is marked transmitted. |
| Receiving | A mutual-TLS server (`TRISA_LISTEN`) that requires a client certificate from the CA you configure, verifies the HMAC before decrypting, accepts only the two algorithms it implements, finds the customer who holds the beneficiary address, stores the information as immutable evidence, and replies with a sealed acknowledgement. An address nobody here holds is rejected with `UNKNOWN_WALLET_ADDRESS`. |
| Counterparties | Per organisation. Added by one admin, key fetched by key exchange or supplied, and **trusted by a different person who must state the key signature they checked** against one the counterparty gave them out-of-band. A trusted counterparty cannot be edited, only revoked. Enforced by the database (migration 032). |
| Fallback | No trusted counterparty, or TRISA not configured: unchanged behaviour (provider URL, or `awaiting_transmission`). |

## What is verified, and what is not

Verified in CI (`trisa-interop.spec.ts`): an envelope this gateway seals is opened, and its
identity validated under the reference rules, by **TRISA's own Go implementation**
(`github.com/trisacrypto/trisa`, built from `test/trisa-interop`), and an envelope that
implementation seals is opened here. This found two real defects while building it (a
dropped `Any` type URL and a dropped address-line field) and the encoder now rejects
unknown fields instead of silently dropping them.

Verified (`trisa.live.spec.ts`): the full exchange between two nodes over real mutual-TLS
gRPC and Postgres, including a peer with a certificate from the wrong CA, tampered,
unsealed and wrongly-sealed envelopes, duplicate delivery, and the counterparty rules.

**Not verified: interoperability with the live TRISA network or any other vendor's node.**
That needs certificates issued by TRISA's certificate authority and a real counterparty,
neither of which this repository has. The reference-implementation test makes that
interoperability likely, not proven. **Not built:** discovery through the TRISA directory
service (counterparties are added by hand), address confirmation, the streaming RPC,
and non-TRISA protocols (OpenVASP/TRP, Sunrise).

## Becoming a TRISA member

1. Register the provider with the TRISA directory (travelrule.io) and obtain the identity
   certificate and the CA bundle. This is a business and compliance process, not code.
2. Generate a separate RSA key for sealing (recommended), and register its public key.
3. Put `tls.crt`, `tls.key`, `ca.crt` (and `sealing.key`) in a Kubernetes secret, then
   install the chart with `--set trisa.enabled=true --set trisa.secretName=<secret>
   --set trisa.sealingKeyInSecret=true`. The chart opens the TRISA port through its own
   Service and NetworkPolicy; the API stays where it was.
4. In the console API, add each counterparty, fetch its key, compare the signature with
   what they told you, and have a second administrator trust it.

Environment: `TRISA_CERT_FILE`, `TRISA_KEY_FILE`, `TRISA_CA_FILE`, `TRISA_SEALING_KEY_FILE`
(optional), `TRISA_LISTEN` (e.g. `0.0.0.0:8443`).
