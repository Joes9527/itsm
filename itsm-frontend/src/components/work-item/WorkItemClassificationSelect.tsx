'use client';
import { useCallback, useState } from 'react';
import { Alert } from 'antd';
import { CTISelector } from '@/components/business/CTISelector';
import type { TicketCategory } from '@/lib/api/ticket-category-api';
import { classificationPath } from './classification';

interface Props {
  value?: number[];
  onChange?: (value: number[]) => void;
  initialCategoryId?: number;
  id?: string;
}

/**
 * 工单分类选择的兼容适配层。
 *
 * 选择与校验统一由 `CTISelector` 负责（value = 所选最深节点 ID）；
 * 本组件只把「最深节点」与既有调用方的路径表示（number[]）互相转换，
 * 保持事件/工单详情页现有的 props 契约不变。
 */
export function WorkItemClassificationSelect({ value, onChange, initialCategoryId, id }: Props) {
  const [categories, setCategories] = useState<TicketCategory[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const selectedPath = value ?? classificationPath(initialCategoryId, categories);
  const deepest = selectedPath?.length ? selectedPath[selectedPath.length - 1] : null;
  const unresolvedInitial = Boolean(initialCategoryId) && value === undefined && !selectedPath;

  // 回调引用必须稳定：CTISelector 的通知 effect 以值为依赖，这里再 memo 化可避免
  // 父级等价刷新时产生无谓更新（历史缺陷曾因内联回调 + 新数组形成无限更新环）。
  const handleStateChange = useCallback(
    (state: { categories: TicketCategory[]; loading: boolean; error: string }) => {
      setCategories(prev => (prev === state.categories ? prev : state.categories));
      setLoading(prev => (prev === state.loading ? prev : state.loading));
      setError(prev => (prev === state.error ? prev : state.error));
    },
    []
  );

  const handleChange = useCallback(
    (next: number | null) => {
      if (next === null) {
        onChange?.([]);
        return;
      }
      onChange?.(classificationPath(next, categories) ?? [next]);
    },
    [categories, onChange]
  );

  return (
    <>
      <CTISelector
        id={id}
        value={deepest}
        onChange={handleChange}
        requiredDepth={0}
        allowClear
        onStateChange={handleStateChange}
      />
      {!loading && !error && unresolvedInitial ? (
        <Alert type="warning" message="原分类已停用或不可见；未重新选择时保留原分类" />
      ) : null}
    </>
  );
}

export default WorkItemClassificationSelect;
