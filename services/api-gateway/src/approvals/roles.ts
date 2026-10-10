// What each role in an organisation may do. One place, so the guard, the
// service and the tests agree -- and so a reviewer can read the whole
// separation of duties in twenty lines.
//
// The split that matters: whoever can start a transfer and whoever can
// approve one are different sets of people. An admin is in both, and the
// database stops an admin approving a transfer they started themselves
// (migration 023); an approver cannot start transfers at all, and an
// operator cannot approve them.

export type TenantRole =
  | 'admin'
  | 'approver'
  | 'operator'
  | 'auditor'
  | 'viewer'
  | 'billing_admin'
  | 'user'; // legacy, treated as operator

export const ALL_ROLES: readonly TenantRole[] = [
  'admin',
  'approver',
  'operator',
  'auditor',
  'viewer',
  'billing_admin',
  'user',
];

export const CAN_READ_APPROVALS: readonly TenantRole[] = ['admin', 'approver', 'operator', 'auditor', 'viewer', 'user'];
export const CAN_DECIDE: readonly TenantRole[] = ['admin', 'approver'];
export const CAN_INITIATE: readonly TenantRole[] = ['admin', 'operator', 'user'];
export const CAN_MANAGE: readonly TenantRole[] = ['admin'];
// Who sees and changes how the organisation pays. Billing is not custody:
// a billing admin can add a card but cannot see keys or start transfers.
export const CAN_BILL: readonly TenantRole[] = ['admin', 'billing_admin'];

export function isTenantRole(value: unknown): value is TenantRole {
  return typeof value === 'string' && (ALL_ROLES as readonly string[]).includes(value);
}
