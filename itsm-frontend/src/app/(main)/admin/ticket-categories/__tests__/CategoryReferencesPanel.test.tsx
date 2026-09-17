import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { CategoryReferencesPanel, blocksMaintenance } from '../CategoryReferencesPanel';
import type { CTIReferenceView } from '@/lib/api/ticket-category-api';

const path = [
  { id: 11, parentId: null, level: 1, name: '网络服务', code: 'network', isActive: true },
  { id: 12, parentId: 11, level: 2, name: '远程访问', code: 'remote', isActive: true },
];

const view = (groups: CTIReferenceView['groups'], overrides: Partial<CTIReferenceView> = {}): CTIReferenceView => ({
  categoryId: 12,
  categoryPath: path,
  blocking: groups.some(group => group.referenced),
  page: 1,
  pageSize: 20,
  groups,
  ...overrides,
});

// 引用页签的边界：无权时只显示"存在引用"（不得用 0 伪装），失败必须显式报错。
it('lists visible references with names, counts and a management link', async () => {
  const loader = jest.fn().mockResolvedValue(view([
    { kind: 'catalog', referenced: true, visible: true, total: 2, items: [{ id: 7, name: '引用目录A' }, { id: 8, name: '引用目录B' }] },
    { kind: 'sla_definition', referenced: true, visible: true, total: 1, items: [{ id: 3, name: '标准SLA' }] },
  ]));
  render(<CategoryReferencesPanel categoryId={12} loadReferences={loader} />);

  expect(await screen.findByText('该分类（或其下级）已被引用，不可删除或移动')).toBeInTheDocument();
  expect(await screen.findByText('引用目录A')).toBeInTheDocument();
  expect(screen.getByText('引用目录B')).toBeInTheDocument();
  expect(screen.getByText('标准SLA')).toBeInTheDocument();
  // 管理入口必须指向真实存在的模块路由。
  const links = screen.getAllByRole('link', { name: '前往管理' });
  expect(links.map(link => link.getAttribute('href'))).toEqual(['/admin/service-catalogs', '/admin/sla-definitions']);
  expect(loader).toHaveBeenCalledWith(12, { page: 1, pageSize: 20 });
});

it('reports existence without leaking counts or names when details are not permitted', async () => {
  const loader = jest.fn().mockResolvedValue(view([
    { kind: 'process_binding', referenced: true, visible: false },
    { kind: 'incident_escalation_rule', referenced: true, visible: false },
  ]));
  render(<CategoryReferencesPanel categoryId={12} loadReferences={loader} />);

  expect(await screen.findByTestId('cti-reference-hidden-process_binding')).toHaveTextContent('存在引用（无权查看明细）');
  expect(screen.getByTestId('cti-reference-hidden-incident_escalation_rule')).toBeInTheDocument();
  expect(screen.getByTestId('cti-references-hidden-notice')).toHaveTextContent('有 2 类引用对象超出你的查看权限');
  // 不得出现任何计数或名称。
  expect(screen.queryByText(/共 \d+ 项引用/)).not.toBeInTheDocument();
  // 历史字符串引用必须说明真实语义，不伪装成结构化规则。
  expect(screen.getByText(/无法映射为 ID/)).toBeInTheDocument();
  // blocking 依然为真：无权查看不能绕过维护保护。
  expect(blocksMaintenance(view([{ kind: 'process_binding', referenced: true, visible: false }]))).toBe(true);
});

it('shows a clean empty state when nothing references the category', async () => {
  const loader = jest.fn().mockResolvedValue(view([{ kind: 'catalog', referenced: false, visible: true, total: 0, items: [] }]));
  render(<CategoryReferencesPanel categoryId={12} loadReferences={loader} />);
  expect(await screen.findByText('当前没有结构性引用')).toBeInTheDocument();
  expect(screen.getByText('该分类及下级暂无引用')).toBeInTheDocument();
});

it('surfaces query failures instead of rendering a fake empty result', async () => {
  const loader = jest.fn().mockRejectedValue(new Error('后端不可用'));
  render(<CategoryReferencesPanel categoryId={12} loadReferences={loader} />);
  expect(await screen.findByText('引用查询失败')).toBeInTheDocument();
  expect(screen.getByText('后端不可用')).toBeInTheDocument();
  expect(screen.queryByText('该分类及下级暂无引用')).not.toBeInTheDocument();
  expect(screen.queryByText('当前没有结构性引用')).not.toBeInTheDocument();
});

it('paginates kinds whose totals exceed one page', async () => {
  const loader = jest.fn()
    .mockResolvedValueOnce(view([{ kind: 'catalog', referenced: true, visible: true, total: 25, items: [{ id: 7, name: '第一页目录' }] }]))
    .mockResolvedValueOnce(view([{ kind: 'catalog', referenced: true, visible: true, total: 25, items: [{ id: 30, name: '第二页目录' }] }]));
  render(<CategoryReferencesPanel categoryId={12} loadReferences={loader} />);

  expect(await screen.findByText('第一页目录')).toBeInTheDocument();
  await userEvent.click(screen.getByTitle('2'));
  await waitFor(() => expect(screen.getByText('第二页目录')).toBeInTheDocument());
  // 第二页请求必须带上页码，且每类总数来自后端真实值。
  expect(loader).toHaveBeenLastCalledWith(12, { page: 2, pageSize: 20 });
});
