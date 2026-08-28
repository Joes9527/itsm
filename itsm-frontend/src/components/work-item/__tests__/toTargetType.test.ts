import { toTargetType } from '../toTargetType';

describe('toTargetType', () => {
  it.each([
    ['incident', 'incident'],
    ['problem', 'problem'],
    ['change_request', 'change'],
    ['generic', 'ticket'],
    ['service_request_item', 'ticket'],
    ['catalog_task', 'ticket'],
  ] as const)('maps recordClass %s to TargetType %s', (recordClass, expected) => {
    expect(toTargetType(recordClass)).toBe(expected);
  });
});
