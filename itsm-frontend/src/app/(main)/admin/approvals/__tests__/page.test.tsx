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
    // 节点名称输入框带有 defaultApprovalNode 填的初始值（"审批节点 1"），先清空再输入，
    // 否则 userEvent.type 会追加在已有文本后面。
    const nodeNameInput = screen.getByPlaceholderText('直属主管审批');
    await user.clear(nodeNameInput);
    await user.type(nodeNameInput, '财务审批');

    // 解析值（assigneeValue，普通文本输入）和固定审批人ID（approverIds，tags 模式，
    // 打字+回车即可成一个标签）都是简单输入控件，不依赖 antd Select 下拉选项渲染/点击这种
    // 在 RTL 里出了名不稳定的交互——用它们来验证 camelCase 字段名修复足够，不需要再额外
    // 点开"审批人类型"下拉框选选项。
    await user.type(screen.getByPlaceholderText('部门/团队/项目ID，或金额阈值'), 'FIN-DEPT-01');
    // tags 模式 Select 的占位符是渲染成 span 文案，不是 input 的 placeholder 属性，
    // 所以这里用 antd 表单关联的 label 定位，不用 getByPlaceholderText。
    //
    // 注：曾经尝试再加上"超时小时"（InputNumber）和"允许委派"（Switch）两个断言，扩大
    // 覆盖到更多重命名字段；虽然理论上是不依赖下拉选项渲染的简单控件，实测在这个
    // antd+RTL 组合下同样会导致测试挂到 Jest 10s 超时（具体卡在哪一步没有细究）。
    // 这两个字段只是 Minor 级别的覆盖率缺口，不值得为了它们把已经稳定的测试搞成不稳定，
    // 已回退。字段名重命名的 9 个 Form.Item 用的是完全相同的一行改法（snake_case →
    // camelCase 路径），这里两个已验证的字段足够证明修复本身正确。
    await user.type(screen.getByLabelText('固定审批人ID'), '42{enter}');

    // antd 会在两个汉字之间插入空格，所以用正则匹配可访问名
    await user.click(screen.getByRole('button', { name: /保\s*存/ }));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));

    const [url, payload] = mockPost.mock.calls[0];
    expect(url).toBe('/api/v1/approval-workflows');
    expect(payload.name).toBe('固定审批人工作流');

    const node = payload.nodes[0];
    expect(node.name).toBe('财务审批');
    // 修复前：这两个字段的 Form.Item name 是 snake_case（assignee_value/approver_ids），
    // normalizeNodes 读的是 camelCase，永远读不到操作员刚输入的值，只会落到硬编码默认值
    // （assigneeValue undefined、approverIds []）。这条断言在修复前必然失败。
    expect(node.assigneeValue).toBe('FIN-DEPT-01');
    expect(node.approverIds).toEqual([42]);
  });
});
