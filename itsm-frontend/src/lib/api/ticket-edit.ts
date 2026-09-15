import { sessionSecurity } from '@/lib/security';

// Editing uses the version the user observed, never a refreshed server version.
export function ticketEditVersion(version: unknown): number {
  if (typeof version !== 'number' || !Number.isSafeInteger(version) || version <= 0) {
    throw new Error('工单版本无效，请刷新工单后重新确认修改');
  }
  return version;
}

export interface TicketEditResult {
  workItemId: number;
  version: number;
  status: string;
  replayed: boolean;
}

export function ticketEditOperation(operationId: unknown): string {
  if (typeof operationId !== 'string' || !operationId.trim() || operationId.length > 200) {
    throw new Error('工单操作标识无效，请重新确认修改');
  }
  return operationId;
}

export interface TicketEditIntent<T extends object> {
  fingerprint: string;
  payload: T & { version: number; operationId: string };
}

function createTicketEditOperation(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return globalThis.crypto.randomUUID();
  }
  // LAN HTTP can expose getRandomValues without the secure-context randomUUID API.
  if (typeof globalThis.crypto?.getRandomValues !== 'function') {
    throw new Error('浏览器不支持安全操作标识，请使用支持加密随机数的浏览器');
  }
  return sessionSecurity.generateSessionId();
}

// Hold one confirmed payload across uncertain retries. A background refresh does
// not replace its observed version; changed form values define a new intent.
export function prepareTicketEdit<T extends object>(
  previous: TicketEditIntent<T> | undefined,
  fields: T,
  version: unknown
): TicketEditIntent<T> {
  const fingerprint = JSON.stringify(fields);
  if (previous?.fingerprint === fingerprint) return previous;
  return {
    fingerprint,
    payload: {
      ...JSON.parse(fingerprint),
      version: ticketEditVersion(version),
      operationId: createTicketEditOperation(),
    },
  };
}

// Only the explicit backend conflict envelope proves this command was rejected.
// Network failures and ambiguous server errors must retain the original intent.
export function isTicketEditConflict(error: unknown): boolean {
  if (!(error instanceof Error)) return false;
  const response = error as Error & { status?: unknown; code?: unknown };
  return response.status === 409 && response.code === 4090;
}
