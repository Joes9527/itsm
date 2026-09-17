'use client';

import React, { useMemo, useState } from 'react';
import { Alert, Button, Empty, Input, Space, Tag, Tree, Typography } from 'antd';
import { Plus } from 'lucide-react';
import type { TicketCategory } from '@/lib/api/ticket-category-api';
import {
  buildCategoryTree,
  canAddChild,
  filterByPath,
  flattenCategoryTree,
} from './categoryTreeUtils';

interface Props {
  categories: TicketCategory[];
  loading: boolean;
  error: string;
  selectedId: number | null;
  onSelect: (category: TicketCategory) => void;
  onAddChild: (parent: TicketCategory) => void;
  onRetry: () => void;
}

interface PanelTreeNode {
  key: number;
  title: React.ReactNode;
  children?: PanelTreeNode[];
}

/**
 * CategoryTreePanel：左树。只负责导航与「新增下级」入口。
 *
 * 三级约束：第三级节点不提供新增下级（后端同样拒绝超三级）。
 * 搜索按完整路径匹配并直接展示路径，避免同名分类无法区分。
 */
export function CategoryTreePanel({
  categories,
  loading,
  error,
  selectedId,
  onSelect,
  onAddChild,
  onRetry,
}: Props) {
  const [keyword, setKeyword] = useState('');
  const tree = useMemo(() => buildCategoryTree(flattenCategoryTree(categories)), [categories]);
  const flattened = useMemo(() => flattenCategoryTree(categories), [categories]);
  const matches = useMemo(() => filterByPath(flattened, keyword), [flattened, keyword]);
  const searching = keyword.trim().length > 0;

  const toNode = (node: TicketCategory): PanelTreeNode => ({
    key: node.id,
    title: (
      <span className="flex w-full items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{node.name}</span>
          <Typography.Text type="secondary" className="text-[12px]">
            {node.code}
          </Typography.Text>
          {!node.isActive ? <Tag>已停用</Tag> : null}
        </span>
        {canAddChild(node.level) ? (
          <Button
            size="small"
            type="text"
            aria-label={`为 ${node.name} 新增下级分类`}
            icon={<Plus size={12} />}
            onClick={event => {
              event.stopPropagation();
              onAddChild(node);
            }}
          />
        ) : null}
      </span>
    ),
    children: node.children?.map(toNode),
  });

  if (error) {
    return (
      <Alert
        type="error"
        message="分类加载失败"
        description={error}
        action={<Button onClick={onRetry}>重试</Button>}
      />
    );
  }

  return (
    <Space orientation="vertical" style={{ width: '100%' }} size="middle">
      <Input
        allowClear
        prefix={<span aria-hidden>🔍</span>}
        placeholder="搜索完整路径（如 网络服务 / 远程访问 / VPN）"
        aria-label="搜索工单分类"
        value={keyword}
        onChange={event => setKeyword(event.target.value)}
      />
      {searching ? (
        matches.length === 0 ? (
          <Empty description="没有匹配的分类" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        ) : (
          <ul aria-label="分类搜索结果" className="space-y-1">
            {matches.map(item => (
              <li key={item.id}>
                <Button
                  type={item.id === selectedId ? 'primary' : 'text'}
                  block
                  className="h-auto justify-start py-2 text-left whitespace-normal"
                  onClick={() => onSelect(item)}
                >
                  <span className="flex flex-col items-start gap-1">
                    <span className="flex items-center gap-2">
                      {item.name}
                      {!item.isActive ? <Tag>已停用</Tag> : null}
                    </span>
                    <Typography.Text type="secondary" className="text-[12px]">
                      {item.pathLabel}
                    </Typography.Text>
                  </span>
                </Button>
              </li>
            ))}
          </ul>
        )
      ) : tree.length === 0 && !loading ? (
        <Empty description="暂无工单分类" image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : (
        <Tree
          treeData={tree.map(toNode)}
          defaultExpandAll
          blockNode
          selectedKeys={selectedId ? [selectedId] : []}
          onSelect={keys => {
            const id = Number(keys[0]);
            const picked = flattened.find(item => item.id === id);
            if (picked) onSelect(picked);
          }}
        />
      )}
    </Space>
  );
}

export default CategoryTreePanel;
