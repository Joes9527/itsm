// These integration cases render complete Ant Design forms under coverage instrumentation.
jest.setTimeout(30000);
import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import Page from '../page';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { useAuthStore } from '@/lib/store/auth-store';
import { UserApi } from '@/lib/api/user-api';
jest.mock('@/lib/api/user-api', () => ({ UserApi: { getUsers: jest.fn() } }));
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
  await waitFor(() => expect(screen.getByRole('button', { name: '提交申请' })).toBeEnabled());
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

it('requires explicit discard of a removed/renamed answer on reload, preserving compatible draft and the old unknown attempt', async () => {
  jest.mocked(ServiceCatalogApi.createServiceRequest).mockRejectedValueOnce(new Error('response lost'));
  render(<Page />); await fill(); fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await screen.findByRole('button', { name: '重试原申请' });
  const original = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0];
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, catalogVersion: 'v2', formSchemaVersion: 'f2', fields: [{ name: 'work_location', label: '工作地点', type: 'text', required: true }] } as never);
  fireEvent.click(screen.getByRole('button', { name: '重新读取目录' }));
  expect(await screen.findByText('目录变更需要重新核对已填内容')).toBeInTheDocument();
  expect(screen.getByText(/办公地点.*office_location.*上海/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '提交申请' })).toBeDisabled();
  expect(screen.getByLabelText('申请标题')).toHaveValue('服务申请');
  expect(screen.getByRole('button', { name: '重试原申请' })).toBeEnabled();
  fireEvent.click(screen.getByRole('button', { name: '确认舍弃上述不兼容答案并重新填写' }));
  fireEvent.change(screen.getByLabelText('工作地点'), { target: { value: '北京' } });
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(2));
  const revised = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[1];
  expect(revised[0]).toMatchObject({ title: '服务申请', catalogVersion: 'v2', formData: { customFieldValues: [{ name: 'work_location', value: '北京' }] } });
  expect(revised[1].idempotencyKey).not.toBe(original[1].idempotencyKey);
  fireEvent.click(screen.getByRole('button', { name: '重试原申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(3));
  expect(jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[2][0]).toEqual(original[0]);
  expect(jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[2][1].idempotencyKey).toBe(original[1].idempotencyKey);
});

it('requires acknowledgment of service-request answers before confirming a reloaded Incident target', async () => {
  jest.mocked(ServiceCatalogApi.createServiceRequest).mockRejectedValueOnce(new ApiError('version conflict', 409, 4001, 'catalog_version_conflict', false));
  render(<Page />); await fill();
  fireEvent.change(screen.getByLabelText('联系人'), { target: { value: '代申请联系人' } });
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await screen.findByRole('button', { name: '重新读取目录' });
  const original = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0];
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, targetClass: 'incident', catalogVersion: 'v2', formSchemaVersion: 'f2' } as never);
  fireEvent.click(screen.getByRole('button', { name: '重新读取目录' }));
  await screen.findByText('目录变更需要重新核对已填内容');
  expect(screen.getByText(/联系人.*代申请联系人/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '提交申请' })).toBeDisabled();
  expect(screen.getByLabelText('办公地点')).toHaveValue('上海');
  fireEvent.click(screen.getByRole('button', { name: '确认舍弃上述不兼容答案并重新填写' }));
  for (const [label, option] of [['事件类型', 'incident'], ['事件来源', '手工']]) {
    const input = screen.getByLabelText(label); fireEvent.mouseDown(input);
    const dropdown = document.getElementById(input.getAttribute('aria-controls')!)!.closest('.ant-select-dropdown') as HTMLElement;
    await waitFor(() => expect(dropdown).toBeVisible());
    fireEvent.click(within(dropdown).getByText(option, { selector: '.ant-select-item-option-content' }));
  }
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(2));
  const revised = jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[1];
  expect(revised[0]).toMatchObject({ recordClass: 'incident', title: '服务申请', formData: { customFieldValues: [{ name: 'office_location', value: '上海' }] }, incident: { source: 'manual', type: 'incident' } });
  expect(revised[0]).not.toHaveProperty('contactName'); expect(revised[0]).not.toHaveProperty('quantity');
  expect(original[0]).toMatchObject({ recordClass: 'service_request_item', contactName: '代申请联系人', catalogVersion: 'v1' });
  expect(revised[1].idempotencyKey).not.toBe(original[1].idempotencyKey);
});

it('preserves an explicitly selected authorized requester across a compatible Catalog reload', async () => {
  useAuthStore.setState({ user: { id: 1, tenantId: 2, name: '申请人', email: 'user@example.com', permissions: ['user:read'] } as never });
  jest.mocked(UserApi.getUsers).mockResolvedValue({ users: [{ id: 9, tenantId: 2, name: '代申请人', active: true }] } as never);
  jest.mocked(ServiceCatalogApi.createServiceRequest).mockRejectedValueOnce(new ApiError('version conflict', 409));
  render(<Page />); await fill();
  await waitFor(() => expect(UserApi.getUsers).toHaveBeenCalled());
  const input = screen.getByLabelText('申请人'); fireEvent.mouseDown(input);
  const dropdown = document.getElementById(input.getAttribute('aria-controls')!)!.closest('.ant-select-dropdown') as HTMLElement;
  await waitFor(() => expect(dropdown).toBeVisible());
  fireEvent.click(await within(dropdown).findByText('代申请人', { selector: '.ant-select-item-option-content' }));
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await screen.findByRole('button', { name: '重新读取目录' });
  expect(jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[0][0].requesterId).toBe(9);
  jest.mocked(ServiceCatalogApi.getService).mockResolvedValue({ ...catalog, catalogVersion: 'v2' } as never);
  fireEvent.click(screen.getByRole('button', { name: '重新读取目录' }));
  await waitFor(() => expect(ServiceCatalogApi.getService).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByRole('button', { name: '提交申请' })).toBeEnabled());
  await screen.findByLabelText('申请标题');
  fireEvent.click(screen.getByRole('button', { name: '提交申请' }));
  await waitFor(() => expect(ServiceCatalogApi.createServiceRequest).toHaveBeenCalledTimes(2));
  expect(jest.mocked(ServiceCatalogApi.createServiceRequest).mock.calls[1][0]).toMatchObject({ requesterId: 9, catalogVersion: 'v2' });
});
