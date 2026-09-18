import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { Button, Form } from 'antd';
import { RuleConditionRow, CTI_CONDITION_OPERATORS, RULE_CONDITION_FIELDS } from '../RuleConditionRow';
import { TicketCategoryApi } from '@/lib/api/ticket-category-api';

jest.mock('@/lib/api/ticket-category-api', () => {
  const actual = jest.requireActual('@/lib/api/ticket-category-api');
  return {
    ...actual,
    TicketCategoryApi: { getCategoryTree: jest.fn() },
  };
});

const tree = [
  {
    id: 11, name: '网络服务', code: 'network', parentId: null, level: 1, isActive: true,
    path: '网络服务', pathIds: [11], sortOrder: 1, description: '', createdAt: '', updatedAt: '',
    children: [
      {
        id: 13, name: 'VPN', code: 'vpn', parentId: 12, level: 3, isActive: true,
        path: '网络服务 / 远程访问 / VPN', pathIds: [11, 12, 13], sortOrder: 1,
        description: '', createdAt: '', updatedAt: '', children: [],
      },
      {
        id: 12, name: '远程访问', code: 'remote', parentId: 11, level: 2, isActive: true,
        path: '网络服务 / 远程访问', pathIds: [11, 12], sortOrder: 1,
        description: '', createdAt: '', updatedAt: '', children: [],
      },
    ],
  },
];

// 共享的条件行：分类条件必须显式声明"仅当前 / 包含下级"，并且只提供后端支持的算子。
function renderRow(initialField: string, onChange?: (values: any) => void) {
  const Holder = () => {
    const [form] = Form.useForm();
    return (
      <Form
        form={form}
        initialValues={{ conditions: [{ field: initialField, operator: 'equals', value: 11, scope: 'exact' }] }}
        onValuesChange={() => onChange?.(form.getFieldValue('conditions'))}
      >
        <Form.List name="conditions">
          {fields => (
            <div>
              {fields.map((field, index) => (
                <RuleConditionRow key={field.key} index={index} field={field} fieldValue={form.getFieldValue(['conditions', index])?.field} />
              ))}
              <Button type="primary" htmlType="submit">提交</Button>
            </div>
          )}
        </Form.List>
      </Form>
    );
  };
  return render(<Holder />);
}

beforeEach(() => {
  jest.mocked(TicketCategoryApi.getCategoryTree).mockResolvedValue(tree as never);
});

it('exposes the backend condition vocabulary and only the supported category operators', () => {
  // 字段词汇必须与后端 evaluateTicketRuleConditions 一致：不支持 title，申请人用 requester_id。
  expect(RULE_CONDITION_FIELDS.map(option => option.value)).toEqual([
    'status', 'priority', 'category_id', 'department_id', 'requester_id', 'assignee_id',
  ]);
  expect(CTI_CONDITION_OPERATORS.map(option => option.value)).toEqual(['equals', 'not_equals', 'in', 'not_in']);
});

it('renders the scope selector with 仅当前分类 / 包含下级 and defaults to exact', async () => {
  renderRow('category_id');
  // 下拉展开后必须同时提供两种范围。
  await userEvent.click(await screen.findByText('仅当前分类'));
  expect(await screen.findByTitle('包含下级')).toBeInTheDocument();
  expect(screen.getAllByTitle('仅当前分类').length).toBeGreaterThan(0);
});

it('lets the operator switch the classification condition to 包含下级', async () => {
  let latest: any[] = [];
  renderRow('category_id', values => { latest = values; });
  await userEvent.click(await screen.findByText('仅当前分类'));
  await userEvent.click(await screen.findByTitle('包含下级'));
  await waitFor(() => expect(latest[0]?.scope).toBe('subtree'));
});

it('hides the category scope for non-classification conditions', async () => {
  renderRow('status');
  await waitFor(() => expect(screen.queryByText('仅当前分类')).not.toBeInTheDocument());
  // 非分类条件仍是普通取值输入。
  expect(screen.getByPlaceholderText('值')).toBeInTheDocument();
});

it('offers the shared classification picker for classification conditions', async () => {
  const { container } = renderRow('category_id');
  // 复用共享 CTISelector（requiredDepth=0，允许指向任意层级节点），
  // 其 data-cti-complete 标记用于表达"当前选择是否完整"。
  await waitFor(() => expect(container.querySelector('[data-cti-complete]')).toBeTruthy());
  // 非分类条件不使用分类选择器。
  const statusRender = renderRow('status');
  await waitFor(() => expect(statusRender.container.querySelector('[data-cti-complete]')).toBeNull());
});
