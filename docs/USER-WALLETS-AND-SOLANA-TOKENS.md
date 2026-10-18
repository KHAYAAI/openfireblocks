# User wallets and Solana tokens

Two additions to the API, from 2026-10-16:

1. **Per-user wallets**, for wallet-as-a-service: a wallet for each of a customer's own users.
2. **Solana SPL tokens**, such as USDC and USDT on Solana.

Both use the existing transfer path. The organisation's freeze, address whitelist, spending policy,
approvals and Travel Rule apply to them unchanged.

## Per-user wallets

A **user wallet** is a set of threshold keys, one per chain, held for one of your users. It is labelled
with your own reference for that user.

- **Each key is a real 2-of-3 threshold key with its own key ceremony.** It is not an address derived
  from a shared key. This keeps one user's funds cryptographically separate from another's. It also means
  the signing parties' reviewed cryptography is unchanged.
- **The cost is one key ceremony per user per chain**, so only create the chains a user needs. You can
  add a chain later with the same call.
- **The platform keeps no personal information about your users.**
  - `externalRef` is your identifier, such as an account number or a UUID.
  - It accepts only `A-Z a-z 0-9 . _ : -`, up to 128 characters, so an email address or a name with a space is refused.
  - Keep names, emails and ID numbers out of it, and out of `label` too.

### API (API key)

| Route | What it does |
|---|---|
| `POST /wallets` | Create a wallet. Body: `{ "externalRef": "acct-10042", "blockchains": ["solana", "ethereum"], "label": "optional" }`. **Idempotent on `externalRef`:** calling it again returns the same wallet, and creates keys only for chains it doesn't have yet. Optional `threshold` and `totalParties` (default 2 of 3) |
| `GET /wallets?limit=50&before=<ISO time>` | List wallets, newest first. The response includes a `next` cursor |
| `GET /wallets/by-ref/:externalRef` | Find a wallet by your reference |
| `GET /wallets/:walletId` | The wallet, with each key's chain, status and address |
| `GET /wallets/:walletId/addresses` | Deposit addresses, per chain |
| `GET /wallets/:walletId/balances?chainId=1` | Balances, per chain. `chainId` is needed only for EVM keys |
| `POST /wallets/:walletId/transfers` | Send from the user's key on one chain. Body: `blockchain`, plus the usual transfer fields for that chain. The response is `200` when the transfer completes, or `202` with an approval id when approvers must sign off |
| `POST /wallets/:walletId/freeze` | Freeze the wallet, with a `reason` (3–500 characters). Your fraud system can call this |

**Keys are ready only when their key ceremony finishes.** A new wallet's keys start as `pending_dkg`. Poll
`GET /wallets/:walletId` until each key is `active` and has an address, then show the address to your user.

### Freezing

- **A frozen wallet's keys sign nothing.** The check sits in the one place every signing route loads its
  key. It also runs when a transfer is parked for approval, so approvers are never asked to approve a
  transfer that can't run.
- **The API key can freeze, but it can't unfreeze.** Unfreezing needs an organisation admin in the
  console (`POST /organisations/:id/wallets/:walletId/unfreeze`). A stolen API key therefore can't undo a
  freeze.
- **Every freeze and unfreeze is written to the audit log** with its reason.

### What the database enforces

- A wallet's owner and reference can never change.
- Wallets and their key links are never deleted.
- A wallet's keys are fixed once linked.
- A key can only be linked to a wallet of the same organisation, on the stated chain.
- Row-level security keeps each organisation to its own wallets.

All of this is tested against real Postgres in `services/api-gateway/src/wallets/wallets.live.spec.ts`.

### Not included yet

- **Per-user spending limits as a wallet setting.** Per-key policies already exist and apply, because each
  user's key is a separate key.
- **Moving a user's balance to a treasury.** Use a sweep rule on the user's key.
- **A console screen for wallets.** The organisation routes for listing, balances, freeze and unfreeze
  exist; there's no screen for them yet.

## Solana SPL tokens

Send a token with the same route as SOL. Add `token`, either the registered symbol or the mint address:

```http
POST /keys/:keyId/solana-transactions
{ "destination": "<recipient WALLET address>", "amount": "2500000", "token": "USDC" }
```

- **`amount` is in the token's base units.** For USDC (6 decimals), 2,500,000 is 2.5 USDC. Without
  `token`, `amount` is lamports as before.
- **`destination` is the recipient's wallet address, not their token account.** The platform derives both
  token accounts. If the recipient doesn't have a token account for this token yet, the platform creates
  it, and your key pays its rent. The amount is read from the network each time (on devnet on 2026-10-18 it was 1,488,440 lamports, about 0.0015 SOL; on mainnet it has been about 0.002 SOL).
- **`/transfers` and `/wallets/:id/transfers` accept the same `token` field.** An approver sees the
  parked transfer as the token ("2.5 USDC"), not as SOL.
- **`GET /keys/:keyId/balances` on a Solana key** lists SOL and every verified Solana token.

### Before signing, the signer refuses:

These all fail after the parties have signed, so they are checked before the key ceremony.

- A token that isn't registered and verified on chain 501.
- A mint whose decimals on chain differ from the registry's.
- A destination that is a token account or a mint, rather than a wallet.
- Fewer tokens held than the amount.
- A source or destination token account that the token's issuer has frozen.
- Too little SOL for the fee, plus the new token account's rent where one is created.

### The token registry

- **Solana is chain 501.**
  - Mints are stored exactly as written: base58 is case-sensitive, so they aren't lowercased like EVM addresses.
  - The database checks the format for each chain (migration 038).
- **USDC and USDT on Solana are seeded as `unverified`** at the issuers' published mint addresses.
  - An operator runs verification, which reads each mint from the chain. A mint becomes usable only if the Token program owns it and its decimals match.
  - Solana has no on-chain symbol, so the mint address is the identity. Always take it from the issuer.
- **Token-2022 mints are refused.** They can carry transfer fees, transfer hooks and confidential
  balances, each of which changes what "send N tokens" means.

### How it was checked

- **Token account derivation** (program-derived and associated token addresses) gives the same answers as
  `@solana/spl-token` 0.4.8 and `@solana/web3.js` 1.95.3, including spl-token's own published test vector.
- **The two-instruction message** (CreateIdempotent, then TransferChecked) decompiles to instructions
  identical to those from spl-token's own instruction builders: the same accounts, signer and writable
  flags, and data. The vectors are in `services/mpc-signer/chains/solana_spl_test.go`.
- **On devnet:** token transfers ran on Solana devnet on 2026-10-17 and 2026-10-18, including one to a recipient with
  no token account, where the platform created it and the sending key paid the rent
  (`docs/evidence/2026-10-18-staging-checks.md`).
