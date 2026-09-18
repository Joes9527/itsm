'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Empty, Pagination, Skeleton, Space, Tag, Typography } from 'antd';
import { SyncOutlined } from '@ant-design/icons';
import Link from 'next/link';
import {
  CTIReferenceGroup,
  CTIReferenceView,
  TicketCategoryApi,
} from '@/lib/api/ticket-category-api';

/**
 * CategoryReferencesPanel：分类详情"引用"页签。
 *
 * 契约边界（与后端一致）：
 * - blocking 来自不受 RBAC 过滤的真实扫描，为真时必须阻止删除/移动；
 * - 无权查看某类型明细时后端不返回数量与名称，界面只显示"存在引用"，
 *   **不得**用 0 或占位数字伪装；
 * - 查询失败必须明确报错并允许重试，不能把失败渲染成"无引用"；
 * - 每种引用类型给出管理入口（见 KINDS 的 href），便于从引用跳到维护对象。
 */

interface KindMeta {
  label: string;
  href: string;
  hint?: string;
}

const KINDS: Record<string, KindMeta> = {
  catalog: { label: '服务目录', href: '/admin/service-catalogs' },
  sla_definition: { label: 'SLA 定义', href: '/admin/sla-definitions' },
  assignment_rule: { label: '分派规则', href: '/admin/tickets/assignment-rules' },
  automation_rule: { label: '自动化规则', href: '/admin/tickets/automation-rules' },
  ticket_template: { label: '工单模板', href: '/tickets/templates' },
  process_binding: { label: '流程绑定', href: '/admin/process-routing' },
  incident_escalation_rule: {
    label: '事件升级规则（历史字符串引用）',
    href: '/admin/escalation-rules',
    hint: '该类型以字符串匹配分类，无法映射为 ID；命中即视为引用，需由配置所有者迁移到结构化引用',
  },
  work_item: { label: '工单引用', href: '/tickets' },
};

const DEFAULT_PAGE_SIZE = 20;

export interface CategoryReferencesPanelProps {
  categoryId: number;
  /** 测试与嵌入式场景可注入，不传则调用真实 API。 */
  loadReferences?: (id: number, params?: { page?: number; pageSize?: number }) => Promise<CTIReferenceView>;
}

export function CategoryReferencesPanel({ categoryId, loadReferences }: CategoryReferencesPanelProps) {
  const loader = loadReferences ?? TicketCategoryApi.getCategoryReferences;
  const [view, setView] = useState<CTIReferenceView | null>(null);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await loader(categoryId, { page, pageSize: DEFAULT_PAGE_SIZE });
      setView(result);
    } catch (cause) {
      // 失败必须显式暴露，并把已有结果清空，避免旧数据被误读为当前结果。
      setView(null);
      setError(cause instanceof Error && cause.message ? cause.message : '引用查询失败');
    } finally {
      setLoading(false);
    }
  }, [categoryId, loader, page]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading && !view) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }
  if (error) {
    return (
      <Alert
        type="error"
        showIcon
        message="引用查询失败"
        description={error}
        action={
          <Button size="small" icon={<SyncOutlined aria-hidden="true" />} onClick={() => void load()}>
            重试
          </Button>
        }
      />
    );
  }
  if (!view) {
    return <Empty description="暂无引用信息" />;
  }

  const referencedGroups = view.groups.filter(group => group.referenced);
  const hiddenCount = referencedGroups.filter(group => !group.visible).length;
  const paginatedKinds = referencedGroups.filter(
    group => group.visible && (group.total ?? 0) > view.pageSize,
  );

  return (
    <Space direction="vertical" className="w-full" size="middle">
      <Alert
        type={view.blocking ? 'warning' : 'success'}
        showIcon
        message={view.blocking ? '该分类（或其下级）已被引用，不可删除或移动' : '当前没有结构性引用'}
        description={
          <Typography.Text type="secondary" className="text-[12px]">
            引用范围包含该分类的整棵子树；计数与名称按当前账号权限过滤，
            被引用保护始终按真实引用判定。
          </Typography.Text>
        }
      />

      {hiddenCount > 0 ? (
        <Typography.Text type="secondary" data-testid="cti-references-hidden-notice">
          有 {hiddenCount} 类引用对象超出你的查看权限，仅显示"存在引用"；请联系对应模块管理员查看明细。
        </Typography.Text>
      ) : null}

      {referencedGroups.length === 0 ? (
        <Empty description="该分类及下级暂无引用" />
      ) : (
        <div className="space-y-3">
          {referencedGroups.map(group => {
            const meta = KINDS[group.kind] ?? { label: group.kind, href: '' };
            return (
              <div key={group.kind} className="rounded border border-neutral-200 p-3" data-testid={`cti-reference-${group.kind}`}>
                <Space wrap className="mb-1">
                  <Typography.Text strong>{meta.label}</Typography.Text>
                  {group.visible ? (
                    <Tag color="blue">共 {group.total ?? 0} 项引用</Tag>
                  ) : (
                    <Tag data-testid={`cti-reference-hidden-${group.kind}`}>存在引用（无权查看明细）</Tag>
                  )}
                  {meta.href ? (
                    <Link href={meta.href} className="text-[12px]">
                      前往管理
                    </Link>
                  ) : null}
                </Space>
                {meta.hint ? (
                  <Typography.Paragraph type="secondary" className="mb-1 text-[12px]">
                    {meta.hint}
                  </Typography.Paragraph>
                ) : null}
                {group.visible && group.kind !== 'work_item' ? (
                  <ul className="mb-0 pl-5">
                    {(group.items ?? []).map(item => (
                      <li key={item.id}>{item.name}</li>
                    ))}
                  </ul>
                ) : null}
                {group.visible && group.kind === 'work_item' ? (
                  <Typography.Text type="secondary" className="text-[12px]">
                    工单明细请到工单列表按分类筛选查看（此处只提供计数，避免泄露不可见工单内容）。
                  </Typography.Text>
                ) : null}
              </div>
            );
          })}
        </div>
      )}

      {paginatedKinds.length > 0 ? (
        <Pagination
          size="small"
          current={view.page}
          pageSize={view.pageSize}
          total={Math.max(...paginatedKinds.map(group => group.total ?? 0))}
          showSizeChanger={false}
          onChange={setPage}
        />
      ) : null}
    </Space>
  );
}

export default CategoryReferencesPanel;

/** 供列表页/详情页复用的类型判断：是否存在需要阻止维护动作的引用。 */
export function blocksMaintenance(view: CTIReferenceView | null): boolean {
  return view?.blocking ?? false;
}

export type { CTIReferenceGroup };
