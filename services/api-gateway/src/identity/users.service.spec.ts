import { Pool } from 'pg';
import * as bcrypt from 'bcrypt';
import { ConflictException } from '@nestjs/common';
import { UsersService } from './users.service';

describe('UsersService', () => {
  function svcWith(query: jest.Mock) {
    return new UsersService({ query } as unknown as Pool);
  }

  describe('create', () => {
    it('hashes the password before storing it, never the plaintext', async () => {
      const query = jest
        .fn()
        .mockResolvedValueOnce({ rows: [] }) // findByEmail: no existing user
        .mockResolvedValueOnce({ rows: [{ id: 'u1', email: 'a@b.com' }] }); // insert

      await svcWith(query).create({
        email: 'A@B.com',
        password: 'correct horse battery staple',
        fullName: 'Alice',
      });

      const [sql, params] = query.mock.calls[1];
      expect(sql).toContain('INSERT INTO users');
      expect(params[0]).toBe('a@b.com'); // lowercased
      expect(params[1]).not.toBe('correct horse battery staple');
      expect(await bcrypt.compare('correct horse battery staple', params[1])).toBe(true);
    });

    it('refuses to create a second account with the same email', async () => {
      const query = jest.fn().mockResolvedValueOnce({ rows: [{ id: 'existing' }] });
      await expect(
        svcWith(query).create({ email: 'a@b.com', password: 'x', fullName: 'Alice' }),
      ).rejects.toBeInstanceOf(ConflictException);
    });
  });

  describe('verifyPassword', () => {
    it('accepts the correct password and rejects a wrong one', async () => {
      const hash = await bcrypt.hash('right-password', 12);
      const user = { password_hash: hash } as any;
      const svc = svcWith(jest.fn());
      await expect(svc.verifyPassword(user, 'right-password')).resolves.toBe(true);
      await expect(svc.verifyPassword(user, 'wrong-password')).resolves.toBe(false);
    });
  });

  describe('isLocked', () => {
    const svc = svcWith(jest.fn());

    it('is false with no locked_until', () => {
      expect(svc.isLocked({ locked_until: null } as any)).toBe(false);
    });

    it('is true while locked_until is in the future', () => {
      const future = new Date(Date.now() + 60_000).toISOString();
      expect(svc.isLocked({ locked_until: future } as any)).toBe(true);
    });

    it('is false once locked_until has passed', () => {
      const past = new Date(Date.now() - 60_000).toISOString();
      expect(svc.isLocked({ locked_until: past } as any)).toBe(false);
    });
  });

  describe('recordFailedLogin', () => {
    it('locks the account once the failure count reaches the threshold', async () => {
      const query = jest
        .fn()
        .mockResolvedValueOnce({ rows: [{ failed_login_count: 5 }] }) // increment
        .mockResolvedValueOnce({ rows: [] }); // lock

      await svcWith(query).recordFailedLogin('u1');

      expect(query.mock.calls[1][0]).toContain('locked_until');
      expect(query.mock.calls[1][0]).toContain('make_interval');
    });

    it('does not lock the account below the threshold', async () => {
      const query = jest.fn().mockResolvedValueOnce({ rows: [{ failed_login_count: 2 }] });
      await svcWith(query).recordFailedLogin('u1');
      expect(query).toHaveBeenCalledTimes(1);
    });
  });
});
