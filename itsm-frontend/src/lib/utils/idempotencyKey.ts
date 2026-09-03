export function generateIdempotencyKey(): string {
  const randomUuid = globalThis.crypto?.randomUUID;

  if (typeof randomUuid === 'function') {
    return randomUuid.call(globalThis.crypto);
  }

  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (placeholder) => {
    const randomNibble = Math.floor(Math.random() * 16);
    const value = placeholder === 'x' ? randomNibble : (randomNibble & 0x3) | 0x8;

    return value.toString(16);
  });
}