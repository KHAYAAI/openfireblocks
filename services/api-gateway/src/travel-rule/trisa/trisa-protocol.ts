import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import * as protobuf from 'protobufjs';
import { isAbsolute, join } from 'path';

// The TRISA wire protocol: the gRPC service and the protobuf messages, loaded from
// the upstream .proto files (see proto/README.md) rather than re-described by hand,
// so that what this node sends is what the definition says and not what we remember
// of it.

const PROTO_ROOT = join(__dirname, 'proto');
const FILES = ['trisa/api/v1beta1/api.proto', 'ivms101/identity.proto', 'trisa/data/generic/v1beta1/transaction.proto'];

export const IDENTITY_TYPE_URL = 'type.googleapis.com/ivms101.IdentityPayload';
export const TRANSACTION_TYPE_URL = 'type.googleapis.com/trisa.data.generic.v1beta1.Transaction';

export interface Protocol {
  root: protobuf.Root;
  Payload: protobuf.Type;
  IdentityPayload: protobuf.Type;
  Transaction: protobuf.Type;
  // grpc service definitions
  network: grpc.ServiceDefinition;
  health: grpc.ServiceDefinition;
  NetworkClient: grpc.ServiceClientConstructor;
}

let cached: Protocol | undefined;

export function protocol(): Protocol {
  if (cached) return cached;
  const root = new protobuf.Root();
  root.resolvePath = (_origin, target) => (isAbsolute(target) || target.startsWith('google/') ? target : join(PROTO_ROOT, target));
  root.loadSync(FILES, { keepCase: false });
  root.resolveAll();

  const def = protoLoader.loadSync(FILES, {
    keepCase: false, longs: String, enums: String, defaults: true, oneofs: true, includeDirs: [PROTO_ROOT],
  });
  const pkg = grpc.loadPackageDefinition(def) as any;
  const api = pkg.trisa.api.v1beta1;
  cached = {
    root,
    Payload: root.lookupType('trisa.api.v1beta1.Payload'),
    IdentityPayload: root.lookupType('ivms101.IdentityPayload'),
    Transaction: root.lookupType('trisa.data.generic.v1beta1.Transaction'),
    network: api.TRISANetwork.service,
    health: api.TRISAHealth.service,
    NetworkClient: api.TRISANetwork,
  };
  return cached;
}

// TRISA's own error codes (errors.proto), for the ones this node sends.
export const ErrorCode = {
  UNAVAILABLE: 1,
  INTERNAL_ERROR: 49,
  REJECTED: 50,
  UNKNOWN_WALLET_ADDRESS: 51,
  UNKNOWN_IDENTITY: 52,
  UNSUPPORTED_CURRENCY: 60,
  INVALID_SIGNATURE: 105,
  INVALID_KEY: 106,
  ENVELOPE_DECODE_FAIL: 107,
  BAD_REQUEST: 150,
} as const;
