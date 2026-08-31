const submissionKeys = new WeakMap<object, string>();

export const newIdempotencyKey = (): string => {
  if (typeof globalThis.crypto.randomUUID === 'function') {
    return globalThis.crypto.randomUUID();
  }

  // jsdom and a few older WebCrypto implementations expose getRandomValues
  // without randomUUID. Preserve cryptographic randomness and RFC 4122 v4 bits.
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
};

export const idempotencyKeyFor = (submission: object, suppliedKey?: string): string => {
  if (suppliedKey) return suppliedKey;

  const existing = submissionKeys.get(submission);
  if (existing) return existing;

  const created = newIdempotencyKey();
  submissionKeys.set(submission, created);
  return created;
};
