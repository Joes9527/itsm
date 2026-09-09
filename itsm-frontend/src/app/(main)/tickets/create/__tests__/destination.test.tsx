import { TicketCategoryApi } from '@/lib/api/ticket-category-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { useAuthStore } from '@/lib/store/auth-store';
import { creationReceipt } from '@/lib/api/creation.test-utils';
import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import Page from '../page';
const push = jest.fn();
let key = 0;
Object.defineProperty(crypto, 'randomUUID', {
  configurable: true,
  value: () => `generic-${++key}`,
});
beforeEach(() => {
  jest.clearAllMocks();
  useAuthStore.setState({
    isAuthenticated: true,
    user: { id: 1, actorTenantId: 2, tenantId: 2 } as never,
    currentTenant: { id: 2 } as never,
  });
});
jest.mock('next/navigation', () => ({
  useRouter: () => ({ push }),
  useSearchParams: () => new URLSearchParams('category=change&item=DDL%20firewall%20domain'),
}));
jest.mock('@/lib/api/ticket-category-api', () => ({
  TicketCategoryApi: {
    getCategoryTree: jest.fn().mockResolvedValue([]),
    list: jest.fn().mockResolvedValue({ categories: [] }),
  },
}));
jest.mock('@/lib/api/ticket-api', () => ({
  TicketApi: {
    getTemplates: jest.fn().mockResolvedValue({ templates: [] }),
    createTicket: jest.fn(),
  },
}));
jest.mock('@/lib/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));
it('requires explicit destination before input despite DDL/firewall/domain URL hints', () => {
  render(<Page />);
  expect(screen.queryByPlaceholderText('请输入工单标题')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '普通工单' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '变更请求' }));
  expect(push).toHaveBeenCalledWith('/changes/new');
});
it('catalog destination opens real Catalog selection before confirmation', () => {
  render(<Page />);
  fireEvent.click(screen.getByRole('button', { name: '服务目录申请' }));
  expect(push).toHaveBeenCalledWith('/service-catalog');
});

it('explicit Generic keeps database template and exact fields even for DDL/firewall/domain labels', async () => {
  jest
    .mocked(TicketApi.getTemplates)
    .mockResolvedValue({
      templates: [
        {
          id: 7,
          name: 'DDL firewall domain',
          description: '',
          category: '',
          priority: 'medium',
          fields: [{ name: 'office_location', label: '办公地点', type: 'text' }],
          isActive: true,
        },
      ],
    } as never);
  jest.mocked(TicketApi.createTicket).mockResolvedValue(creationReceipt);
  render(<Page />);
  fireEvent.click(screen.getByRole('button', { name: '普通工单' }));
  fireEvent.click(await screen.findByText('DDL firewall domain'));
  fireEvent.change(screen.getByLabelText(/办公地点/), { target: { value: '上海' } });
  fireEvent.change(screen.getByLabelText(/^标题/), { target: { value: 'DDL execution fields' } });
  fireEvent.click(screen.getByRole('button', { name: '创建工单' }));
  await waitFor(() => expect(TicketApi.createTicket).toHaveBeenCalledTimes(1));
  expect(TicketApi.createTicket).toHaveBeenCalledWith(
    expect.objectContaining({
      type: 'ticket',
      templateId: 7,
      formFields: expect.objectContaining({ values: [{ name: 'office_location', value: '上海' }] }),
    }),
    expect.objectContaining({ idempotencyKey: expect.any(String) })
  );
  expect(push).toHaveBeenCalledWith('/tickets/41');
});

it('submits the selected tenant category ID instead of its code or name', async () => {
  jest.mocked(TicketApi.getTemplates).mockResolvedValue({ templates: [] } as never);
  jest.mocked(TicketCategoryApi.getCategoryTree).mockResolvedValue([
    { id: 81, name: '租户专属分类', code: 'CUSTOM-81', isActive: true, parentId: null, children: [] },
    { id: 90, name: '停用父分类', isActive: false, children: [
      { id: 91, name: '不可选子分类', isActive: true, parentId: 90 },
    ] },
  ] as never);
  jest.mocked(TicketApi.createTicket).mockResolvedValue(creationReceipt);
  render(<Page />);
  fireEvent.click(screen.getByRole('button', { name: '普通工单' }));
  fireEvent.click(await screen.findByText('租户专属分类'));
  expect(screen.queryByText('不可选子分类')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText(/^标题/), { target: { value: '分类契约测试工单' } });
  fireEvent.change(screen.getByLabelText(/^详细描述/), { target: { value: '通过真实分类树创建工单' } });
  fireEvent.click(screen.getByRole('button', { name: '创建工单' }));
  await waitFor(() => expect(TicketApi.createTicket).toHaveBeenCalledTimes(1));
  const payload = jest.mocked(TicketApi.createTicket).mock.calls[0][0];
  expect(payload.cti).toEqual({ categoryId: 81 });
  expect(payload).not.toHaveProperty('category');
});