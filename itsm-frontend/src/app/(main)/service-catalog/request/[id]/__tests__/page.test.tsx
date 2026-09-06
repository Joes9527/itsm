// These integration cases render complete Ant Design forms under coverage instrumentation.
jest.setTimeout(30000);
import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import Page from '../page';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { useAuthStore } from '@/lib/store/auth-store';
const push = jest.fn();
jest.mock('antd', () => ({ ...jest.requireActual('antd'), DatePicker: () => <input aria-label="date" /> }));
jest.mock('next/navigation', () => ({ useParams: () => ({ id: '24' }), useRouter: () => ({ push }) }));
jest.mock('@/lib/api/service-catalog-api', () => ({ ServiceCatalogApi: { getService: jest.fn(), createServiceRequest: jest.fn() } }));
const receipt = { workItemId: 71, number: 'WI-71', recordClass: 'service_request_item', professionalReference: { type: 'service_request', id: 9 }, workflowStartStatus: 'pending', replayed: false };
const catalog = { id: '24', name: '申请服务', targetClass: 'service_request_item', catalogVersion: 'v1', formSchemaVersion: 'f1', requiresInfraFields: false, fields: [{ name: 'office_location', label: '办公地点', type: 'text', required: true }] };
let key = 0;
Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: () => `catalog-${++key}` });
beforeEach(() => { jest.clearAllMocks(); useAuthStore.setState({ isAuthenticated: true, user: { id: 1, tenantId: 2, name: '申请人', email: 'user@example.com' } as never, currentTenant: { id: 2 } as never }); jest.mocked(ServiceCatalogApi.getService).mockResolvedValue(catalog as never); jest.mocked(ServiceCatalogApi.createServiceRequest).mockResolvedValue(receipt as never); });
async function fill() {
  await screen.findByLabelText('申请标题');
  fireEvent.change(screen.getByLabelText('申请标题'), { target: { value: '服务申请' } });
  fireEvent.change(screen.getByLabelText('申请理由'), { target: { value: '需要开展业务' } });
  fireEvent.change(screen.getByLabelText('办公地点'), { target: { value: '上海' } });
}
it('uses authenticated snapshot, preserves custom names and navigates shared identity with truthful receipt', async () => {
  render(<Page />); await fill(); fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/tickets/71'));
  expect(ServiceCatalogApi.getService).toHaveBeenCalledWith('24');
  expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledWith(expect.objectContaining({ catalogId: 24, recordClass: 'service_request_item', catalogVersion: 'v1', formSchemaVersion: 'f1', contactName: '申请人', formData: { customFieldValues: [{ name: 'office_location', value: '上海' }] } }), expect.objectContaining({ idempotencyKey: expect.any(String) }));
  expect(screen.getAllByText(/WI-71.*流程启动排队中/).length).toBeGreaterThan(0);
  expect(screen.queryByText(/等待审批/)).not.toBeInTheDocument();
});
it('fails visibly without versionless fallback when detail read fails', async () => {
  jest.mocked(ServiceCatalogApi.getService).mockRejectedValue(new Error('denied')); render(<Page />);
  expect(await screen.findByText(/服务信息加载失败/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '提交申请' })).toBeDisabled();
});
it('shows service-request infrastructure controls only for service-request target', async () => {
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, targetClass: 'incident', requiresInfraFields: true } as never);
  render(<Page />); await screen.findByLabelText('事件类型');
  expect(screen.queryByLabelText('联系人')).not.toBeInTheDocument(); expect(screen.queryByText('成本中心')).not.toBeInTheDocument();
});
it('labels requested validity without promising revocation', async () => {
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, requiresInfraFields: true } as never);
  render(<Page />); expect(await screen.findByText('申请有效期')).toBeInTheDocument(); expect(screen.queryByText(/自动回收/)).not.toBeInTheDocument();
});

import { ApiError } from '@/lib/api/http-client';
it('requires explicit Catalog reload and reconfirmation after version conflict while retaining entered fields', async () => {
  jest.mocked(ServiceCatalogApi.createServiceRequest).mockRejectedValueOnce(new ApiError('目录版本已变化', 409, 4001, 'catalog_version_conflict', false));
  render(<Page />); await fill(); fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await screen.findByText('请重新读取目录并核对后再确认申请');
  const first = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0];
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, catalogVersion: 'v2', formSchemaVersion: 'f2' } as never);
  fireEvent.click(screen.getByRole('button', { name: '重新读取目录' }));
  await waitFor(() => expect(ServiceCatalogApi.getService).toHaveBeenCalledTimes(2));
  await screen.findByLabelText('申请标题');
  expect(screen.getByLabelText('办公地点')).toHaveValue('上海');
  expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(2));
  const second = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[1];
  expect(first[0]).toMatchObject({ catalogVersion: 'v1', formSchemaVersion: 'f1' });
  expect(second[0]).toMatchObject({ catalogVersion: 'v2', formSchemaVersion: 'f2' });
  expect(first[1].idempotencyKey).not.toBe(second[1].idempotencyKey);
});
it('lost Catalog response retries the frozen snapshot despite edited draft and does not reload versions', async () => {
  jest.mocked(ServiceCatalogApi.createServiceRequest).mockRejectedValueOnce(new Error('response lost'));
  render(<Page />); await fill(); fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await screen.findByRole('button', { name: '重试原申请' });
  fireEvent.change(screen.getByLabelText('办公地点'), { target: { value: '北京' } });
  fireEvent.click(screen.getByRole('button', { name: '重试原申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(2));
  const [first, second] = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls;
  expect(first[0]).toEqual(second[0]); expect(first[1].idempotencyKey).toBe(second[1].idempotencyKey);
  expect(ServiceCatalogApi.getService).toHaveBeenCalledTimes(1);
});
it('Catalog Incident uses typed fields with exact metadata and never sends service-request contacts', async () => {
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, targetClass: 'incident' } as never);
  render(<Page />); await fill();
  for (const [label, option] of [['事件类型', 'incident'], ['事件来源', '手工']]) {
    const input = screen.getByLabelText(label);
    fireEvent.mouseDown(input);
    const dropdown = document.getElementById(input.getAttribute('aria-controls')!)!.closest('.ant-select-dropdown') as HTMLElement;
    await waitFor(() => expect(dropdown).toBeVisible());
    fireEvent.click(within(dropdown).getByText(option, { selector: '.ant-select-item-option-content' }));
  }
  fireEvent.change(screen.getByLabelText('事件补充数据（JSON）'), { target: { value: '{"monitor_label":"CPU"}' } });
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(1));
  const payload = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0][0];
  expect(payload).toMatchObject({ recordClass: 'incident', incident: { type: 'incident', source: 'manual', metadata: { monitor_label: 'CPU' } } });
  expect(payload).not.toHaveProperty('contactName'); expect(payload).not.toHaveProperty('quantity'); expect(payload).not.toHaveProperty('dataClassification');
});
it.each(['generic', 'problem'] as const)('Catalog %s submits its own typed section through the confirmed Catalog endpoint', async targetClass => {
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, targetClass } as never);
  render(<Page />); await fill();
  fireEvent.change(screen.getByLabelText(targetClass === 'generic' ? '工单分类' : '问题分类'), { target: { value: 'database' } });
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(1));
  expect(jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0][0]).toMatchObject({ recordClass: targetClass, [targetClass]: { category: 'database' } });
});
it('Catalog Change requires professional inputs and preserves them in the confirmed Catalog payload', async () => {
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, targetClass: 'change_request' } as never);
  render(<Page />); await fill();
  for (const [label, option] of [['变更类型', 'normal'], ['影响范围', 'medium'], ['风险等级', 'low']]) {
    const input = screen.getByLabelText(label);
    fireEvent.mouseDown(input);
    const dropdown = document.getElementById(input.getAttribute('aria-controls')!)!.closest('.ant-select-dropdown') as HTMLElement;
    await waitFor(() => expect(dropdown).toBeVisible());
    fireEvent.click(within(dropdown).getByText(option, { selector: '.ant-select-item-option-content' }));
  }
  for (const label of ['变更理由', '实施计划', '回退计划']) fireEvent.change(screen.getByLabelText(label), { target: { value: `${label}内容` } });
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(1));
  const payload = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0][0];
  expect(payload).toMatchObject({ catalogId: 24, recordClass: 'change_request', catalogVersion: 'v1', formSchemaVersion: 'f1', change: { type: 'normal', justification: '变更理由内容', implementationPlan: '实施计划内容', rollbackPlan: '回退计划内容', impactScope: 'medium', riskLevel: 'low' } });
  expect(payload).not.toHaveProperty('quantity');
});
