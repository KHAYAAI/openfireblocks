import { configFromEnv, problems, requirementFor, toIvms101, TravelRuleInput } from './travel-rule';

const cfg = { thresholdZar: 5000, zarPerUsd: 18.5 };
const ZARP = { asset: 'ZARP', decimals: 18, pegCurrency: 'ZAR' };
const USDC = { asset: 'USDC', decimals: 6, pegCurrency: 'USD' };
const units = (n: string, d: number) => (BigInt(n.replace('.', '')) * 10n ** BigInt(d - (n.split('.')[1]?.length ?? 0))).toString();

describe('when the Travel Rule applies', () => {
  it('a rand stablecoin just under the threshold does not need it', () => {
    const r = requirementFor({ ...ZARP, amount: units('4999.99', 18) }, cfg);
    expect(r).toMatchObject({ required: false, valueZar: 4999.99, valuation: 'zar-peg' });
  });

  it('at the threshold exactly, it does ("at or above")', () => {
    expect(requirementFor({ ...ZARP, amount: units('5000', 18) }, cfg).required).toBe(true);
  });

  it('a dollar stablecoin is valued at the configured rate', () => {
    // 270.28 USDC * 18.5 = R5000.18
    const r = requirementFor({ ...USDC, amount: units('270.28', 6) }, cfg);
    expect(r).toMatchObject({ required: true, valueZar: 5000.18, valuation: 'usd-peg' });
    expect(requirementFor({ ...USDC, amount: units('270', 6) }, cfg).required).toBe(false);
  });

  it('without a rate, a dollar stablecoin cannot be valued and is treated as over', () => {
    const r = requirementFor({ ...USDC, amount: '1' }, { thresholdZar: 5000 });
    expect(r).toMatchObject({ required: true, valueZar: null, valuation: 'unpriced' });
  });

  it('a native asset with no price source is treated as over the threshold, however small', () => {
    const r = requirementFor({ asset: 'ETH', decimals: 18, amount: '1' }, cfg);
    expect(r).toMatchObject({ required: true, valuation: 'unpriced' });
    expect(r.reason).toMatch(/treated as over/);
  });

  it('a zero-value transaction moves nothing and does not need it', () => {
    expect(requirementFor({ asset: 'ETH', decimals: 18, amount: '0' }, cfg)).toMatchObject({ required: false, valuation: 'zero' });
  });

  it('an allowance moves nothing and does not need it', () => {
    expect(requirementFor({ ...ZARP, amount: units('1000000', 18), isAllowance: true }, cfg).required).toBe(false);
  });

  it('a very large amount keeps its cents', () => {
    const r = requirementFor({ ...ZARP, amount: units('50000000.37', 18) }, cfg);
    expect(r.valueZar).toBe(50000000.37);
  });

  it('bad configuration is refused, not defaulted', () => {
    expect(() => configFromEnv({ TRAVEL_RULE_THRESHOLD_ZAR: 'lots' })).toThrow();
    expect(() => configFromEnv({ TRAVEL_RULE_ZAR_PER_USD: '0' })).toThrow();
    expect(configFromEnv({})).toEqual({ thresholdZar: 5000, zarPerUsd: undefined });
  });
});

describe('what information is enough', () => {
  const good: TravelRuleInput = {
    originator: {
      naturalPerson: {
        name: { primaryIdentifier: 'Dlamini', secondaryIdentifier: 'Thandi' },
        nationalIdentification: { nationalIdentifier: '8001015009087', nationalIdentifierType: 'NIDN', countryOfIssue: 'ZA' },
      },
    },
    beneficiary: { legalPerson: { name: 'Acme Suppliers (Pty) Ltd' } },
    beneficiaryVasp: { name: 'Example Exchange', lei: '5493001KJTIIGC8Y1R12' },
  };

  it('a named, identified originator, a named beneficiary and their provider is enough', () => {
    expect(problems(good)).toEqual([]);
  });

  it('nothing at all names every missing part', () => {
    expect(problems(undefined)[0]).toMatch(/travelRule is required/);
  });

  it('an originator with only a name is not identified', () => {
    const p = problems({ ...good, originator: { naturalPerson: { name: { primaryIdentifier: 'Dlamini' } } } });
    expect(p.join()).toMatch(/originator: one of geographicAddress/);
  });

  it('an address counts only with a country', () => {
    const p = problems({
      ...good,
      originator: { naturalPerson: { name: { primaryIdentifier: 'D' }, geographicAddress: { addressLine: ['1 Main Rd'], country: '' } } },
    });
    expect(p.join()).toMatch(/originator: one of/);
  });

  it('a legal-person originator can be identified by its LEI', () => {
    expect(
      problems({
        ...good,
        originator: { legalPerson: { name: 'Khaya Treasury', nationalIdentification: { nationalIdentifier: '5493001KJTIIGC8Y1R12', nationalIdentifierType: 'LEIX' } } },
      }),
    ).toEqual([]);
  });

  it('the beneficiary provider is required unless the wallet is unhosted', () => {
    const { beneficiaryVasp, ...rest } = good;
    void beneficiaryVasp;
    expect(problems(rest).join()).toMatch(/beneficiaryVasp.name is required/);
    expect(problems({ ...rest, beneficiaryUnhosted: true })).toEqual([]);
    expect(problems({ ...good, beneficiaryUnhosted: true }).join()).toMatch(/cannot both/);
  });

  it('a person must be one kind, not both', () => {
    const p = problems({ ...good, beneficiary: { naturalPerson: { name: { primaryIdentifier: 'X' } }, legalPerson: { name: 'Y' } } });
    expect(p.join()).toMatch(/not both/);
  });

  it('builds the IVMS101 payload with the transaction supplying the accounts', () => {
    const out = toIvms101(
      good,
      { ...ZARP, amount: '1', chainId: 1, originatorAddress: '0xfrom', beneficiaryAddress: '0xto' },
      { name: 'Khaya Demo Bank' },
    );
    expect(out.originator.accountNumber).toEqual(['0xfrom']);
    expect(out.beneficiary.accountNumber).toEqual(['0xto']);
    expect(out.beneficiaryVASP?.lei).toBe('5493001KJTIIGC8Y1R12');
    expect(out.originatingVASP?.name).toBe('Khaya Demo Bank');
  });
});
