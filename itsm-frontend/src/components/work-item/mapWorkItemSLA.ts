import type { TicketSLAInfo } from '@/lib/api/ticket-api';
import type { WorkItemSLAState } from './WorkItemTypes';

// Preserve the authoritative backend cycle projection, including completed facts.
export function mapWorkItemSLA(
  info: TicketSLAInfo | null | undefined
): WorkItemSLAState | undefined {
  if (!info) return undefined;
  return { ...info };
}
