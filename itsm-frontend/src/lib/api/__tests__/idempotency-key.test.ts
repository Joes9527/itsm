import { generateIdempotencyKey } from '../../utils/idempotencyKey';

describe('generateIdempotencyKey', () => {
  const originalCrypto = globalThis.crypto;

  afterEach(() => {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: originalCrypto,
    });
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
});