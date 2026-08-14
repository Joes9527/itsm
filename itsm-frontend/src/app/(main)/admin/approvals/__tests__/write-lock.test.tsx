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
});
