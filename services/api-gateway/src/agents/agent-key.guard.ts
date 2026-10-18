import { CanActivate, ExecutionContext, Injectable, UnauthorizedException } from '@nestjs/common';
import { Agent, AgentsService } from './agents.service';

export interface AgentRequest {
  headers: Record<string, string | string[] | undefined>;
  agent: Agent;
}

// Authenticates an agent key (ofb_agent_...). Used only on the agent's own
// routes: an agent key is not a customer API key, is not in the customers
// table, and so cannot pass ApiKeyGuard anywhere else.
@Injectable()
export class AgentKeyGuard implements CanActivate {
  constructor(private readonly agents: AgentsService) {}

  async canActivate(ctx: ExecutionContext): Promise<boolean> {
    const req = ctx.switchToHttp().getRequest<AgentRequest>();
    const header = req.headers['authorization'];
    const key = typeof header === 'string' && header.startsWith('Bearer ') ? header.slice(7).trim() : '';
    if (!key) throw new UnauthorizedException('missing agent key');
    const agent = await this.agents.authenticate(key);
    if (!agent) throw new UnauthorizedException('invalid, revoked or expired agent key');
    req.agent = agent;
    return true;
  }
}
