import { generateApiKey, hashApiKey, hashesEqual } from './api-key.util';

describe('api-key util', () => {
  it('generates prefixed, unique keys', () => {
    const a = generateApiKey();
    const b = generateApiKey();
    expect(a.startsWith('ofb_')).toBe(true);
    expect(a).not.toEqual(b);
  });

  it('hashes deterministically to 64 hex chars', () => {
    expect(hashApiKey('dev-demo-key')).toBe(
      '6cbae51c7775b973f845b3fb4b333495890ecc9c57a9c3b3d662a3200d3227e1',
    );
    expect(hashApiKey('x')).toMatch(/^[0-9a-f]{64}$/);
  });

  it('compares hashes in constant time', () => {
    const h = hashApiKey('k');
    expect(hashesEqual(h, h)).toBe(true);
    expect(hashesEqual(h, hashApiKey('other'))).toBe(false);
    expect(hashesEqual(h, '')).toBe(false);
  });
});
