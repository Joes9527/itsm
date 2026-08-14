import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import ApprovalManagement from '../page';
import { httpClient } from '@/lib/api/http-client';
import { WorkflowDefinitionApi } from '@/lib/api/workflow-definition-api';

// 这个页面的 Form.List 节点编辑器以前把 Form.Item 的 name 路径写成 snake_case
// （'approver_type'/'approver_ids'/...），而 ApprovalNode 接口、defaultApprovalNode、
// normalizeNodes、handleEdit 的 setFieldsValue 全都用 camelCase。两边是不同的字符串键，
// 于是 antd Form 存的值和周围 TS 代码读的值对不上：操作员选什么都会被丢掉，保存时提交的
// 永远是 defaultApprovalNode 的硬编码默认值（approverType 恒为 'role'、approverIds 恒为 []）。
// 这条测试真填一遍表单，断言提交给 httpClient.post 的是操作员实际选的值。

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

const mockGet = httpClient.get as jest.Mock;
const mockPost = httpClient.post as jest.Mock;
const mockGetWorkflows = WorkflowDefinitionApi.getWorkflows as jest.Mock;

describe('ApprovalManagement 节点编辑器字段名', () => {
  jest.setTimeout(30000);
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue({ items: [], total: 0 });
    mockPost.mockResolvedValue({ id: 1 });
    mockGetWorkflows.mockResolvedValue({ workflows: [] });
  });

  it('提交操作员实际选择的 camelCase 节点值，而不是硬编码默认值', async () => {
    const user = userEvent.setup();
    render(<ApprovalManagement />);

    await waitFor(() => expect(mockGet).toHaveBeenCalled());

    await user.click(screen.getByRole('button', { name: /新建工作流/ }));

    await user.type(await screen.findByPlaceholderText('请输入工作流名称'), '固定审批人工作流');
    await user.type(screen.getByPlaceholderText('直属主管审批'), '财务审批');

    // 审批人类型：默认是 '指定角色'(role)，操作员改成 '指定用户'(user)
    await user.click(screen.getByLabelText('审批人类型'));
    await user.click(await screen.findByTitle('指定用户'));

    // 固定审批人ID：tags 模式，输入 42 回车成为一个标签
    await user.type(screen.getByLabelText('固定审批人ID'), '42{enter}');

    // antd 会在两个汉字之间插入空格，所以用正则匹配可访问名
    await user.click(screen.getByRole('button', { name: /保\s*存/ }));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));

    const [url, payload] = mockPost.mock.calls[0];
    expect(url).toBe('/api/v1/approval-workflows');
    expect(payload.name).toBe('固定审批人工作流');

    const node = payload.nodes[0];
    expect(node.name).toContain('财务审批');
    expect(node.approverType).toBe('user');
    expect(node.approverIds).toEqual([42]);
  });
});
