import { ForbiddenException } from '@nestjs/common';

// Policy approved a transfer on the condition that people sign off on it.
//
// The policy service answers "approved: true, requiresApproval: true" for a
// high-value transfer, and enforcePolicy used to look only at `approved`, so
// every direct signing route signed such a transfer immediately. This is
// thrown instead, before anything is recorded or signed. Routes that can open
// an approval catch it and do; the rest let it surface as a 403 that says so.
export class ApprovalRequiredException extends ForbiddenException {
  constructor(
    readonly reasons: string[],
    requestId: string,
  ) {
    super({
      error: 'approval required',
      requiresApproval: true,
      reasons,
      requestId,
      message:
        'This transfer needs approval before it can be signed: ' +
        (reasons.length ? reasons.join('; ') : 'policy requires a manual approval') +
        '. Start it from the console or as a settlement so that approvers are asked.',
    });
  }
}
