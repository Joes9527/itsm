'use client';

/**
 * 工单分类（CTI）维护界面：左树右详情。
 *
 * 维护的是工单的三级业务分类，供工单录入、统计及处理规则引用。
 * 分派、SLA 与 BPMN 规则由各自所有者维护，这里不复制它们的业务规则。
 */

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Col, Modal, Row, Typography, message } from 'antd';
import { Plus } from 'lucide-react';
import { LoadingSkeleton } from '@/components/ui/LoadingSkeleton';
import { TicketCategoryApi, type TicketCategory } from '@/lib/api/ticket-category-api';
import { CommonApi } from '@/lib/api/common-api';
import { buildCategoryTree, flattenCategoryTree } from './categoryTreeUtils';
import { CategoryTreePanel } from './CategoryTreePanel';
import { CategoryDetailsPanel } from './CategoryDetailsPanel';
import { CategoryEditor, type CategoryFormValues } from './CategoryEditor';

const { Title, Text } = Typography;

interface DepartmentOption {
  id: number;
  name: string;
}

interface EditorState {
  mode: 'create' | 'edit';
  category: TicketCategory | null;
  lockedParent: TicketCategory | null;
}

const TicketCategoryManagementPage = () => {
  const [categories, setCategories] = useState<TicketCategory[]>([]);
  const [departments, setDepartments] = useState<DepartmentOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [editor, setEditor] = useState<EditorState | null>(null);
  // 窄屏：树与详情互斥显示；宽屏始终并排。
  const [mobilePane, setMobilePane] = useState<'tree' | 'details'>('tree');

  const loadCategories = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    try {
      // 维护界面必须看到停用节点与状态，并且需要派生的完整路径。
      const rows = await TicketCategoryApi.getCategoryTree({ includeInactive: true });
      setCategories(Array.isArray(rows) ? rows : []);
    } catch (error) {
      // 加载失败不伪装成空分类：清空并给出可重试的错误状态。
      console.error('Failed to load categories:', error);
      setLoadError(error instanceof Error ? error.message : '分类加载失败，请重试');
      setCategories([]);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadDepartments = useCallback(async () => {
    try {
      const response = await CommonApi.getDepartments();
      const list = Array.isArray(response) ? response : [];
      setDepartments(
        list
          .filter((item: { id?: number; name?: string }) => item?.id && item?.name)
          .map((item: { id: number; name: string }) => ({ id: item.id, name: item.name }))
      );
    } catch (error) {
      // 部门仅用于维护归属展示，加载失败不阻塞分类维护主流程。
      console.error('Failed to load departments:', error);
    }
  }, []);

  useEffect(() => {
    void loadCategories();
    void loadDepartments();
  }, [loadCategories, loadDepartments]);

  const flattened = useMemo(() => flattenCategoryTree(categories), [categories]);
  const tree = useMemo(() => buildCategoryTree(flattened), [flattened]);
  const selected = useMemo(
    () => flattened.find(item => item.id === selectedId) ?? null,
    [flattened, selectedId]
  );

  const reloadAndKeep = async (id: number | null) => {
    await loadCategories();
    setSelectedId(id);
  };

  const handleSubmit = async (values: CategoryFormValues): Promise<boolean> => {
    setSaving(true);
    try {
      if (editor?.mode === 'edit' && editor.category) {
        // 编码由后端保证创建后不可修改，这里不提交编码字段。
        await TicketCategoryApi.updateCategory(editor.category.id, {
          name: values.name,
          description: values.description,
          parentId: values.parentId ?? 0,
          sortOrder: values.sortOrder,
          isActive: values.isActive,
          departmentId: values.departmentId ?? 0,
        });
        message.success('分类更新成功');
        const kept = editor.category.id;
        setEditor(null);
        await reloadAndKeep(kept);
        return true;
      }
      const created = await TicketCategoryApi.createCategory({
        name: values.name,
        code: values.code,
        description: values.description,
        parentId: values.parentId,
        sortOrder: values.sortOrder,
        isActive: values.isActive ?? true,
        departmentId: values.departmentId,
      });
      message.success('分类创建成功');
      setEditor(null);
      await reloadAndKeep(created?.id ?? values.parentId ?? null);
      return true;
    } catch (error) {
      // 失败保留弹窗与用户输入，先说明后端给出的可操作原因（路径失效/冲突/被引用）。
      message.error(error instanceof Error ? error.message : '保存失败，请重试');
      return false;
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = (category: TicketCategory) => {
    Modal.confirm({
      title: `确定删除「${category.name}」吗？`,
      content: '仅当该分类没有下级分类且未被工单、目录或规则引用时才能删除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await TicketCategoryApi.deleteCategory(category.id);
          message.success('分类删除成功');
          await reloadAndKeep(null);
        } catch (error) {
          message.error(error instanceof Error ? error.message : '删除失败');
        }
      },
    });
  };

  const handleCopy = async (category: TicketCategory) => {
    try {
      await TicketCategoryApi.createCategory({
        name: `${category.name} - 副本`,
        code: `${category.code}_COPY_${Date.now().toString(36).toUpperCase()}`,
        description: category.description,
        parentId: category.parentId ?? undefined,
        sortOrder: category.sortOrder,
        isActive: category.isActive,
        departmentId: category.departmentId ?? undefined,
      });
      message.success('分类复制成功');
      await reloadAndKeep(category.id);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '复制失败');
    }
  };

  const handleToggleStatus = async (category: TicketCategory, checked: boolean) => {
    try {
      await TicketCategoryApi.updateCategory(category.id, { isActive: checked });
      setCategories(previous => updateTreeStatus(previous, category.id, checked));
      message.success(checked ? '分类已启用' : '分类已停用');
    } catch (error) {
      // 已发布目录引用时会拒绝停用：保留原状态并说明原因。
      message.error(error instanceof Error ? error.message : '状态更新失败');
    }
  };

  if (loading) {
    return <LoadingSkeleton type="table" rows={8} columns={3} />;
  }

  return (
    <div className="space-y-6">
      <Card>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <Title level={2} className="mb-1">
              工单分类（CTI）
            </Title>
            <Text type="secondary">
              维护工单的三级业务分类，供工单录入、统计及处理规则引用。分派、SLA 与流程规则由各自模块维护。
            </Text>
          </div>
          <Button
            type="primary"
            icon={<Plus size={16} />}
            onClick={() => setEditor({ mode: 'create', category: null, lockedParent: null })}
          >
            创建一级分类
          </Button>
        </div>
      </Card>

      <Row gutter={16}>
        <Col xs={24} md={9} className={mobilePane === 'tree' ? 'block' : 'hidden md:block'}>
          <Card title="分类树">
            <CategoryTreePanel
              categories={tree}
              loading={loading}
              error={loadError}
              selectedId={selectedId}
              onSelect={category => {
                setSelectedId(category.id);
                setMobilePane('details');
              }}
              onAddChild={parent => setEditor({ mode: 'create', category: null, lockedParent: parent })}
              onRetry={() => void loadCategories()}
            />
          </Card>
        </Col>
        <Col xs={24} md={15} className={mobilePane === 'details' ? 'block' : 'hidden md:block'}>
          <Card title="分类详情">
            <CategoryDetailsPanel
              category={selected}
              categories={tree}
              departments={departments}
              onEdit={category => setEditor({ mode: 'edit', category, lockedParent: null })}
              onAddChild={parent => setEditor({ mode: 'create', category: null, lockedParent: parent })}
              onCopy={category => void handleCopy(category)}
              onDelete={handleDelete}
              onToggleStatus={(category, checked) => void handleToggleStatus(category, checked)}
              onBackToList={() => setMobilePane('tree')}
            />
          </Card>
        </Col>
      </Row>

      {editor ? (
        <CategoryEditor
          open
          mode={editor.mode}
          category={editor.category}
          lockedParent={editor.lockedParent}
          categories={tree}
          departments={departments}
          saving={saving}
          onSubmit={handleSubmit}
          onCancel={() => setEditor(null)}
        />
      ) : null}
    </div>
  );
};

/** 就地更新状态，避免一次切换就整树重载。 */
function updateTreeStatus(nodes: TicketCategory[], id: number, isActive: boolean): TicketCategory[] {
  return nodes.map(node => ({
    ...node,
    isActive: node.id === id ? isActive : node.isActive,
    children: node.children ? updateTreeStatus(node.children, id, isActive) : undefined,
  }));
}

export default TicketCategoryManagementPage;
