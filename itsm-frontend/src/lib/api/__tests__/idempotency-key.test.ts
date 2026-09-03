import { generateIdempotencyKey } from '../../utils/idempotencyKey';

describe('generateIdempotencyKey', () => {
  const originalCrypto = globalThis.crypto;
  const originalMathRandom = Math.random;

  afterEach(() => {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: originalCrypto,
    });

    Math.random = originalMathRandom;
  });

  it('produces a stable-format unique key', () => {
    const mockRandomUUID = jest
      .fn()
      .mockReturnValueOnce('0c58c545-3b9c-4e15-8d57-8b037eb6bd4b')
      .mockReturnValueOnce('9ea26cc7-e381-4f1d-9d1b-8f82856f6cea');

    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: {
        ...(originalCrypto ?? {}),
        randomUUID: mockRandomUUID,
      },
    });

    const firstKey = generateIdempotencyKey();
    const secondKey = generateIdempotencyKey();

    expect(firstKey).not.toEqual(secondKey);
    expect(firstKey).toMatch(/^[0-9a-f-]{36}$/);
    expect(secondKey).toMatch(/^[0-9a-f-]{36}$/);
    expect(mockRandomUUID).toHaveBeenCalledTimes(2);
  });

  it('falls back to a UUID-format key when crypto.randomUUID is unavailable', () => {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: originalCrypto ?? {},
    });
    Math.random = jest.fn(() => 0.5);

    expect(generateIdempotencyKey()).toBe('88888888-8888-4888-8888-888888888888');
  });
});