import { ForbiddenException } from '@nestjs/common';
import { FrozenException } from '../controls/controls.service';
import { KeysService } from './keys.service';

// Every path that signs asks the organisation's controls first, before it
// touches the database, policy, the chain or the signer. The stand-ins below
// blow up if anything else is reached, so a path that forgot to ask would
// fail loudly instead of passing.
const boom = new Proxy({}, { get: () => () => { throw new Error('reached something other than the controls'); } });

function service(controls: unknown) {
  return new KeysService(boom as never, boom as never, boom as never, undefined, undefined, undefined, undefined, controls as never);
}
const customer = { customer_id: 'c1', raw_digest_signing_enabled: true } as never;
const frozen = { assertCanSign: async () => { throw new FrozenException('test'); }, assertDestinationAllowed: async () => undefined };
const notListed = { assertCanSign: async () => undefined, assertDestinationAllowed: async () => { throw new ForbiddenException('not on whitelist'); } };

describe('signing paths ask the organisation\'s controls first', () => {
  const calls: Array<[string, (s: KeysService) => Promise<unknown>]> = [
    ['signWithKey', (s) => s.signWithKey(customer, 'k', { message_hash: '0x' } as never)],
    ['signTransaction', (s) => s.signTransaction(customer, 'k', { to: '0x' + '1'.repeat(40) } as never)],
    ['sendToken', (s) => s.sendToken(customer, 'k', { recipient: '0x' + '1'.repeat(40) } as never)],
    ['sendBitcoin', (s) => s.sendBitcoin(customer, 'k', { destination: 'bc1q' + 'a'.repeat(30) } as never)],
    ['sendSolana', (s) => s.sendSolana(customer, 'k', { destination: '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM' } as never)],
    ['sendCosmos', (s) => s.sendCosmos(customer, 'k', { destination: 'cosmos1' + 'q'.repeat(38) } as never)],
  ];
  it.each(calls)('%s refuses while the organisation is frozen', async (_n, run) => {
    await expect(run(service(frozen))).rejects.toBeInstanceOf(FrozenException);
  });
  it.each(calls.slice(1))('%s refuses a destination that is not on the whitelist', async (_n, run) => {
    await expect(run(service(notListed))).rejects.toThrow(/whitelist/);
  });
});
