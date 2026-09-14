import { renderHook } from '@testing-library/react';
import { useChangeOperation } from '../useChangeOperation';

test('retry freezes version; corrected payload and explicitly new operation use observed version', () => {
  let key = 0;
  Object.defineProperty(global.crypto, 'randomUUID', {
    configurable: true,
    value: () => `operation-${++key}`,
  });
  const { result } = renderHook(useChangeOperation);
  const original = result.current.identity('1/metadata', { title: 'original' }, 3);
  expect(result.current.identity('1/metadata', { title: 'original' }, 4)).toEqual(original);
  expect(result.current.identity('1/metadata', { title: 'corrected' }, 4)).toEqual({
    operationId: 'operation-2',
    expectedVersion: 4,
  });
  result.current.clear();
  expect(result.current.identity('1/metadata', { title: 'corrected' }, 5)).toEqual({
    operationId: 'operation-3',
    expectedVersion: 5,
  });
});
