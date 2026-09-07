import {readFileSync} from 'node:fs';

// PostgreSQL evidence uses a space and offset; API results use RFC3339.
// Compare instants at nanosecond precision so replay cannot move original times.
function instant(value: unknown): bigint | null {
  if (typeof value !== 'string') return null;
  const parts = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!parts) return null;
  const milliseconds = Date.parse(parts[1] + 'T' + parts[2] + parts[4]);
  if (!Number.isFinite(milliseconds)) return null;
  return BigInt(milliseconds) * BigInt(1_000_000) + BigInt((parts[3] ?? '').padEnd(9, '0'));
}

export function matchesSSLVPNPostcheck(file: string, workItemId: number, result: {verifiedAt: string; expiresAt: string}): boolean {
  try {
    const evidence = JSON.parse(readFileSync(file, 'utf8'));
    if (evidence?.passed !== true || !Number.isSafeInteger(workItemId) || workItemId <= 0 || evidence.workItemId !== workItemId) return false;
    const verified = instant(result.verifiedAt);
    const expires = instant(result.expiresAt);
    return verified !== null && expires !== null &&
      instant(evidence.beforeVerifiedAt) === verified && instant(evidence.afterVerifiedAt) === verified &&
      instant(evidence.beforeExpiresAt) === expires && instant(evidence.afterExpiresAt) === expires;
  } catch {
    return false;
  }
}
