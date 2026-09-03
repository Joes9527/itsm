export function generateIdempotencyKey(): string {
  const randomUuid = globalThis.crypto?.randomUUID;

  if (typeof randomUuid !== 'function') {
    throw new Error('crypto.randomUUID is required to generate Idempotency-Key values');
  }

  return randomUuid.call(globalThis.crypto);
}