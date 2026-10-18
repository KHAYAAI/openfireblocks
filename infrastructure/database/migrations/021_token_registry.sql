-- Tokens, and the fact that a token transfer is invisible to every control
-- this platform has.
--
-- The signing path has always been able to carry contract calldata:
-- SignTransactionDto has a `data` field, it goes into the bytes that get
-- threshold-signed, and a customer who ABI-encodes transfer(address,uint256)
-- themselves gets a real MPC-signed USDC transfer out of the platform. What
-- does NOT happen is any of the governing. An ERC-20 transfer puts the
-- recipient and the amount inside the calldata, so the fields every control
-- reads are:
--
--   to_address -> the token contract, identical for every transfer of that
--                 token, so a counterparty whitelist either allows all of
--                 them or none;
--   amount     -> "0", so an amount limit of a thousand rand passes a
--                 fifty million rand transfer, and the daily aggregate that
--                 decides whether a regulatory filing is due sums to
--                 nothing.
--
-- That last one is not a product gap, it is a reporting failure: the
-- platform would be under a statutory duty to file and would have no
-- record that the threshold was ever crossed.
--
-- Two things are created here. A registry of the tokens the platform will
-- transact at all, and somewhere on the transaction record to put what a
-- transfer *actually* moved, so the controls read the transfer rather than
-- its envelope.

-- ---------------------------------------------------------------------
-- The registry
-- ---------------------------------------------------------------------

-- Platform-wide rather than per-customer, and so deliberately not
-- RLS-scoped: which contract is USDC on Ethereum is a fact about the
-- chain, not about a tenant. Letting each customer bring their own
-- address for a given symbol is precisely the hole this table exists to
-- close -- "send me 10000 USDC" where USDC is a contract the customer
-- named is not custody, it is a confused deputy.
CREATE TABLE IF NOT EXISTS tokens (
  token_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- The EVM chain id, not a name. A token is only meaningful against one
  -- chain, and the same address on two chains is two different contracts.
  chain_id         INT NOT NULL,

  -- Nullable, because a row is allowed to exist before anyone has
  -- confirmed the address. See the status column: a token with no address
  -- names an asset the platform is prepared to handle and cannot yet
  -- move, which is a more useful state than silence when the operator's
  -- next job is to go and get the address from the issuer.
  --
  -- Stored lowercase. EIP-55 gives every address two spellings and
  -- comparing them case-sensitively is how a registered token fails to
  -- match itself.
  contract_address VARCHAR(42),

  symbol           VARCHAR(32)  NOT NULL,
  name             VARCHAR(128) NOT NULL,

  -- The single most dangerous number in this table. USDC is 6 decimals
  -- and DAI is 18; reading one for the other is a 10^12 error in an
  -- amount limit, in a balance, and in a regulatory aggregate. It is
  -- checked against the contract's own decimals() before the token can be
  -- used -- see status.
  decimals         SMALLINT NOT NULL CHECK (decimals BETWEEN 0 AND 36),

  -- What the token claims to be worth, for the controls that work in
  -- money rather than in base units. 'USD' for USDC/USDT/DAI, 'ZAR' for
  -- the rand-referenced tokens, NULL for anything that is not pegged.
  --
  -- A peg is a claim by an issuer, not a fact, and this column is not a
  -- price feed. It is used where treating one unit as one unit of the peg
  -- currency is the conservative reading -- notably regulatory
  -- aggregation, where under-counting is the failure that matters and a
  -- depegged stablecoin trading at 0.97 would otherwise let a filing
  -- threshold be dodged by three percent.
  peg_currency     CHAR(3),

  -- Who issues it. A rand stablecoin's issuer is a South African
  -- accountable institution and an operator being asked to confirm an
  -- address needs to know who to confirm it with.
  issuer           VARCHAR(128),

  -- awaiting_address -- named, no contract address confirmed yet
  -- unverified       -- address present, never checked against the chain
  -- verified         -- symbol() and decimals() matched this row
  -- suspended        -- deliberately withdrawn from use
  --
  -- Only 'verified' may be transacted. That gate is the reason it is safe
  -- for this migration to seed addresses at all: a seeded address that is
  -- wrong cannot move money, because the on-chain check will disagree
  -- with the row and the token stays unusable. Failing closed on a wrong
  -- address is the difference between a configuration error and a loss.
  status           VARCHAR(24) NOT NULL DEFAULT 'awaiting_address'
                   CHECK (status IN ('awaiting_address','unverified','verified','suspended')),

  -- What the chain actually said, kept separately from what this row
  -- claims. When they disagree the operator needs to see both; collapsing
  -- them into a pass/fail throws away the only evidence of which side is
  -- wrong.
  verified_symbol   VARCHAR(32),
  verified_decimals SMALLINT,
  verified_at       TIMESTAMP,
  verification_error TEXT,

  notes            TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMP NOT NULL DEFAULT NOW()
);

-- One row per contract per chain. Partial, because rows awaiting an
-- address all have NULL there and NULLs do not conflict.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_chain_contract
  ON tokens (chain_id, contract_address)
  WHERE contract_address IS NOT NULL;

-- A symbol is not unique across chains -- USDC exists on many -- but it
-- must be unique *on* a chain, or "send 100 USDC on chain 1" has more
-- than one answer.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_chain_symbol
  ON tokens (chain_id, upper(symbol));

CREATE INDEX IF NOT EXISTS idx_tokens_peg ON tokens (peg_currency)
  WHERE peg_currency IS NOT NULL;

-- Reference data: everyone reads it, nobody but an operator writes it.
GRANT SELECT ON tokens TO app;
GRANT ALL PRIVILEGES ON tokens TO app_admin;

-- ---------------------------------------------------------------------
-- What the transaction actually moved
-- ---------------------------------------------------------------------

-- signing.transactions records the envelope: to_address is where the
-- transaction was sent and amount is the native value attached to it. For
-- a token transfer both are true and both are useless -- the envelope
-- went to a contract carrying nothing.
--
-- These columns record the transfer that envelope performs, decoded from
-- the calldata by the same code that built it, so a reader of this table
-- can answer "who received how much of what" without an ABI decoder. For
-- a plain native transfer they mirror to_address and amount, which keeps
-- every consumer on one pair of columns instead of branching.
ALTER TABLE signing.transactions
  ADD COLUMN IF NOT EXISTS asset_symbol     VARCHAR(32),
  ADD COLUMN IF NOT EXISTS asset_contract   VARCHAR(42),
  ADD COLUMN IF NOT EXISTS asset_decimals   SMALLINT,
  ADD COLUMN IF NOT EXISTS asset_peg        CHAR(3),
  ADD COLUMN IF NOT EXISTS effective_to     VARCHAR(255),
  ADD COLUMN IF NOT EXISTS effective_amount VARCHAR(255);

COMMENT ON COLUMN signing.transactions.effective_to IS
  'Who receives the value: the ERC-20 recipient for a token transfer, the same as to_address for a native one.';
COMMENT ON COLUMN signing.transactions.effective_amount IS
  'How much moves, in the asset''s own base units. Never the native value field for a token transfer, which is 0.';
COMMENT ON COLUMN signing.transactions.asset_peg IS
  'Peg currency of the moved asset at the time of the transfer, copied from the registry so a later registry edit cannot rewrite history.';

-- Aggregating a day of transfers for one customer on one chain is the
-- query every regulatory filing runs.
CREATE INDEX IF NOT EXISTS idx_signing_transactions_asset_day
  ON signing.transactions (customer_id, chain, created_at)
  INCLUDE (asset_symbol, effective_amount);

-- Backfill: every transaction that predates this migration is a native
-- one, because there was no route that could produce anything else. Rows
-- with calldata are the exception -- somebody hand-encoded a contract
-- call through POST /keys/:id/transactions -- and those are deliberately
-- left NULL rather than guessed at. A NULL here reads as "nobody decoded
-- this", which is the truth; filling it with to_address would assert that
-- a contract call was a payment to the contract.
UPDATE signing.transactions
   SET effective_to     = to_address,
       effective_amount = amount,
       asset_symbol     = 'NATIVE'
 WHERE effective_to IS NULL
   AND (data IS NULL OR data = '' OR data = '0x');

-- ---------------------------------------------------------------------
-- Calldata nobody can account for
-- ---------------------------------------------------------------------

-- Contract calls that are not a token transfer the platform understands.
--
-- Once calldata is decoded before policy, there are three kinds of
-- transaction: a native transfer, a transfer of a registered token, and
-- everything else. That third kind is the one where the platform signs
-- bytes whose effect it cannot describe -- the recipient and amount handed
-- to the policy engine are the contract and zero, which is exactly the
-- blindness this work exists to remove.
--
-- Gated per tenant rather than removed, on the same reasoning as
-- raw_digest_signing_enabled: calling a contract that is not an ERC-20 is
-- a real requirement, and there is no other route for it. Turning it on is
-- a decision somebody makes on the record. Off by default means the
-- platform's guarantee -- that every signature was governed by a control
-- which understood what it was signing -- holds for every tenant who has
-- not explicitly traded it away.
ALTER TABLE customers
  ADD COLUMN IF NOT EXISTS arbitrary_contract_calls_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN customers.arbitrary_contract_calls_enabled IS
  'Allows calldata that is not a registered token transfer. Policy cannot determine the recipient or amount of such a call, so these are always escalated for approval.';

-- ---------------------------------------------------------------------
-- Seed
-- ---------------------------------------------------------------------

-- Everything below lands as 'unverified' or 'awaiting_address'. Nothing
-- here can move money until an operator runs verification against a node
-- and the contract agrees with the row.
--
-- Addresses are given only where they are widely published and stable.
-- Where this file has no address, that is not an oversight: putting an
-- unconfirmed address in a custody platform's registry is how money goes
-- to a contract nobody vetted, and the correct source for a token's
-- address is its issuer, not a migration file.

INSERT INTO tokens (chain_id, contract_address, symbol, name, decimals, peg_currency, issuer, status, notes)
VALUES
  -- Ethereum mainnet, dollar-referenced.
  (1, '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48', 'USDC', 'USD Coin', 6, 'USD', 'Circle', 'unverified',
   'Verify against a node before use; the verification check is what makes this row trustworthy, not this file.'),
  (1, '0xdac17f958d2ee523a2206206994597c13d831ec7', 'USDT', 'Tether USD', 6, 'USD', 'Tether', 'unverified',
   'Non-standard ERC-20: transfer() returns no value on this contract. Handled by the decoder, noted here because it surprises people.'),
  (1, '0x6b175474e89094c44da98b954eedeac495271d0f', 'DAI', 'Dai Stablecoin', 18, 'USD', 'MakerDAO', 'unverified',
   '18 decimals, unlike USDC and USDT. The registry exists so this difference is data rather than an assumption.')
ON CONFLICT DO NOTHING;

-- South African rand stablecoins.
--
-- Named without addresses on purpose. These are the assets the platform
-- is built to custody for a South African settlement customer, and the
-- rows carry their peg, decimals and issuer so that the compliance path,
-- the balance display and the policy engine all treat them as rand from
-- the moment an address is filled in.
--
-- An operator completes each row with the address obtained from the
-- issuer directly, then runs verification. Until then the token is
-- registered, visible, and unusable -- which is the correct state for an
-- asset whose contract nobody has confirmed.
INSERT INTO tokens (chain_id, symbol, name, decimals, peg_currency, issuer, status, notes)
VALUES
  (1,     'ZARP',  'ZARP Stablecoin',      18, 'ZAR', 'ZARP Stablecoin (Pty) Ltd', 'awaiting_address',
   'Rand-referenced. Confirm the Ethereum mainnet address with the issuer, then verify. Decimals recorded as 18 and checked on verification.'),
  (137,   'ZARP',  'ZARP Stablecoin',      18, 'ZAR', 'ZARP Stablecoin (Pty) Ltd', 'awaiting_address',
   'Polygon deployment. A separate row because a separate chain is a separate contract.'),
  (8453,  'ZARP',  'ZARP Stablecoin',      18, 'ZAR', 'ZARP Stablecoin (Pty) Ltd', 'awaiting_address',
   'Base deployment.'),
  (42161, 'ZARP',  'ZARP Stablecoin',      18, 'ZAR', 'ZARP Stablecoin (Pty) Ltd', 'awaiting_address',
   'Arbitrum deployment.'),
  (137,   'XZAR',  'xZAR Stablecoin',      18, 'ZAR', 'xZAR',                      'awaiting_address',
   'Rand-referenced. Confirm decimals on verification -- recorded as 18 but not confirmed against the contract.')
ON CONFLICT DO NOTHING;

COMMENT ON TABLE tokens IS
  'Tokens the platform will transact. Only status=verified is usable; verification compares symbol() and decimals() on the contract against this row.';
