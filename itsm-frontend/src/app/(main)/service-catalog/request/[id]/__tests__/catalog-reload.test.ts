import { incompatibleCatalogAnswers } from '../catalog-reload';
import type { ServiceItem } from '@/types/service-catalog';
const definition = (overrides: Partial<ServiceItem> = {}): ServiceItem => ({
  targetClass: 'service_request_item', fields: [], ...overrides,
} as ServiceItem);

it('keeps a selected answer when still offered and requires acknowledgment when its option is removed', () => {
  const previous = definition({ fields: [{ name: 'region', label: '区域', type: 'select', required: true, options: [{ label: '上海', value: 'sh' }] }] });
  const answers = { customFields: { region: 'sh' } };
  expect(incompatibleCatalogAnswers(previous, previous, answers)).toEqual([]);
  const next = definition({ fields: [{ ...previous.fields![0], options: [{ label: '北京', value: 'bj' }] }] });
  expect(incompatibleCatalogAnswers(previous, next, answers)).toEqual([{ path: ['customFields', 'region'], label: '区域 (region)', value: 'sh' }]);
});

it('requires acknowledgment when an answered custom field changes input type but ignores unanswered removals', () => {
  const previous = definition({ fields: [{ name: 'location', label: '地点', type: 'text', required: false }] });
  const next = definition({ fields: [{ ...previous.fields![0], type: 'number' }] });
  expect(incompatibleCatalogAnswers(previous, next, { customFields: { location: '上海' } })).toMatchObject([{ path: ['customFields', 'location'], value: '上海' }]);
  expect(incompatibleCatalogAnswers(previous, definition(), { customFields: { location: '' } })).toEqual([]);
});

it('identifies answered infrastructure fields when that section is removed while keeping common contacts', () => {
  const previous = definition({ requiresInfraFields: true });
  const changes = incompatibleCatalogAnswers(previous, definition({ requiresInfraFields: false }), { costCenter: 'CC-1', needsPublicIp: false, contactName: '申请联系人' });
  expect(changes).toEqual([
    { path: ['costCenter'], label: '成本中心', value: 'CC-1' },
    { path: ['needsPublicIp'], label: '需要公网 IP', value: false },
  ]);
});

it('identifies prior professional answers on target change while keeping CI links across professional targets', () => {
  const previous = definition({ targetClass: 'incident' });
  const answers = { incident: { category: 'network', metadata: '{"source":"operator"}' }, ciIds: ['42'], title: '共享标题' };
  expect(incompatibleCatalogAnswers(previous, definition({ targetClass: 'problem' }), answers).map(answer => answer.path)).toEqual([['incident', 'category'], ['incident', 'metadata']]);
  expect(incompatibleCatalogAnswers(previous, definition(), answers).map(answer => answer.path)).toEqual([['incident', 'category'], ['incident', 'metadata'], ['ciIds']]);
});
