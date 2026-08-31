import { idempotencyKeyFor, newIdempotencyKey } from '@/lib/api/idempotency-key';

describe('idempotencyKeyFor', () => {
  it('uses a caller-owned key when supplied', () => {
    expect(idempotencyKeyFor({}, 'caller-key')).toBe('caller-key');
  });

  it('reuses one generated key for the same submission object', () => {
    const submission = {};
    const first = idempotencyKeyFor(submission);
    const second = idempotencyKeyFor(submission);

    expect(first).toEqual(expect.any(String));
    expect(second).toBe(first);
  });

  it('creates a UUID-shaped key', () => {
    expect(newIdempotencyKey()).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
    );
  });
});
