# Travel Rule transmission

What the platform does: before a transfer above the reporting threshold is
signed it must carry IVMS101 originator and beneficiary details; they are
stored immutably with the transfer; after signing they are sent to a provider,
and a failed or pending record can be sent again from the console
(**Travel Rule → record → Send now**) or `POST
/organisations/:id/travel-rule/records/:recordId/transmit`.

## The provider contract

Set `TRAVEL_RULE_PROVIDER_URL` (and optionally `TRAVEL_RULE_PROVIDER_TOKEN`).
The platform POSTs `{ "ivms101": {...}, "txHash": "0x..." }` with an
`Idempotency-Key` equal to the record id (so a retry cannot be filed twice)
and a bearer token, and expects 2xx, optionally with `{ "reference": "..." }`.
Anything else is recorded as a failed attempt and kept for a retry.

## TRISA (direct provider-to-provider exchange)

Built: the gateway speaks the TRISA wire protocol itself, as sender and receiver, so a
transfer to a beneficiary provider you have a trusted TRISA relationship with goes
straight to that provider in an envelope only it can open, with no intermediary. Anything
else still uses the provider URL above, or waits for export. Full detail, including what
is verified and what is not, in [TRISA.md](TRISA.md).
