# TRISA protocol definitions

These `.proto` files are copied unchanged from https://github.com/trisacrypto/trisa
(MIT licence, Copyright (c) 2021 TRISA; the IVMS101 definitions carry their own
notices in their headers). They are the wire format of the TRISA Travel Rule network:
a node that sends or receives these messages over mutual TLS gRPC speaks to any other
TRISA node.

* `trisa/api/v1beta1/api.proto`, `errors.proto` -- the `TRISANetwork` service and the
  `SecureEnvelope`.
* `trisa/data/generic/v1beta1/transaction.proto` -- the generic transaction payload.
* `ivms101/*.proto` -- the IVMS101 identity payload.

Do not edit them here. To update, copy the new versions from upstream and run
`npx jest src/travel-rule/trisa`, which includes a cross-check against TRISA's own Go
reference implementation (`test/trisa-interop`).
