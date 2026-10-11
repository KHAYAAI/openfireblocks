#!/usr/bin/env node
// FORGE CUSTODY MCP server, over stdio.
//
//   FORGE_API_URL=https://custody.example.com FORGE_AGENT_KEY=ofb_agent_... forge-custody-mcp
//
// The agent key is issued once by an organisation admin (POST /organisations/:id/agents or
// the console) and carries that agent's limits. Keep it in the MCP client's secret
// configuration, never in a prompt.
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { gatewayClient } from './gateway.js';
import { createServer } from './server.js';

const url = process.env.FORGE_API_URL;
const key = process.env.FORGE_AGENT_KEY;
if (!url || !key) {
  process.stderr.write('FORGE_API_URL and FORGE_AGENT_KEY must be set\n');
  process.exit(2);
}
if (!key.startsWith('ofb_agent_')) {
  // A customer API key or an admin key would be refused by the agent routes anyway; saying
  // so here stops someone from pasting a far more powerful credential into an AI client.
  process.stderr.write('FORGE_AGENT_KEY must be an agent key (ofb_agent_...), not a customer or admin key\n');
  process.exit(2);
}
if (!/^https:\/\//.test(url) && !/^http:\/\/(127\.0\.0\.1|localhost)(:|\/|$)/.test(url)) {
  process.stderr.write('FORGE_API_URL must be https (plain http is accepted only for localhost)\n');
  process.exit(2);
}

const server = createServer(gatewayClient(url, key, Number(process.env.FORGE_TIMEOUT_MS ?? 120_000)));
await server.connect(new StdioServerTransport());
