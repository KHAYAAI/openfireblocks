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

## TRISA

TRISA (travelrule.io, Apache-2.0) is an open protocol for the counterparty
exchange. The practical route is to run a TRISA node (`trisacrypto/envoy`),
which needs mTLS certificates from the TRISA directory for a registered VASP,
and put a small adapter in front of it that implements the contract above.

**Not built, and why:** a direct TRISA integration. It needs TRISA directory
registration and certificates, and a counterparty on a TRISA network to test
against; none are available to this repository, and Envoy's request shapes
were not verifiable from here. Treat TRISA as an integration to do during a
pilot with a real counterparty, not as something this platform already speaks.
