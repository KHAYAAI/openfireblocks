import { Injectable, Logger } from '@nestjs/common';

// Telling customers that something happened.
//
// The webhooks service has been able to sign payloads, record every
// attempt, and retry with exponential backoff for a long time, and nothing
// in the platform ever emitted an event to it. A customer could not learn
// that their key had finished its DKG ceremony, that a transaction had been
// signed, or that it had been broadcast -- they could only poll, which for
// a ceremony that takes a minute or two means either polling hard or
// finding out late.
//
// Emission is deliberately best-effort and out of band. A customer's
// signature must not fail because their notification endpoint is down: the
// money moved, and telling them about it is a separate concern with its own
// retry machinery on the other side of this call.

export type WebhookEventType =
  | 'key.created'
  | 'key.activated'
  | 'key.failed'
  | 'signature.created'
  | 'transaction.broadcast';

@Injectable()
export class WebhookEmitter {
  private readonly logger = new Logger(WebhookEmitter.name);
  private readonly baseUrl = process.env.WEBHOOKS_URL ?? '';

  // Fire-and-forget by design, and the signature says so: nothing awaits
  // this, because the caller has already done the thing being announced.
  //
  // Callers that do await it (tests) get a promise that resolves either
  // way; it never rejects, so an unhandled rejection cannot take down a
  // request that already succeeded.
  async emit(
    customerId: string,
    eventType: WebhookEventType,
    data: Record<string, unknown>,
  ): Promise<void> {
    if (!this.baseUrl) {
      // Not configured is not an error. A deployment without the webhooks
      // service still signs transactions; it just cannot announce them,
      // and logging that on every signature would be noise.
      return;
    }

    try {
      const response = await fetch(`${this.baseUrl}/v1/events`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          event_type: eventType,
          customer_id: customerId,
          data,
        }),
        // Short: this runs after the customer's work is done and must not
        // extend their request. The webhooks service fans out in the
        // background, so this is waiting for an acknowledgement, not a
        // delivery.
        signal: AbortSignal.timeout(5_000),
      });
      if (!response.ok) {
        this.logger.warn(
          `the webhooks service refused a ${eventType} event for ${customerId}: HTTP ${response.status}`,
        );
      }
    } catch (err) {
      // Logged, never thrown. A notification that could not be queued is a
      // real problem and it is not the customer's -- their transaction is
      // signed either way.
      this.logger.warn(
        `could not emit ${eventType} for ${customerId}: ${(err as Error).message}`,
      );
    }
  }
}
