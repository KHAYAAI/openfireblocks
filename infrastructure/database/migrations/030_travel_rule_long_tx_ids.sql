-- A Solana transaction id is an 88-character base58 signature. The Travel Rule
-- record's tx_hash was VARCHAR(80), sized for an EVM hash, so completing the
-- record after a Solana transfer had been signed and broadcast failed with
-- "value too long" and the caller got a 500 for a transfer that had gone out.
-- Found by running a real key through the real stack, not by any unit test.
ALTER TABLE travel_rule_records ALTER COLUMN tx_hash TYPE VARCHAR(255);
