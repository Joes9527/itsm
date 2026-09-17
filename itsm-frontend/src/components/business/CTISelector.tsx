'use client';

/**
 * CTISelector：工单分类（CTI）的唯一值契约选择器。
 *
 * 约定（与后端 `service/ticket_category_policy.go` 一致）：
 * - value 是**所选最深节点 ID**（null = 未分类），前端不维护第二套 C/T/I 文本权威；
 * - 最多三级，完整路径由树派生；
 * - `requiredDepth=0`：普通报障允许未分类或部分分类；
 * - `requiredDepth=3`：目录发布/申请等场景要求完整三级，缺级时给出明确提示。
 *
 * 用户报障、坐席补齐、管理端配置复用同一个组件，仅通过 props 决定展示深度。
 */

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, Button, Cascader, Space, Typography } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import {
  CTI_COMPLETE_LEVEL,
  TicketCategoryApi,
  type TicketCategory,
} from '@/lib/api/ticket-category-api';

export const CTI_INCOMPLETE_MESSAGE = '请选择完整三级分类';

export interface CTISelectorProps {
  /** 所选最深节点 ID；null/undefined 表示未分类（可直接放入 antd Form.Item）。 */
  value?: number | null;
  onChange?: (value: number | null) => void;
  /** 0 = 允许未分类/部分分类；3 = 要求完整三级。 */
  requiredDepth: 0 | 3;
  disabled?: boolean;
  id?: string;
  placeholder?: string;
  /** 是否允许清除选择（普通报障允许“不确定”）。 */
  allowClear?: boolean;
  /** 选择是否构成完整三级（供调用方禁用提交按钮）。 */
  onValidityChange?: (valid: boolean) => void;
  /**
   * 暴露加载状态与分类树，供需要把「最深节点」转换为其它历史表示的适配层使用。
   * 业务组件不应自行再请求一次分类树，避免同一视图出现两份分类权威。
   */
  onStateChange?: (state: { categories: TicketCategory[]; loading: boolean; error: string }) => void;
}

interface CascaderOption {
  value: number;
  label: string;
  disabled?: boolean;
  children?: CascaderOption[];
}

/** 把分类树转换为 Cascader 选项；停用节点已在数据层被过滤，这里只做投影。 */
export function toCTIOptions(nodes: TicketCategory[] | undefined): CascaderOption[] {
  if (!nodes || nodes.length === 0) return [];
  return nodes.map(node => {
    const children = toCTIOptions(node.children);
    return {
      value: node.id,
      label: node.name,
      children: children.length > 0 ? children : undefined,
    };
  });
}

/** 收集分类树中每个节点的根→自身路径 ID。 */
export function collectCTIPaths(
  nodes: TicketCategory[] | undefined,
  parents: number[] = [],
  into: Map<number, number[]> = new Map()
): Map<number, number[]> {
  (nodes ?? []).forEach(node => {
    const path = [...parents, node.id];
    into.set(node.id, path);
    collectCTIPaths(node.children, path, into);
  });
  return into;
}

/** 路径 ID 是否构成要求的深度。 */
export function isCTIComplete(pathIds: number[] | undefined, requiredDepth: 0 | 3): boolean {
  if (requiredDepth === 0) return true;
  return (pathIds?.length ?? 0) >= CTI_COMPLETE_LEVEL;
}

export function CTISelector({
  value,
  onChange,
  requiredDepth,
  disabled,
  id,
  placeholder,
  allowClear,
  onValidityChange,
  onStateChange,
}: CTISelectorProps) {
  const tenantId = useAuthStore(state => state.currentTenant?.id);
  const [categories, setCategories] = useState<TicketCategory[] | undefined>(undefined);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    if (!tenantId) {
      // 未选择租户时不发起请求，也不把“无租户”伪装成空分类。
      setCategories(undefined);
      setError('');
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError('');
    TicketCategoryApi.getCategoryTree()
      .then(rows => {
        if (!cancelled) setCategories(Array.isArray(rows) ? rows : []);
      })
      .catch(() => {
        // 异步失败必须显式暴露，不能显示成“暂无分类”的空结果。
        if (!cancelled) setError('分类加载失败，请重试或确认拥有工单分类读取权限');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [tenantId, revision]);

  const paths = useMemo(() => collectCTIPaths(categories), [categories]);
  const selectedPath = value && paths.has(value) ? paths.get(value) : undefined;
  const complete = isCTIComplete(selectedPath, requiredDepth);

  useEffect(() => {
    onValidityChange?.(complete);
  }, [complete, onValidityChange]);

  useEffect(() => {
    onStateChange?.({ categories: categories ?? [], loading, error });
  }, [categories, loading, error, onStateChange]);

  const handleChange = useCallback(
    (path?: (number | string)[]) => {
      if (!path || path.length === 0) {
        onChange?.(null);
        return;
      }
      const deepest = Number(path[path.length - 1]);
      onChange?.(Number.isFinite(deepest) ? deepest : null);
    },
    [onChange]
  );

  const incomplete = requiredDepth === CTI_COMPLETE_LEVEL && !complete;
  const notice = placeholder ?? (requiredDepth === 0 ? '选择分类（可不确定）' : '选择完整三级分类');

  return (
    <Space orientation="vertical" style={{ width: '100%' }}>
      <Cascader
        id={id}
        value={selectedPath}
        onChange={handleChange}
        options={toCTIOptions(categories)}
        placeholder={notice}
        changeOnSelect
        allowClear={allowClear ?? requiredDepth === 0}
        showSearch
        style={{ width: '100%' }}
        loading={loading}
        disabled={disabled || !!error || !tenantId}
        notFoundContent={loading ? '加载中…' : '暂无可用分类'}
        aria-invalid={incomplete || undefined}
        data-cti-complete={complete ? 'true' : 'false'}
      />
      {error ? (
        <Alert
          type="error"
          role="alert"
          message={error}
          action={<Button onClick={() => setRevision(current => current + 1)}>重试</Button>}
        />
      ) : null}
      {incomplete ? (
        <Typography.Text type="danger" role="alert" data-testid="cti-incomplete">
          {CTI_INCOMPLETE_MESSAGE}
        </Typography.Text>
      ) : null}
    </Space>
  );
}

export default CTISelector;
