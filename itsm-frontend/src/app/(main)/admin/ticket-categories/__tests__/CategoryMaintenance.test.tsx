import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { CategoryTreePanel } from '../CategoryTreePanel';
import { CategoryDetailsPanel } from '../CategoryDetailsPanel';
import { CategoryEditor } from '../CategoryEditor';
import type { TicketCategory } from '@/lib/api/ticket-category-api';

const category = (id: number, name: string, level: number, parentId: number | null, extra: Partial<TicketCategory> = {}): TicketCategory => ({
  id,
  name,
  code: `c${id}`,
  description: '',
  parentId,
  level,
  sortOrder: id,
  isActive: true,
  departmentId: null,
  createdAt: '',
  updatedAt: '',
  ...extra,
});

const leaf = category(13, 'VPN', 3, 12);
const type = category(12, '远程访问', 2, 11);
const root = category(11, '网络服务', 1, null, { children: [type] });
type.children = [leaf];
const tree = [root];

describe('CategoryTreePanel', () => {
  it('offers "add child" on levels one and two but never on level three', async () => {
    render(
      <CategoryTreePanel
        categories={tree}
        loading={false}
        error=""
        selectedId={null}
        onSelect={jest.fn()}
        onAddChild={jest.fn()}
        onRetry={jest.fn()}
      />
    );
    expect(await screen.findByRole('button', { name: '为 网络服务 新增下级分类' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '为 远程访问 新增下级分类' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '为 VPN 新增下级分类' })).not.toBeInTheDocument();
  });

  it('searches by full path so duplicate names remain distinguishable', async () => {
    const onSelect = jest.fn();
    render(
      <CategoryTreePanel
        categories={tree}
        loading={false}
        error=""
        selectedId={null}
        onSelect={onSelect}
        onAddChild={jest.fn()}
        onRetry={jest.fn()}
      />
    );
    await userEvent.type(screen.getByLabelText('搜索工单分类'), '网络服务 / 远程访问 / VPN');
    const results = await screen.findByRole('list', { name: '分类搜索结果' });
    expect(results).toHaveTextContent('网络服务 / 远程访问 / VPN');
    await userEvent.click(screen.getByRole('button', { name: /VPN/ }));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 13 }));
  });

  it('shows a retryable error instead of an empty result', async () => {
    const onRetry = jest.fn();
    render(
      <CategoryTreePanel
        categories={[]}
        loading={false}
        error="分类加载失败：network down"
        selectedId={null}
        onSelect={jest.fn()}
        onAddChild={jest.fn()}
        onRetry={onRetry}
      />
    );
    expect(screen.getByText('分类加载失败')).toBeInTheDocument();
    expect(screen.queryByText('暂无工单分类')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    expect(onRetry).toHaveBeenCalled();
  });
});

describe('CategoryDetailsPanel', () => {
  it('shows the full C/T/I path and never implies departments drive assignment', () => {
    render(
      <CategoryDetailsPanel
        category={leaf}
        categories={tree}
        departments={[{ id: 7, name: '基础架构组' }]}
        onEdit={jest.fn()}
        onAddChild={jest.fn()}
        onCopy={jest.fn()}
        onDelete={jest.fn()}
        onToggleStatus={jest.fn()}
      />
    );
    expect(screen.getByText('网络服务 / 远程访问 / VPN')).toBeInTheDocument();
    expect(screen.getByText('一级 · 分类（Category）')).toBeInTheDocument();
    expect(screen.getByText('三级 · 项目（Item）')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增下级' })).not.toBeInTheDocument();
  });

  it('keeps "add child" for the first two levels', () => {
    render(
      <CategoryDetailsPanel
        category={type}
        categories={tree}
        departments={[]}
        onEdit={jest.fn()}
        onAddChild={jest.fn()}
        onCopy={jest.fn()}
        onDelete={jest.fn()}
        onToggleStatus={jest.fn()}
      />
    );
    expect(screen.getByRole('button', { name: '新增下级' })).toBeInTheDocument();
    expect(screen.getByText(/不代表自动分派对象/)).toBeInTheDocument();
  });
});

describe('CategoryEditor', () => {
  it('locks the code on edit and disables level-three parents', async () => {
    render(
      <CategoryEditor
        open
        mode="edit"
        category={type}
        categories={tree}
        departments={[]}
        saving={false}
        onSubmit={jest.fn().mockResolvedValue(true)}
        onCancel={jest.fn()}
      />
    );
    expect(screen.getByLabelText('分类编码')).toBeDisabled();
    expect(screen.getByLabelText('分类编码')).toHaveValue('c12');
    expect(screen.getByText(/最多三级/)).toBeInTheDocument();
  });

  it('prefills the parent when creating a child and locks it', () => {
    render(
      <CategoryEditor
        open
        mode="create"
        category={null}
        lockedParent={root}
        categories={tree}
        departments={[]}
        saving={false}
        onSubmit={jest.fn().mockResolvedValue(true)}
        onCancel={jest.fn()}
      />
    );
    expect(screen.getByText('在「网络服务」下新增分类')).toBeInTheDocument();
  });
});
