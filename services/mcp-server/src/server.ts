import { randomUUID } from 'node:crypto';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { z } from 'zod';
import { GatewayClient, GatewayError } from './gateway.js';

// The tools an AI agent gets. Deliberately few, and deliberately one-way: an agent can look
// at what it may spend, pay within that, and check what happened. It cannot raise a limit,
// add a recipient, approve a held payment, see a key or reach another agent's record --
// those are the organisation's, through the console, by people.

export const INSTRUCTIONS = `You can make stablecoin payments from a FORGE CUSTODY account, within limits a person at the organisation set for you.
Rules you must follow:
- Call get_spending_status before paying, and pay only what the task requires.
- Never split a payment, change the recipient, or switch token to get around a limit or a refusal. A refusal is final for that request: explain it to the user and stop.
- Always give a purpose that a human reviewer would understand (an invoice number, an order, a reason).
- Reuse the same idempotency_key when retrying the same payment, so it cannot be paid twice.
- If a payment is held for approval, tell the user that a person must approve it in the FORGE CUSTODY console; do not try again.`;

const EXPLORERS: Record<number, string> = {
  1: 'https://etherscan.io/tx/',
  11155111: 'https://sepolia.etherscan.io/tx/',
  8453: 'https://basescan.org/tx/',
  84532: 'https://sepolia.basescan.org/tx/',
};

type ToolResult = { content: { type: 'text'; text: string }[]; isError?: boolean; structuredContent?: Record<string, unknown> };

function ok(summary: string, data: Record<string, unknown>): ToolResult {
  return { content: [{ type: 'text', text: `${summary}\n\n${JSON.stringify(data, null, 2)}` }], structuredContent: data };
}

function refused(summary: string, data: Record<string, unknown>): ToolResult {
  return { content: [{ type: 'text', text: `${summary}\n\n${JSON.stringify(data, null, 2)}` }], structuredContent: data, isError: true };
}

// Turns a gateway failure into something an agent can act on correctly: which failures
// are final, which mean a person has to act, and which are worth one retry.
function failure(err: unknown, context: Record<string, unknown> = {}, originatorConfigured = true): ToolResult {
  if (err instanceof GatewayError) {
    const body = (err.body && typeof err.body === 'object' ? err.body : {}) as Record<string, unknown>;
    if (body.requiresApproval) {
      return refused(
        'HELD FOR APPROVAL: this payment needs a person to approve it in the FORGE CUSTODY console. Do not retry; tell the user.',
        { outcome: 'approval_required', reasons: body.reasons ?? [], ...context },
      );
    }
    if (err.status === 401) {
      return refused('NOT AUTHORISED: the agent key is invalid, revoked or expired. A person must issue a new one.', { outcome: 'unauthorised', ...context });
    }
    if (err.status === 403) {
      return refused(
        `REFUSED: ${err.message}. This is final for this request. Do not split the amount, change the recipient or switch token to get around it; explain the refusal to the user.`,
        { outcome: 'refused', reason: err.message, ...context },
      );
    }
    if (err.status === 422 && /Travel Rule/i.test(err.message)) {
      const missing = Array.isArray(body.missing) ? (body.missing as string[]) : [];
      return refused(
        'TRAVEL RULE REQUIRED: this payment is above the Travel Rule threshold, so the recipient must be identified before it can be signed. Nothing was paid. ' +
          'Retry with the same idempotency_key and a `beneficiary` taken from the invoice or instruction you were given. Never invent beneficiary details: if you do not have them, ask the user.' +
          (originatorConfigured ? '' : ' (The organisation has not configured its own originator details for this agent; a person must add FORGE_TRAVEL_RULE_ORIGINATOR.)'),
        { outcome: 'travel_rule_required', reason: body.reason ?? err.message, missing, ...context },
      );
    }
    if (err.status === 400 || err.status === 404 || err.status === 409 || err.status === 422) {
      return refused(`REJECTED: ${err.message}`, { outcome: 'rejected', status: err.status, reason: err.message, ...context });
    }
    return refused(
      `UNAVAILABLE: ${err.message}. Nothing was paid unless get_payment says otherwise; check it with the same idempotency_key before trying again.`,
      { outcome: 'unavailable', status: err.status, reason: err.message, ...context },
    );
  }
  const message = (err as Error)?.message ?? String(err);
  return refused(
    `UNAVAILABLE: ${message}. Check get_payment with the same idempotency_key before trying again.`,
    { outcome: 'unavailable', reason: message, ...context },
  );
}

const ADDRESS = /^0x[0-9a-fA-F]{40}$/;
const AMOUNT = /^[0-9]+(\.[0-9]+)?$/;

export interface ServerOptions {
  // IVMS101 originator details for the organisation itself (legalPerson or naturalPerson),
  // configured by the organisation in the MCP client's settings. Needed only for payments
  // above the Travel Rule threshold. The agent supplies the beneficiary; it never supplies
  // the originator.
  travelRuleOriginator?: Record<string, unknown>;
}

export function createServer(gateway: GatewayClient, opts: ServerOptions = {}): McpServer {
  const server = new McpServer({ name: 'forge-custody', version: '0.1.0' }, { instructions: INSTRUCTIONS });

  server.registerTool(
    'get_spending_status',
    {
      title: 'What this agent may spend',
      description:
        'Returns the limits a person set for this agent: allowed tokens, allowed recipients (if restricted), the per-payment limit, the 24-hour limit, what remains of it now, and when the agent expires. Amounts are in South African rand (ZAR). Call this before paying.',
      inputSchema: {},
      annotations: { readOnlyHint: true, openWorldHint: false },
    },
    async () => {
      try {
        const me = await gateway.get('/agent/me');
        return ok(
          `You may pay up to R${me.perTransferLimitZar} per payment; R${me.remaining24hZar} of your R${me.dailyLimitZar} 24-hour limit remains. Allowed tokens: ${(me.allowedTokens ?? []).join(', ')}.` +
            (me.allowedRecipients?.length ? ` Only these recipients: ${me.allowedRecipients.join(', ')}.` : ''),
          me,
        );
      } catch (err) {
        return failure(err);
      }
    },
  );

  server.registerTool(
    'list_payment_tokens',
    {
      title: 'Tokens this agent can pay in',
      description:
        'Lists the stablecoins this agent may pay in, with the chain id and contract each one lives on and its peg currency. Use the chain_id from here when calling send_payment.',
      inputSchema: {},
      annotations: { readOnlyHint: true, openWorldHint: false },
    },
    async () => {
      try {
        const out = await gateway.get('/agent/tokens');
        const tokens = (out.tokens ?? []) as Array<Record<string, unknown>>;
        return ok(
          tokens.length
            ? `You can pay in: ${tokens.map((t) => `${t.symbol} on chain ${t.chainId}`).join('; ')}.`
            : 'No verified token is available to this agent.',
          out,
        );
      } catch (err) {
        return failure(err);
      }
    },
  );

  server.registerTool(
    'send_payment',
    {
      title: 'Pay a recipient in a stablecoin',
      description:
        'Pays `amount` of `token` to `recipient` on `chain_id`, from the organisation\'s threshold-signed custody key. The platform checks this agent\'s limits and recipient list, the organisation\'s policy and the Travel Rule, then two of three independent signing parties sign and the platform broadcasts. The agent never holds a key. A refusal is final: never split a payment or change the recipient or token to get around one. Reuse idempotency_key when retrying the same payment.',
      inputSchema: {
        token: z.string().min(1).max(32).describe('Token symbol, e.g. USDC'),
        recipient: z.string().regex(ADDRESS).describe('Recipient address, 0x followed by 40 hex characters'),
        amount: z.string().regex(AMOUNT).describe('Amount in whole token units as a decimal string, e.g. "125.50"'),
        chain_id: z.number().int().positive().describe('Chain id from list_payment_tokens'),
        purpose: z.string().min(3).max(500).describe('Why this payment is being made, for the human record (invoice number, order, reason)'),
        idempotency_key: z
          .string()
          .min(1)
          .max(120)
          .optional()
          .describe('Optional. The same key on a retry guarantees the payment is made at most once. Generated if omitted, and returned.'),
        beneficiary: z
          .object({
            name: z.string().min(1).max(200).describe('Company name, or the person\'s surname'),
            type: z.enum(['company', 'person']).optional().describe('Default company'),
            given_names: z.string().max(200).optional().describe('For a person'),
            provider: z.string().max(200).optional().describe('The provider (exchange, custodian) holding the recipient\'s wallet; omit if the recipient holds it themselves'),
          })
          .optional()
          .describe('Who is being paid, for the Travel Rule. Required above the threshold (R5,000). Take it from the invoice or instruction; never invent it.'),
      },
      annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
    },
    async ({ token, recipient, amount, chain_id, purpose, idempotency_key, beneficiary }) => {
      const key = idempotency_key ?? `mcp-${randomUUID()}`;
      const context = { idempotency_key: key, token, recipient, amount, chain_id };
      let travelRule: Record<string, unknown> | undefined;
      if (beneficiary) {
        if (!opts.travelRuleOriginator) {
          return refused(
            'NOT CONFIGURED: the organisation has not set its own originator details (FORGE_TRAVEL_RULE_ORIGINATOR), so a Travel Rule payment cannot be made by this agent. Nothing was paid; a person must configure it.',
            { outcome: 'not_configured', ...context },
          );
        }
        travelRule = {
          originator: opts.travelRuleOriginator,
          beneficiary: beneficiary.type === 'person'
            ? { naturalPerson: { name: { primaryIdentifier: beneficiary.name, ...(beneficiary.given_names ? { secondaryIdentifier: beneficiary.given_names } : {}) } } }
            : { legalPerson: { name: beneficiary.name } },
          ...(beneficiary.provider ? { beneficiaryVasp: { name: beneficiary.provider } } : { beneficiaryUnhosted: true }),
        };
      }
      try {
        const out = await gateway.post('/agent/transfers', {
          token,
          recipient,
          amount,
          chainId: chain_id,
          purpose,
          idempotencyKey: key,
          ...(travelRule ? { travelRule } : {}),
        });
        const hash = out.transaction_hash as string | undefined;
        const explorer = hash && EXPLORERS[chain_id] ? `${EXPLORERS[chain_id]}${hash}` : null;
        const data = {
          outcome: out.replayed ? 'already_paid' : out.broadcast ? 'broadcast' : 'signed_not_broadcast',
          idempotency_key: key,
          transaction_hash: hash ?? null,
          explorer,
          token: out.token?.symbol ?? token,
          amount: out.amount ?? amount,
          recipient: out.recipient ?? recipient,
          chain_id,
          signed_by_parties: out.parties ?? null,
          value_zar: out.agent?.valueZar ?? null,
          travel_rule: out.travel_rule ? { record_id: out.travel_rule.record_id, status: out.travel_rule.status } : null,
          remaining_24h_zar: out.agent?.remaining24hZar ?? null,
        };
        if (out.replayed) {
          return ok(
            `ALREADY PAID: a payment with idempotency_key "${key}" was made earlier (transaction ${hash}). Nothing was paid again.`,
            data,
          );
        }
        return ok(
          `PAID: ${data.amount} ${data.token} to ${data.recipient} on chain ${chain_id}, signed by parties ${JSON.stringify(data.signed_by_parties)}. Transaction ${hash}. R${data.remaining_24h_zar} of today's limit remains. Use get_payment with idempotency_key "${key}" to confirm it on-chain.`,
          data,
        );
      } catch (err) {
        return failure(err, context, !!opts.travelRuleOriginator);
      }
    },
  );

  server.registerTool(
    'get_payment',
    {
      title: 'Check one payment',
      description:
        'Looks up one of this agent\'s payments by its idempotency_key: whether it was paid, refused or released, why, and (given chain_id) whether the chain has confirmed it.',
      inputSchema: {
        idempotency_key: z.string().min(1).max(120),
        chain_id: z.number().int().positive().optional().describe('Give it to check confirmation on-chain'),
      },
      annotations: { readOnlyHint: true, openWorldHint: true },
    },
    async ({ idempotency_key, chain_id }) => {
      try {
        const q = chain_id ? `?chainId=${chain_id}` : '';
        const out = await gateway.get(`/agent/transfers/${encodeURIComponent(idempotency_key)}${q}`);
        const onChain = out.onChain ? ` On-chain: ${out.onChain.state}${out.onChain.blockNumber ? ` in block ${out.onChain.blockNumber}` : ''}.` : '';
        return ok(`Payment ${idempotency_key}: ${out.status}${out.reason ? ` (${out.reason})` : ''}.${onChain}`, out);
      } catch (err) {
        return failure(err, { idempotency_key });
      }
    },
  );

  server.registerTool(
    'list_payments',
    {
      title: 'Recent payments by this agent',
      description: 'Lists this agent\'s recent payments and refusals, newest first. It cannot see any other agent\'s record.',
      inputSchema: { limit: z.number().int().min(1).max(100).optional() },
      annotations: { readOnlyHint: true, openWorldHint: false },
    },
    async ({ limit }) => {
      try {
        const out = await gateway.get(`/agent/transfers?limit=${limit ?? 20}`);
        const n = (out.transfers ?? []).length;
        return ok(`${n} recent payment record${n === 1 ? '' : 's'}.`, out);
      } catch (err) {
        return failure(err);
      }
    },
  );

  return server;
}
