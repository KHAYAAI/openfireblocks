# Tokenisation: issuing and administering security tokens

For an asset manager, bank or fintech that wants to issue a regulated token (a fund unit, a
bond, a deposit token) and run it from the same governed platform it already uses for
custody.

## What is built

* **A contract**, `contracts/PermissionedToken.sol`: an ERC-20 where a transfer works only
  if the contract is not paused, neither party is frozen, and **both are holders the issuer
  admitted**. The owner can admit and remove holders, freeze, pause, mint up to a fixed cap,
  burn (redemption) and force a transfer between holders (lost-key recovery, legal orders).
  A holder with a balance cannot be removed, ownership moves in two steps, and `canTransfer`
  reports why a transfer would fail. 12 tests on a real EVM.
* **Registration that does not trust the issuer's say-so.** The issuer deploys the contract
  with any wallet, owner set to the address of their threshold key, and registers the
  address. The platform reads the code from the chain and compares it with the audited
  artifact (modulo the two constructor immutables), checks the owner is that key, and reads
  name, symbol, decimals and cap **from the contract**. Some other contract, or the right
  contract owned by someone else, is refused.
* **Every administrative act is held for approval, always.** Admit, remove, freeze,
  unfreeze, pause, unpause, mint, burn, force-transfer and propose-owner are signed
  transactions from the issuer's threshold key, and each waits for the organisation's
  approvers whatever the spending policy says: none of them moves value, which is exactly
  why a value limit would wave them through. Approvers see the act in words (*"FORCE-move
  50000 ACME30 from 0x… to 0x…, without the holder's consent"*). The person who asked cannot
  approve it. The act is **simulated against the chain first**, so approvers are never asked
  to approve something the contract will refuse.
* **A holder register with a KYC reference required.** The issuer's own book of holders
  (name, wallet, where the verification file is). A holder cannot be admitted on chain
  without one. The platform stores the reference, never the documents.
* **A cap table read from the chain**, with percentage of supply, and a flag for supply held
  by addresses with no holder record (admitted outside the platform).
* **A register of every act** asked for, linked to its approval and, once sent, its
  transaction hash.

## How the signing path stays narrow

The platform refuses contract calls it cannot read unless an organisation turns on a blanket
"arbitrary contract calls" switch. Tokenisation does not use that switch. The module vouches
for exactly the calls above, to a registered token's address, with zero value, and only if
the calldata is byte-for-byte what encoding the decoded arguments produces (no trailing
bytes, no dirty padding). Anything else is refused as before.

## API

`GET securities/contract` (the artifact to deploy), `POST securities` (register),
`GET securities`, `GET securities/:id`, `GET|POST securities/:id/holders`,
`GET securities/:id/cap-table`, `GET|POST securities/:id/ops` (`op` is one of `admit`,
`remove`, `freeze`, `unfreeze`, `pause`, `unpause`, `mint`, `burn`, `force_transfer`,
`propose_owner`, with `holder` / `to` / `from` / `next` / `amount` as that act needs).
Registering and recording holders is an administrator's; asking for an act is an
operator's or administrator's; approving is an approver's. If the organisation enforces an
address whitelist, the token's address must be on it.

## What is not built, and what you still need

* **The contract has not been independently audited.** Do not deploy it on a public chain
  with real value before it has been.
* **Not a legal wrapper.** Whether the token is a security, who may hold it, the holder's
  rights, prospectus, transfer-agent registration and investor-eligibility rules are the
  issuer's counsel's question. The contract enforces who may transfer; this platform records
  who the issuer says the holders are.
* **No primary-market workflow.** No subscription, allocation, coupon or dividend
  distribution, corporate actions, or on-chain voting. Mint and burn are the primitives.
* **EVM only**, one contract version, no upgradeability (deliberately: an upgradeable token
  is one whose owner can change the rules after the fact).
* **No secondary-market venue.** Holders trade peer to peer, between admitted wallets.
* **No deployment from the platform.** The issuer deploys with their own wallet; the platform
  verifies. (A deployment from the threshold key would need contract-creation support in the
  signing path, which has not been added.)
* Tested against a local dev chain, not a public network.
