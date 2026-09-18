import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';
import { WorkItemClassificationSelect } from '../WorkItemClassificationSelect';
import { TicketCategoryApi } from '@/lib/api/ticket-category-api';
import { useAuthStore } from '@/lib/store/auth-store';

jest.mock('@/lib/api/ticket-category-api', () => {
  const actual = jest.requireActual('@/lib/api/ticket-category-api');
  return {
    ...actual,
    TicketCategoryApi: { getCategoryTree: jest.fn() },
  };
});

const tree = [
  {
    id: 81,
    name: '租户应用支持',
    code: 'tenant_app',
    parentId: null,
    level: 1,
    isActive: true,
    path: '租户应用支持',
    pathIds: [81],
    sortOrder: 1,
    description: '',
    createdAt: '',
    updatedAt: '',
    children: [
      {
        id: 82,
        name: '账号与权限',
        code: 'account',
        parentId: 81,
        level: 2,
        isActive: true,
        path: '租户应用支持 / 账号与权限',
        pathIds: [81, 82],
        sortOrder: 1,
        description: '',
        createdAt: '',
        updatedAt: '',
        children: [],
      },
    ],
  },
];

// 回归：适配层把 onStateChange 以内联函数传给 CTISelector，而 CTISelector 的 effect 依赖该回调。
// 若选择器每次发出新的数组/对象，适配层的 setState 就会触发新渲染 → 新回调 → effect 重跑，
// 形成"Maximum update depth exceeded"的无限环（曾导致整仓 Jest 与 CI 超过 30 分钟上限）。
describe('WorkItemClassificationSelect', () => {
  beforeEach(() => {
    useAuthStore.setState({
      isAuthenticated: true,
      user: { id: 1, tenantId: 2 } as never,
      currentTenant: { id: 2 } as never,
    });
    jest.mocked(TicketCategoryApi.getCategoryTree).mockResolvedValue(tree as never);
  });

  afterEach(() => {
    useAuthStore.setState({ isAuthenticated: false, user: null as never, currentTenant: null as never });
  });

  it('renders without exceeding the React update depth when no selection exists', async () => {
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      render(<WorkItemClassificationSelect />);

      await waitFor(() => expect(TicketCategoryApi.getCategoryTree).toHaveBeenCalled());

      const depthErrors = consoleError.mock.calls.filter(call =>
        String(call[0]).includes('Maximum update depth exceeded')
      );
      expect(depthErrors).toHaveLength(0);
    } finally {
      consoleError.mockRestore();
    }
  });

  it("re-renders with equivalent props without extra fetches or update-depth errors", async () => {
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const { rerender } = render(<WorkItemClassificationSelect id="cti-loop" />);

      await waitFor(() => expect(TicketCategoryApi.getCategoryTree).toHaveBeenCalledTimes(1));
      expect(screen.getByRole('combobox')).toBeInTheDocument();

      // 等价重渲染（父组件刷新）不应再次通知：回调身份变化不得驱动 CTISelector 的 effect。
      rerender(<WorkItemClassificationSelect id="cti-loop-2" />);
      await waitFor(() => expect(TicketCategoryApi.getCategoryTree).toHaveBeenCalledTimes(1));

      const depthErrors = consoleError.mock.calls.filter(call =>
        String(call[0]).includes('Maximum update depth exceeded')
      );
      expect(depthErrors).toHaveLength(0);
    } finally {
      consoleError.mockRestore();
    }
  });
});
