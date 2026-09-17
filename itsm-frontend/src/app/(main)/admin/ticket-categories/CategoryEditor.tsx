'use client';

import React, { useEffect, useMemo } from 'react';
import { Alert, Form, Input, InputNumber, Modal, Select, Switch, TreeSelect } from 'antd';
import type { TicketCategory } from '@/lib/api/ticket-category-api';
import { CTI_MAX_LEVEL, collectDescendantIds, flattenCategoryTree } from './categoryTreeUtils';

const { TextArea } = Input;

export interface CategoryFormValues {
  name: string;
  code: string;
  description?: string;
  parentId?: number;
  sortOrder?: number;
  isActive?: boolean;
  departmentId?: number;
}

interface DepartmentOption {
  id: number;
  name: string;
}

interface Props {
  open: boolean;
  mode: 'create' | 'edit';
  /** 编辑对象；创建时为 null。 */
  category: TicketCategory | null;
  /** 创建下级时的父级（预填父级，父级不可改）。 */
  lockedParent?: TicketCategory | null;
  categories: TicketCategory[];
  departments: DepartmentOption[];
  saving: boolean;
  /** 保存失败时返回 false，弹窗保持打开并保留用户输入。 */
  onSubmit: (values: CategoryFormValues) => Promise<boolean>;
  onCancel: () => void;
}

interface ParentTreeNode {
  title: string;
  value: number;
  disabled: boolean;
  children?: ParentTreeNode[];
}

/**
 * CategoryEditor：分类创建/编辑表单。
 *
 * - 编码创建后只读（后端同样拒绝修改）；
 * - 父级只能选一级/二级节点，且禁用自身与全部后代，避免超三级或成环；
 * - 新建下级时锁定父级；保存失败保留输入，不出现「成功」后再回滚。
 */
export function CategoryEditor({
  open,
  mode,
  category,
  lockedParent,
  categories,
  departments,
  saving,
  onSubmit,
  onCancel,
}: Props) {
  const [form] = Form.useForm<CategoryFormValues>();
  const flattened = useMemo(() => flattenCategoryTree(categories), [categories]);

  const parentTreeData = useMemo(() => {
    const disabledIds = category ? collectDescendantIds(categories, category.id) : new Set<number>();
    const toNode = (node: TicketCategory): ParentTreeNode => ({
      title: node.name,
      value: node.id,
      // 第三级不能作为父级（会超过三级），自身与后代禁用（避免成环）。
      disabled: node.level >= CTI_MAX_LEVEL || disabledIds.has(node.id),
      children: node.children?.map(toNode),
    });
    return flattenRoots(categories).map(toNode);
  }, [categories, category]);

  useEffect(() => {
    if (!open) return;
    if (category) {
      form.setFieldsValue({
        name: category.name,
        code: category.code,
        description: category.description,
        parentId: category.parentId ?? undefined,
        sortOrder: category.sortOrder,
        isActive: category.isActive,
        departmentId: category.departmentId ?? undefined,
      });
      return;
    }
    form.resetFields();
    form.setFieldsValue({
      sortOrder: 0,
      isActive: true,
      parentId: lockedParent?.id,
    });
  }, [open, category, lockedParent, form]);

  return (
    <Modal
      title={mode === 'edit' ? '编辑分类' : lockedParent ? `在「${lockedParent.name}」下新增分类` : '创建一级分类'}
      open={open}
      onOk={async () => {
        const values = await form.validateFields();
        const saved = await onSubmit(values);
        if (saved) form.resetFields();
      }}
      onCancel={onCancel}
      okText="保存"
      cancelText="取消"
      confirmLoading={saving}
      destroyOnHidden={false}
      width={640}
      // 表单字段较多：在小屏（如 1280x720 笔记本）上整窗会超出视口，
      // 导致「保存/取消」不可达。这里让正文独立滚动、按钮固定可达。
      style={{ top: 24 }}
      styles={{ body: { maxHeight: 'calc(100vh - 200px)', overflowY: 'auto', paddingRight: 8 } }}
    >
      <Alert
        type="info"
        showIcon
        className="mb-4"
        message="工单分类最多三级（分类 / 类型 / 项目），工单只记录所选最深节点。"
      />
      <Form form={form} layout="vertical" preserve>
        <Form.Item name="name" label="分类名称" rules={[{ required: true, message: '请输入分类名称' }]}>
          <Input placeholder="请输入分类名称" />
        </Form.Item>

        <Form.Item
          name="code"
          label="分类编码"
          tooltip="租户内唯一，创建后作为分类的稳定标识，不可修改"
          rules={
            mode === 'edit'
              ? []
              : [
                  { required: true, message: '请输入分类编码' },
                  {
                    pattern: /^[A-Za-z][A-Za-z0-9_-]*$/,
                    message: '以字母开头，仅允许字母、数字、下划线和连字符',
                  },
                ]
          }
        >
          <Input placeholder="如 NETWORK_VPN" disabled={mode === 'edit'} />
        </Form.Item>

        <Form.Item name="parentId" label="父级分类" tooltip="第三级节点不能有下级">
          <TreeSelect
            placeholder="不选择则为一级分类"
            allowClear
            treeDefaultExpandAll
            treeData={parentTreeData}
            disabled={Boolean(lockedParent)}
          />
        </Form.Item>

        <Form.Item name="description" label="分类描述">
          <TextArea rows={3} placeholder="请输入分类描述" />
        </Form.Item>

        <Form.Item
          name="departmentId"
          label="所属部门"
          tooltip="仅表示维护归属，不参与自动分派"
        >
          <Select
            placeholder="请选择部门（可选）"
            allowClear
            showSearch
            optionFilterProp="label"
            options={departments.map(department => ({ value: department.id, label: department.name }))}
          />
        </Form.Item>

        <Form.Item name="sortOrder" label="排序顺序" initialValue={0}>
          <InputNumber min={0} style={{ width: '100%' }} placeholder="数字越小越靠前" />
        </Form.Item>

        <Form.Item name="isActive" label="启用状态" valuePropName="checked" initialValue>
          <Switch checkedChildren="启用" unCheckedChildren="停用" />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/** 仅取根节点（parentId 为空或不在集合内的节点不参与父级选择）。 */
function flattenRoots(categories: TicketCategory[]): TicketCategory[] {
  const flat = flattenCategoryTree(categories);
  const ids = new Set(flat.map(item => item.id));
  return buildTree(categories, ids);
}

function buildTree(nodes: TicketCategory[], ids: Set<number>): TicketCategory[] {
  return nodes
    .filter(node => !node.parentId || !ids.has(node.parentId))
    .map(node => ({ ...node, children: node.children ? buildTree(node.children, ids) : undefined }));
}

export default CategoryEditor;
