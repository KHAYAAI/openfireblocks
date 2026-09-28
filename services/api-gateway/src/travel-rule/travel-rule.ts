// The Travel Rule, as pure functions: does a transfer need it, and is the
// information supplied enough. No database, no network -- so every case
// below is a unit test, and the rules can be read in one sitting.
//
// FIC Directive 9 (South Africa), following FATF Recommendation 16: an
// outbound crypto-asset transfer at or above the threshold must carry the
// originator's and beneficiary's information, in the interVASP Messaging
// Standard (IVMS101) that receiving providers read.

export interface TravelRuleConfig {
  thresholdZar: number;
  // How many rand one US dollar is taken to be worth, for USD-pegged
  // stablecoins. Unset means USD-pegged transfers cannot be valued, and
  // are treated like any other asset that cannot be: as over the
  // threshold.
  zarPerUsd?: number;
}

export function configFromEnv(env: NodeJS.ProcessEnv = process.env): TravelRuleConfig {
  const threshold = Number(env.TRAVEL_RULE_THRESHOLD_ZAR ?? '5000');
  const rate = env.TRAVEL_RULE_ZAR_PER_USD ? Number(env.TRAVEL_RULE_ZAR_PER_USD) : undefined;
  if (!Number.isFinite(threshold) || threshold < 0) {
    throw new Error('TRAVEL_RULE_THRESHOLD_ZAR must be a non-negative number');
  }
  if (rate !== undefined && (!Number.isFinite(rate) || rate <= 0)) {
    throw new Error('TRAVEL_RULE_ZAR_PER_USD must be a positive number');
  }
  return { thresholdZar: threshold, zarPerUsd: rate };
}

export interface TransferFacts {
  asset: string;
  amount: string; // base units
  decimals: number;
  pegCurrency?: string | null;
  isAllowance?: boolean;
}

export interface Requirement {
  required: boolean;
  valueZar: number | null;
  valuation: 'zar-peg' | 'usd-peg' | 'unpriced' | 'allowance' | 'zero';
  reason: string;
}

// Whole units as a decimal string, exactly.
function units(amount: string, decimals: number): number {
  const v = BigInt(amount);
  const base = 10n ** BigInt(decimals);
  const whole = v / base;
  const frac = v % base;
  // Rand values are compared at cent precision; converting the remainder
  // through a string keeps a 50-million-unit amount from losing cents.
  return Number(whole) + Number(frac) / Number(base);
}

// Does this transfer need Travel Rule information?
//
// The conservative reading throughout. A transfer whose value the
// platform cannot establish is treated as over the threshold: the cost of
// asking for information that turned out to be unnecessary is a form; the
// cost of not asking when it was necessary is a regulatory breach on every
// such transfer.
export function requirementFor(t: TransferFacts, cfg: TravelRuleConfig): Requirement {
  if (t.isAllowance) {
    // approve() moves nothing now. The transfer it later permits is what
    // the rule applies to, and that is a separate transaction.
    return { required: false, valueZar: null, valuation: 'allowance', reason: 'an allowance moves no value itself' };
  }
  if (BigInt(t.amount || '0') === 0n) {
    return { required: false, valueZar: 0, valuation: 'zero', reason: 'the transfer moves no value' };
  }
  const peg = (t.pegCurrency ?? '').toUpperCase();
  if (peg === 'ZAR') {
    const zar = units(t.amount, t.decimals);
    return {
      required: zar >= cfg.thresholdZar,
      valueZar: round2(zar),
      valuation: 'zar-peg',
      reason: `valued at its rand peg: R${round2(zar)} against a threshold of R${cfg.thresholdZar}`,
    };
  }
  if (peg === 'USD' && cfg.zarPerUsd) {
    const zar = units(t.amount, t.decimals) * cfg.zarPerUsd;
    return {
      required: zar >= cfg.thresholdZar,
      valueZar: round2(zar),
      valuation: 'usd-peg',
      reason: `valued at its dollar peg at R${cfg.zarPerUsd}/USD: R${round2(zar)} against R${cfg.thresholdZar}`,
    };
  }
  return {
    required: true,
    valueZar: null,
    valuation: 'unpriced',
    reason: `${t.asset} has no price source here, so the transfer is treated as over the R${cfg.thresholdZar} threshold`,
  };
}

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}

// ------------------------------------------------------------ IVMS101
//
// The subset of IVMS101 a transfer must carry, in the shape receiving
// providers read. Names follow the standard.

export interface NaturalPersonName {
  primaryIdentifier: string; // surname
  secondaryIdentifier?: string; // given names
}

export interface Person {
  naturalPerson?: {
    name: NaturalPersonName;
    geographicAddress?: { addressLine: string[]; country: string };
    nationalIdentification?: { nationalIdentifier: string; nationalIdentifierType: string; countryOfIssue?: string };
    customerIdentification?: string;
    dateAndPlaceOfBirth?: { dateOfBirth: string; placeOfBirth: string };
  };
  legalPerson?: {
    name: string;
    geographicAddress?: { addressLine: string[]; country: string };
    nationalIdentification?: { nationalIdentifier: string; nationalIdentifierType: string }; // e.g. LEI
    customerIdentification?: string;
  };
}

export interface TravelRuleInput {
  originator: Person;
  beneficiary: Person;
  // The provider holding the beneficiary's wallet. Omitted, with
  // beneficiaryUnhosted true, when the beneficiary holds it themselves.
  beneficiaryVasp?: { name: string; lei?: string; did?: string };
  beneficiaryUnhosted?: boolean;
}

export interface Ivms101Payload {
  originator: { originatorPersons: Person[]; accountNumber: string[] };
  beneficiary: { beneficiaryPersons: Person[]; accountNumber: string[] };
  originatingVASP?: { name: string; lei?: string };
  beneficiaryVASP?: { name: string; lei?: string; did?: string };
  transfer: { asset: string; amount: string; decimals: number; chainId: number };
}

function nonEmpty(s: unknown): s is string {
  return typeof s === 'string' && s.trim().length > 0;
}

function personProblems(p: Person | undefined, role: 'originator' | 'beneficiary'): string[] {
  const out: string[] = [];
  if (!p || (!p.naturalPerson && !p.legalPerson)) {
    return [`${role}: a naturalPerson or legalPerson is required`];
  }
  if (p.naturalPerson && p.legalPerson) out.push(`${role}: give a naturalPerson or a legalPerson, not both`);
  if (p.naturalPerson && !nonEmpty(p.naturalPerson.name?.primaryIdentifier)) {
    out.push(`${role}: naturalPerson.name.primaryIdentifier (surname) is required`);
  }
  if (p.legalPerson && !nonEmpty(p.legalPerson.name)) out.push(`${role}: legalPerson.name is required`);

  // The originator must be identifiable beyond a name: FATF R.16 asks for
  // an address, a national identity number, a customer number, or date
  // and place of birth. The beneficiary needs only a name (and the
  // account, which the transaction supplies).
  if (role === 'originator') {
    const n = p.naturalPerson;
    const l = p.legalPerson;
    const hasAddress = (x?: { addressLine: string[]; country: string }) =>
      !!x && Array.isArray(x.addressLine) && x.addressLine.some(nonEmpty) && nonEmpty(x.country);
    const identified = n
      ? hasAddress(n.geographicAddress) ||
        nonEmpty(n.nationalIdentification?.nationalIdentifier) ||
        nonEmpty(n.customerIdentification) ||
        (nonEmpty(n.dateAndPlaceOfBirth?.dateOfBirth) && nonEmpty(n.dateAndPlaceOfBirth?.placeOfBirth))
      : hasAddress(l?.geographicAddress) ||
        nonEmpty(l?.nationalIdentification?.nationalIdentifier) ||
        nonEmpty(l?.customerIdentification);
    if (!identified) {
      out.push(
        'originator: one of geographicAddress, nationalIdentification, customerIdentification, ' +
          'or dateAndPlaceOfBirth is required in addition to the name',
      );
    }
  }
  return out;
}

// Everything wrong with the supplied information, or an empty list.
export function problems(input: TravelRuleInput | undefined): string[] {
  if (!input || typeof input !== 'object') {
    return ['travelRule is required for this transfer: originator, beneficiary, and the beneficiary\'s provider (or beneficiaryUnhosted: true)'];
  }
  const out = [...personProblems(input.originator, 'originator'), ...personProblems(input.beneficiary, 'beneficiary')];
  if (input.beneficiaryUnhosted) {
    if (input.beneficiaryVasp) out.push('beneficiaryVasp and beneficiaryUnhosted cannot both be given');
  } else if (!input.beneficiaryVasp || !nonEmpty(input.beneficiaryVasp.name)) {
    out.push('beneficiaryVasp.name is required, or beneficiaryUnhosted: true if the beneficiary holds the wallet themselves');
  }
  return out;
}

export function toIvms101(
  input: TravelRuleInput,
  t: TransferFacts & { chainId: number; originatorAddress: string; beneficiaryAddress: string },
  originatingVasp?: { name: string; lei?: string },
): Ivms101Payload {
  return {
    originator: { originatorPersons: [input.originator], accountNumber: [t.originatorAddress] },
    beneficiary: { beneficiaryPersons: [input.beneficiary], accountNumber: [t.beneficiaryAddress] },
    originatingVASP: originatingVasp,
    beneficiaryVASP: input.beneficiaryUnhosted ? undefined : input.beneficiaryVasp,
    transfer: { asset: t.asset, amount: t.amount, decimals: t.decimals, chainId: t.chainId },
  };
}
