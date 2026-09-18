'use client';

import React from 'react';
import { Button, Descriptions, Empty, Space, Switch, Tabs, Tag, Typography } from 'antd';
import { Copy, Delete, Edit, Plus } from 'lucide-react';
import type { TicketCategory } from '@/lib/api/ticket-category-api';
import { canAddChild, flattenCategoryTree } from './categoryTreeUtils';
import { CategoryReferencesPanel } from './CategoryReferencesPanel';

interface DepartmentOption {
  id: number;
  name: string;
}

interface Props {
  category: TicketCategory | null;
  categories: TicketCategory[];
  departments: DepartmentOption[];
  onEdit: (category: TicketCategory) => void;
  onAddChild: (parent: TicketCategory) => void;
  onCopy: (category: TicketCategory) => void;
  onDelete: (category: TicketCategory) => void;
  onToggleStatus: (category: TicketCategory, checked: boolean) => void;
  onBackToList?: () => void;
}

const LEVEL_LABELS = ['一级 · 分类（Category）', '二级 · 类型（Type）', '三级 · 项目（Item）'];

/**
 * CategoryDetailsPanel：右详情。展示完整 C/T/I 路径与基本信息，并提供维护动作。
 *
 * 说明边界：所属部门只表示维护归属，不参与自动分派；分派规则仍由分派模块维护。
 */
export function CategoryDetailsPanel({
  category,
  categories,
  departments,
  onEdit,
  onAddChild,
  onCopy,
  onDelete,
  onToggleStatus,
  onBackToList,
}: Props) {
  if (!category) {
    return <Empty description="请选择左侧分类查看详情" />;
  }
  const flattened = flattenCategoryTree(categories);
  const self = flattened.find(item => item.id === category.id);
  const pathLabel = self?.pathLabel ?? category.name;
  const pathIds = self?.pathIds ?? [category.id];
  const departmentName = category.departmentId
    ? departments.find(item => item.id === category.departmentId)?.name ?? `部门 #${category.departmentId}`
    : null;

  return (
    <div className="space-y-4">
      {onBackToList ? (
        <Button className="md:hidden" onClick={onBackToList}>
          返回分类列表
        </Button>
      ) : null}

      <Space wrap>
        <Button type="primary" icon={<Edit size={14} />} onClick={() => onEdit(category)}>
          编辑
        </Button>
        {canAddChild(category.level) ? (
          <Button icon={<Plus size={14} />} onClick={() => onAddChild(category)}>
            新增下级
          </Button>
        ) : null}
        <Button icon={<Copy size={14} />} onClick={() => onCopy(category)}>
          复制
        </Button>
        <Button danger icon={<Delete size={14} />} onClick={() => onDelete(category)}>
          删除
        </Button>
      </Space>

      <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="完整路径（C / T / I）">
          <div className="space-y-1">
            {pathIds.map((id, index) => {
              const node = flattened.find(item => item.id === id);
              return (
                <div key={id} className="flex items-center gap-2">
                  <Tag color={index === 0 ? 'blue' : 'cyan'}>{LEVEL_LABELS[index] ?? `第 ${index + 1} 级`}</Tag>
                  <span>{node?.name ?? `#${id}`}</span>
                  {node && !node.isActive ? <Tag>已停用</Tag> : null}
                </div>
              );
            })}
          </div>
          <Typography.Paragraph type="secondary" className="mt-1 mb-0 text-[12px]">
            {pathLabel}
          </Typography.Paragraph>
        </Descriptions.Item>
        <Descriptions.Item label="分类编码">
          <Typography.Text code>{category.code}</Typography.Text>
          <Typography.Text type="secondary" className="ml-2 text-[12px]">
            创建后不可修改
          </Typography.Text>
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Switch
            checked={category.isActive}
            checkedChildren="启用"
            unCheckedChildren="停用"
            onChange={checked => onToggleStatus(category, checked)}
          />
          <Typography.Text type="secondary" className="ml-2 text-[12px]">
            停用后保留历史引用，仅禁止新选择
          </Typography.Text>
        </Descriptions.Item>
        <Descriptions.Item label="排序顺序">{category.sortOrder}</Descriptions.Item>
        <Descriptions.Item label="分类描述">{category.description || '-'}</Descriptions.Item>
        <Descriptions.Item label="所属部门">
          {departmentName ? <Tag color="geekblue">{departmentName}</Tag> : <Typography.Text type="secondary">未设置</Typography.Text>}
          <Typography.Paragraph type="secondary" className="mt-1 mb-0 text-[12px]">
            仅表示分类的维护归属，不代表自动分派对象；分派规则由分派模块单独维护。
          </Typography.Paragraph>
        </Descriptions.Item>
      </Descriptions>

      {/* 关联与引用分区：真实引用来自后端安全扫描，明细按当前账号权限过滤。 */}
      <Tabs
        defaultActiveKey="references"
        items={[
          {
            key: 'references',
            label: '关联与引用',
            children: <CategoryReferencesPanel key={category.id} categoryId={category.id} />,
          },
        ]}
      />
    </div>
  );
}

export default CategoryDetailsPanel;
