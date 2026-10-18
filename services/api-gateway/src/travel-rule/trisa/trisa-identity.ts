import type { Ivms101Payload, Person } from '../travel-rule';
import * as protobuf from 'protobufjs';
import { IDENTITY_TYPE_URL, Protocol, TRANSACTION_TYPE_URL } from './trisa-protocol';

// protobufjs's fromObject drops fields it does not recognise without a word. In a
// compliance message that is the worst kind of failure -- the record looks sent and a
// field the regulator needs is not in it -- so every field name is checked against the
// definition and an unknown one is an error.
export function strictMessage(type: protobuf.Type, obj: Record<string, any>, path = type.name): protobuf.Message {
  for (const [k, v] of Object.entries(obj)) {
    const f = type.fields[k];
    if (!f) throw new Error(`${path}.${k} is not a field of ${type.fullName.slice(1)}`);
    f.resolve();
    if (f.resolvedType instanceof protobuf.Type && v && typeof v === 'object') {
      const sub = f.resolvedType;
      if (Array.isArray(v)) v.forEach((item, i) => item && typeof item === 'object' && strictMessage(sub, item, `${path}.${k}[${i}]`));
      else strictMessage(sub, v, `${path}.${k}`);
    }
  }
  return type.fromObject(obj);
}

// Maps the platform's IVMS101 record onto the protobuf IdentityPayload TRISA carries,
// and the generic transaction that ties it to the chain. The record is the platform's
// (JSON, as stored); this is the only place that knows the protobuf shape.

const NATIONAL_ID_CODES = new Set(['MISC', 'ARNU', 'CCPT', 'RAID', 'DRLC', 'FIIN', 'TXID', 'SOCS', 'IDCD', 'LEIX']);
const NETWORKS: Record<number, string> = { 0: 'BTC', 1: 'ETH', 10: 'ETH', 56: 'BNB', 118: 'ATOM', 137: 'MATIC', 501: 'SOL', 8453: 'ETH', 42161: 'ETH' };

export function networkName(chainId: number): string {
  return NETWORKS[chainId] ?? `CHAIN-${chainId}`;
}

function natId(id?: { nationalIdentifier: string; nationalIdentifierType: string; countryOfIssue?: string }) {
  if (!id?.nationalIdentifier) return undefined;
  const code = String(id.nationalIdentifierType ?? 'MISC').toUpperCase();
  return {
    nationalIdentifier: id.nationalIdentifier,
    nationalIdentifierType: `NATIONAL_IDENTIFIER_TYPE_CODE_${NATIONAL_ID_CODES.has(code) ? code : 'MISC'}`,
    ...(id.countryOfIssue ? { countryOfIssue: id.countryOfIssue } : {}),
  };
}

function address(a?: { addressLine: string[]; country: string }) {
  if (!a) return [];
  return [{ addressType: 'ADDRESS_TYPE_CODE_GEOG', addressLine: a.addressLine, country: a.country }];
}

export function toProtoPerson(p: Person): Record<string, unknown> {
  if (p.naturalPerson) {
    const n = p.naturalPerson;
    return {
      naturalPerson: {
        name: { nameIdentifiers: [{ primaryIdentifier: n.name.primaryIdentifier, secondaryIdentifier: n.name.secondaryIdentifier ?? '', nameIdentifierType: 'NATURAL_PERSON_NAME_TYPE_CODE_LEGL' }] },
        geographicAddresses: address(n.geographicAddress),
        ...(natId(n.nationalIdentification) ? { nationalIdentification: natId(n.nationalIdentification) } : {}),
        customerIdentification: n.customerIdentification ?? '',
        ...(n.dateAndPlaceOfBirth ? { dateAndPlaceOfBirth: n.dateAndPlaceOfBirth } : {}),
      },
    };
  }
  const l = p.legalPerson!;
  return {
    legalPerson: {
      name: { nameIdentifiers: [{ legalPersonName: l.name, legalPersonNameIdentifierType: 'LEGAL_PERSON_NAME_TYPE_CODE_LEGL' }] },
      geographicAddresses: address(l.geographicAddress),
      customerNumber: l.customerIdentification ?? '',
      ...(natId(l.nationalIdentification) ? { nationalIdentification: natId(l.nationalIdentification) } : {}),
    },
  };
}

function vasp(v: { name: string; lei?: string } | undefined): Record<string, unknown> | undefined {
  if (!v?.name) return undefined;
  return toProtoPerson({
    legalPerson: {
      name: v.name,
      ...(v.lei ? { nationalIdentification: { nationalIdentifier: v.lei, nationalIdentifierType: 'LEIX' } } : {}),
    },
  });
}

export function toIdentityPayload(ivms: Ivms101Payload): Record<string, unknown> {
  const originatingVasp = vasp(ivms.originatingVASP);
  const beneficiaryVasp = vasp(ivms.beneficiaryVASP);
  return {
    originator: { originatorPersons: ivms.originator.originatorPersons.map(toProtoPerson), accountNumbers: ivms.originator.accountNumber },
    beneficiary: { beneficiaryPersons: ivms.beneficiary.beneficiaryPersons.map(toProtoPerson), accountNumbers: ivms.beneficiary.accountNumber },
    ...(originatingVasp ? { originatingVasp: { originatingVasp } } : {}),
    ...(beneficiaryVasp ? { beneficiaryVasp: { beneficiaryVasp } } : {}),
  };
}

export interface TransactionFacts {
  txid: string | null;
  originator: string;
  beneficiary: string;
  amount: string; // base units
  decimals: number;
  chainId: number;
  asset: string;
}

// Whole units as the generic payload's double. A transaction amount is carried for
// matching the message to the on-chain transfer, not for settlement; the exact base-unit
// amount travels in extraJson so nothing depends on a double's precision.
export function toTransaction(t: TransactionFacts, at = new Date()): Record<string, unknown> {
  const whole = BigInt(t.amount) / 10n ** BigInt(t.decimals);
  const frac = BigInt(t.amount) % 10n ** BigInt(t.decimals);
  return {
    txid: t.txid ?? '',
    originator: t.originator,
    beneficiary: t.beneficiary,
    amount: Number(whole) + Number(frac) / 10 ** t.decimals,
    network: networkName(t.chainId),
    timestamp: at.toISOString(),
    assetType: t.asset,
    extraJson: JSON.stringify({ amountBaseUnits: t.amount, decimals: t.decimals, chainId: t.chainId }),
  };
}

// (protobufjs's built-in Any keeps the wire name type_url whatever the casing option;
// camel-casing it silently drops the type, which the reference tool in the interop test
// catches.)
// The Payload a SecureEnvelope encrypts: identity and transaction packed as Any, plus
// the timestamps that are part of what is signed.
export function encodePayload(p: Protocol, identity: Record<string, unknown>, tx: Record<string, unknown>, sentAt = new Date(), receivedAt?: Date): Buffer {
  const idBytes = p.IdentityPayload.encode(strictMessage(p.IdentityPayload, identity)).finish();
  const txBytes = p.Transaction.encode(strictMessage(p.Transaction, tx)).finish();
  const msg = p.Payload.fromObject({
    identity: { type_url: IDENTITY_TYPE_URL, value: Buffer.from(idBytes) },
    transaction: { type_url: TRANSACTION_TYPE_URL, value: Buffer.from(txBytes) },
    sentAt: sentAt.toISOString(),
    ...(receivedAt ? { receivedAt: receivedAt.toISOString() } : {}),
  });
  return Buffer.from(p.Payload.encode(msg).finish());
}

export interface DecodedPayload {
  identity: Record<string, any>;
  transaction: Record<string, any>;
  sentAt: string;
  receivedAt: string;
}

// Refuses anything that is not an IVMS101 identity and a generic transaction: the
// Any type URLs are the sender's claim, not a fact.
export function decodePayload(p: Protocol, bytes: Buffer): DecodedPayload {
  const msg: any = p.Payload.toObject(p.Payload.decode(bytes), { enums: String, longs: String, bytes: String, defaults: false });
  if (msg.identity?.type_url !== IDENTITY_TYPE_URL) throw new Error(`the identity payload is of type ${JSON.stringify(msg.identity?.type_url)}, expected ivms101.IdentityPayload`);
  if (msg.transaction?.type_url !== TRANSACTION_TYPE_URL) throw new Error(`the transaction payload is of type ${JSON.stringify(msg.transaction?.type_url)}, expected the generic transaction`);
  const idBuf = Buffer.from(msg.identity.value, 'base64');
  const txBuf = Buffer.from(msg.transaction.value, 'base64');
  return {
    identity: p.IdentityPayload.toObject(p.IdentityPayload.decode(idBuf), { enums: String, longs: String, defaults: false }),
    transaction: p.Transaction.toObject(p.Transaction.decode(txBuf), { enums: String, longs: String, defaults: false }),
    sentAt: msg.sentAt ?? '',
    receivedAt: msg.receivedAt ?? '',
  };
}
