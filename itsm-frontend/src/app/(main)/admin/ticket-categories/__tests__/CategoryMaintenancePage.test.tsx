import React from 'react';
import { render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import TicketCategoryManagementPage from '../page';
import { TicketCategoryApi } from '@/lib/api/ticket-category-api';
import { CommonApi } from '@/lib/api/common-api';

jest.mock('@/lib/api/ticket-category-api', () => ({
  TicketCategoryApi: {
    getCategoryTree: jest.fn(),
    createCategory: jest.fn(),
    updateCategory: jest.fn(),
    deleteCategory: jest.fn(),
  },
}));
jest.mock('@/lib/api/common-api', () => ({
  CommonApi: { getDepartments: jest.fn().mockResolvedValue([]) },
}));

const tree = [
  {
    id: 11,
    name: '网络服务',
    code: 'network',
    parentId: null,
    level: 1,
    isActive: true,
    sortOrder: 1,
    description: '',
    departmentId: null,
    createdAt: '',
    updatedAt: '',
    children: [{ id: 12, name: '远程访问', code: 'remote', parentId: 11, level: 2, isActive: true, sortOrder: 1, description: '', departmentId: null, createdAt: '', updatedAt: '' }],
  },
];

const mockedTree = jest.mocked(TicketCategoryApi.getCategoryTree);
const mockedCreate = jest.mocked(TicketCategoryApi.createCategory);

beforeEach(() => {
  jest.clearAllMocks();
  mockedTree.mockResolvedValue(tree as never);
  jest.mocked(CommonApi.getDepartments).mockResolvedValue([] as never);
});

describe('ticket category maintenance page', () => {
  it('loads the maintenance tree including inactive nodes and their paths', async () => {
    render(<TicketCategoryManagementPage />);
    expect(await screen.findByRole('button', { name: '为 网络服务 新增下级分类' })).toBeInTheDocument();
    // 维护界面必须显式请求停用节点，否则无法维护它们的状态。
    expect(mockedTree).toHaveBeenCalledWith({ includeInactive: true });
    expect(await screen.findByText('远程访问')).toBeInTheDocument();
  });

  it('surfaces a load failure instead of an empty classification list', async () => {
    mockedTree.mockRejectedValueOnce(new Error('分类读取失败'));
    render(<TicketCategoryManagementPage />);
    expect(await screen.findByText('分类加载失败')).toBeInTheDocument();
    expect(screen.queryByText('暂无工单分类')).not.toBeInTheDocument();
  });

  it('keeps the user input when saving fails', async () => {
    mockedCreate.mockRejectedValueOnce(new Error('分类编码已存在'));
    render(<TicketCategoryManagementPage />);
    await screen.findByRole('button', { name: '为 网络服务 新增下级分类' });

    await userEvent.click(screen.getByRole('button', { name: /创建一级分类/ }));
    const dialog = await screen.findByRole('dialog');
    await userEvent.type(within(dialog).getByLabelText('分类名称'), '安全事件');
    await userEvent.type(within(dialog).getByLabelText('分类编码'), 'SECURITY');
    await userEvent.click(within(dialog).getByRole('button', { name: /保\s*存/ }));

    // 失败后弹窗保持打开且输入仍在，用户可以直接改代码重试。
    await waitFor(() => expect(mockedCreate).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(within(screen.getByRole('dialog')).getByLabelText('分类名称')).toHaveValue('安全事件');
    expect(within(screen.getByRole('dialog')).getByLabelText('分类编码')).toHaveValue('SECURITY');
  });

  it('switches the narrow-screen pane to the details view after selecting a node', async () => {
    const { container } = render(<TicketCategoryManagementPage />);
    const treeButton = await screen.findByRole('button', { name: '为 网络服务 新增下级分类' });
    expect(treeButton).toBeInTheDocument();

    await userEvent.click(screen.getByText('网络服务'));

    // 窄屏下详情面板显示、树面板隐藏；宽屏由 md:block 保证并排。
    const panes = container.querySelectorAll('.ant-col');
    expect(panes[0]).toHaveClass('hidden');
    expect(panes[1]).toHaveClass('block');
    // 详情面板展示派生路径与编码只读提示（选中的是一级节点，路径即自身）。
    expect(await screen.findByText('一级 · 分类（Category）')).toBeInTheDocument();
    expect(screen.getByText('创建后不可修改')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增下级' })).toBeInTheDocument();
  });
});
