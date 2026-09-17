import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { CTISelector, CTI_INCOMPLETE_MESSAGE, collectCTIPaths, isCTIComplete, toCTIOptions } from '../CTISelector';
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
    id: 11,
    name: '网络服务',
    code: 'network',
    parentId: null,
    level: 1,
    isActive: true,
    path: '网络服务',
    pathIds: [11],
    sortOrder: 1,
    description: '',
    createdAt: '',
    updatedAt: '',
    children: [
      {
        id: 12,
        name: '远程访问',
        code: 'remote',
        parentId: 11,
        level: 2,
        isActive: true,
        path: '网络服务 / 远程访问',
        pathIds: [11, 12],
        sortOrder: 1,
        description: '',
        createdAt: '',
        updatedAt: '',
        children: [
          {
            id: 13,
            name: 'VPN',
            code: 'vpn',
            parentId: 12,
            level: 3,
            isActive: true,
            path: '网络服务 / 远程访问 / VPN',
            pathIds: [11, 12, 13],
            sortOrder: 1,
            description: '',
            createdAt: '',
            updatedAt: '',
          },
        ],
      },
    ],
  },
];

const mockedTree = jest.mocked(TicketCategoryApi.getCategoryTree);

beforeEach(() => {
  jest.clearAllMocks();
  useAuthStore.setState({ isAuthenticated: true, user: { id: 1, tenantId: 2 } as never, currentTenant: { id: 2 } as never });
  mockedTree.mockResolvedValue(tree as never);
});

describe('CTISelector contract', () => {
  it('allows no classification for an ordinary report', async () => {
    const onChange = jest.fn();
    render(<CTISelector value={null} onChange={onChange} requiredDepth={0} />);
    expect(screen.queryByText(CTI_INCOMPLETE_MESSAGE)).not.toBeInTheDocument();
    await waitFor(() => expect(mockedTree).toHaveBeenCalledWith());
  });

  it('accepts a level-2 selection when requiredDepth=0 but reports it incomplete at requiredDepth=3', async () => {
    const { unmount } = render(<CTISelector value={12} onChange={jest.fn()} requiredDepth={0} />);
    // AntD Cascader 把选中的完整路径渲染在带 title 的展示节点上（输入框本身为空）。
    await waitFor(() => expect(screen.getByTitle('网络服务 / 远程访问')).toBeInTheDocument());
    expect(screen.queryByText(CTI_INCOMPLETE_MESSAGE)).not.toBeInTheDocument();
    unmount();

    const onValidityChange = jest.fn();
    render(
      <CTISelector value={12} onChange={jest.fn()} requiredDepth={3} onValidityChange={onValidityChange} />
    );
    await waitFor(() => expect(screen.getByTestId('cti-incomplete')).toHaveTextContent(CTI_INCOMPLETE_MESSAGE));
    await waitFor(() => expect(onValidityChange).toHaveBeenCalledWith(false));
  });

  it('accepts a complete three-level selection', async () => {
    const onValidityChange = jest.fn();
    render(
      <CTISelector value={13} onChange={jest.fn()} requiredDepth={3} onValidityChange={onValidityChange} />
    );
    await waitFor(() => expect(screen.getByTitle('网络服务 / 远程访问 / VPN')).toBeInTheDocument());
    expect(screen.queryByText(CTI_INCOMPLETE_MESSAGE)).not.toBeInTheDocument();
    await waitFor(() => expect(onValidityChange).toHaveBeenCalledWith(true));
  });

  it('emits the deepest node id, never a display name or duplicated path authority', async () => {
    const onChange = jest.fn();
    render(<CTISelector value={null} onChange={onChange} requiredDepth={0} />);
    await userEvent.click(screen.getByRole('combobox'));
    await userEvent.click(await screen.findByText('网络服务'));
    await userEvent.click(await screen.findByText('远程访问'));
    await userEvent.click(await screen.findByText('VPN'));
    // changeOnSelect 允许逐级选择，因此每次选择都会回调；契约是「回调值始终是节点 ID」。
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(13));
    for (const [emitted] of onChange.mock.calls) {
      expect([11, 12, 13]).toContain(emitted);
    }
  });

  it('clears the classification explicitly', async () => {
    const onChange = jest.fn();
    render(<CTISelector value={13} onChange={onChange} requiredDepth={0} allowClear />);
    await userEvent.click(await screen.findByRole('img', { name: 'close-circle' }));
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(null));
  });

  it('surfaces load failures instead of rendering an empty result', async () => {
    mockedTree.mockRejectedValueOnce(new Error('network down'));
    render(<CTISelector value={null} onChange={jest.fn()} requiredDepth={0} />);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('分类加载失败');
    expect(screen.getByRole('combobox')).toBeDisabled();
    expect(screen.queryByText('暂无可用分类')).not.toBeInTheDocument();
  });
});

describe('CTI path helpers', () => {
  it('projects the tree into cascader options without inventing levels', () => {
    const options = toCTIOptions(tree as never);
    expect(options).toHaveLength(1);
    expect(options[0].children?.[0].children?.[0]).toMatchObject({ value: 13, label: 'VPN' });
  });

  it('derives root-to-node path ids from the tree', () => {
    const paths = collectCTIPaths(tree as never);
    expect(paths.get(13)).toEqual([11, 12, 13]);
    expect(paths.get(11)).toEqual([11]);
  });

  it('treats unknown depth as incomplete only when a complete path is required', () => {
    expect(isCTIComplete([11, 12], 0)).toBe(true);
    expect(isCTIComplete([11, 12], 3)).toBe(false);
    expect(isCTIComplete([11, 12, 13], 3)).toBe(true);
    expect(isCTIComplete(undefined, 0)).toBe(true);
  });
});
