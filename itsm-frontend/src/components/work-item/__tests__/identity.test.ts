import { workItemIdentity } from '../identity';
test('preserves authoritative identity and observed version', () => {
  expect(workItemIdentity({ workItemId: 91, number: 'TKT-0091', version: 7 })).toEqual({
    id: 91,
    number: 'TKT-0091',
    version: 7,
  });
});
test.each([
  { workItemId: 0, number: 'TKT-0091', version: 7 },
  { workItemId: 91, number: ' ', version: 7 },
  { workItemId: 91, number: 'TKT-0091', version: 0 },
  { workItemId: NaN, number: 'TKT-0091', version: 7 },
])('rejects invalid identity without fallback %p', input =>
  expect(() => workItemIdentity(input)).toThrow()
);
