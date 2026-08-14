import { render, screen, waitFor } from '@/lib/test-utils';
import ApprovalManagement from '../page';
import { httpClient } from '@/lib/api/http-client';
import { WorkflowDefinitionApi } from '@/lib/api/workflow-definition-api';
import { SystemConfigAPI } from '@/lib/api/system-config-api';

jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    patch: jest.fn(),
    delete: jest.fn(),
  },
}));

jest.mock('@/lib/api/workflow-definition-api', () => ({
  WorkflowDefinitionApi: {
    getWorkflows: jest.fn(),
  },
}));

jest.mock('@/lib/api/system-config-api', () => ({
  SystemConfigAPI: {
    getConfigByKey: jest.fn(),
  },
}));

const mockGet = httpClient.get as jest.Mock;
const mockGetWorkflows = WorkflowDefinitionApi.getWorkflows as jest.Mock;
const mockGetConfigByKey = SystemConfigAPI.getConfigByKey as jest.Mock;

const oneWorkflow = {
  items: [
    {
      id: 1,
      name: '存量工作流',
      isActive: true,
      nodes: [],
    },
  ],
  total: 1,
};

describe('ApprovalManagement 写锁定状态', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue(oneWorkflow);
    mockGetWorkflows.mockResolvedValue({ workflows: [] });
  });

  it('锁定时禁用新建按钮，不渲染编辑/删除按钮', async () => {
    mockGetConfigByKey.mockResolvedValue({ id: 1, key: 'legacyApprovalWriteLocked', value: 'true', category: 'approval', createdAt: '', updatedAt: '' });

    render(<ApprovalManagement />);

    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await waitFor(() => expect(mockGetConfigByKey).toHaveBeenCalledWith('legacyApprovalWriteLocked'));

    const createButton = await screen.findByRole('button', { name: /新建工作流/ });
    await waitFor(() => expect(createButton).toBeDisabled());

    // Edit/Trash2 都是纯 icon 按钮，没有 accessible name，query 不到具体某一个，
    // 所以直接断言这一行渲染出来的操作列里，除了状态标签之外没有任何可点击的图标按钮。
    const row = (await screen.findByText('存量工作流')).closest('tr');
    expect(row).not.toBeNull();
    const iconButtons = row!.querySelectorAll('button.ant-btn-icon-only, button.ant-btn-circle');
    expect(iconButtons.length).toBe(0);
  });

  it('未锁定（配置不存在，接口 404）时新建按钮保持可用——回归', async () => {
    mockGetConfigByKey.mockRejectedValue(new Error('HTTP error! status: 404'));

    render(<ApprovalManagement />);

    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await waitFor(() => expect(mockGetConfigByKey).toHaveBeenCalledWith('legacyApprovalWriteLocked'));

    const createButton = await screen.findByRole('button', { name: /新建工作流/ });
    expect(createButton).not.toBeDisabled();
  });

  it('已知锁定状态不会被第二次非404错误翻回未锁定 —— 回归', async () => {
    // 当前组件在挂载时只拉取一次写锁定状态，没有手动刷新写锁定状态的入口
    // （"刷新"按钮只重新拉取工作流列表），所以这里没有办法通过真实的用户操作触发第二次
    // GetConfigByKey 调用。用 rerender 复现"同一个已挂载实例，state 应该保持"这个场景：
    // 第一次拉取返回锁定，等锁定状态稳定生效之后，把 mock 换成拒绝（非 404），再 rerender。
    // 无论是"effect 依赖没变所以压根没有重新发起请求"，还是"发起了但被正确忽略"，
    // 断言的结果都应该一致：已经确认过的"锁定"状态不能被翻回"未锁定"。
    mockGetConfigByKey.mockResolvedValueOnce({
      id: 1,
      key: 'legacyApprovalWriteLocked',
      value: 'true',
      category: 'approval',
      createdAt: '',
      updatedAt: '',
    });

    const { rerender } = render(<ApprovalManagement />);

    await waitFor(() => expect(mockGetConfigByKey).toHaveBeenCalledWith('legacyApprovalWriteLocked'));
    const createButton = await screen.findByRole('button', { name: /新建工作流/ });
    await waitFor(() => expect(createButton).toBeDisabled());

    mockGetConfigByKey.mockRejectedValueOnce(new Error('HTTP error! status: 500'));
    rerender(<ApprovalManagement />);

    await waitFor(() => expect(createButton).toBeDisabled());
  });
});
