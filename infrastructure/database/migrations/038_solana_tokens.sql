-- SPL tokens on Solana.
--
-- The token registry was sized for EVM addresses (VARCHAR(42), "0x" + 40 hex).
-- A Solana mint is a 32-byte key in base58: 32 to 44 characters, and
-- case-sensitive, so it is stored exactly as written (the gateway lowercases
-- EVM addresses only). Solana is chain id 501, as for native SOL.
--
-- The transaction record's asset_contract is widened the same way, so a token
-- transfer records which mint it moved.

ALTER TABLE tokens ALTER COLUMN contract_address TYPE VARCHAR(44);
ALTER TABLE signing.transactions ALTER COLUMN asset_contract TYPE VARCHAR(44);

-- Format by chain: EVM rows lowercase hex, Solana rows base58. Anything else is
-- refused here as well as in the gateway.
ALTER TABLE tokens DROP CONSTRAINT IF EXISTS tokens_contract_address_format;
ALTER TABLE tokens ADD CONSTRAINT tokens_contract_address_format CHECK (
  contract_address IS NULL
  OR (chain_id = 501 AND contract_address ~ '^[1-9A-HJ-NP-Za-km-z]{32,44}$')
  OR (chain_id <> 501 AND contract_address ~ '^0x[0-9a-f]{40}$')
);

-- The two dollar stablecoins most used on Solana, at the issuers' published
-- mint addresses. Seeded 'unverified' like the EVM rows: nothing is usable
-- until verification has read the mint from the chain and its decimals match.
INSERT INTO tokens (chain_id, contract_address, symbol, name, decimals, peg_currency, issuer, status, notes)
VALUES
  (501, 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v', 'USDC', 'USD Coin', 6, 'USD', 'Circle', 'unverified',
   'SPL token (Token program). Verification checks the mint is owned by the Token program and has 6 decimals.'),
  (501, 'Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB', 'USDT', 'Tether USD', 6, 'USD', 'Tether', 'unverified',
   'SPL token (Token program). Verification checks the mint is owned by the Token program and has 6 decimals.')
ON CONFLICT DO NOTHING;
