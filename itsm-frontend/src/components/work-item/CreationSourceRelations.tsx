'use client';
import React, { forwardRef, useEffect, useImperativeHandle, useState } from 'react';
import { Alert, Button, Space } from 'antd';
import {
  WorkItemRelationsApi,
  type SourceRelation,
  type RelationEndpoint,
} from '@/lib/api/workitem-relations';
import { relationLabels } from './WorkItemRelations';
export interface CreationSourceRelationsHandle {
  refresh: () => Promise<boolean>;
  validate: () => void;
}
interface Props {
  value?: SourceRelation[];
  onChange?: (value: SourceRelation[]) => void;
}
// The form owns the typed creation input. Each source is explicitly read and
// observed before confirmation; intake creates the target and links atomically.
export const CreationSourceRelations = forwardRef<CreationSourceRelationsHandle, Props>(
  function CreationSourceRelations({ value = [], onChange }, ref) {
    const [sourceId, setSourceId] = useState('');
    const [sources, setSources] = useState<Record<number, RelationEndpoint>>({});
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);
    const [type, setType] = useState<SourceRelation['relationType']>('resolved_by_change');
    const [required, setRequired] = useState(false);
    const read = async (id: number) => {
      const result = await WorkItemRelationsApi.context(id);
      if (!result.mutation.allowed)
        throw new Error(result.mutation.reason || '当前账号不能修改源记录关联');
      return result.source;
    };
    const refresh = async () => {
      setBusy(true);
      try {
        const observed = await Promise.all(value.map(row => read(row.sourceWorkItemId)));
        setSources(Object.fromEntries(observed.map(row => [row.workItemId, row])));
        onChange?.(
          value.map((row, index) => ({ ...row, expectedVersion: observed[index].version }))
        );
        setError('');
        return true;
      } catch (e) {
        setError(e instanceof Error ? e.message : '源记录刷新失败');
        return false;
      } finally {
        setBusy(false);
      }
    };
    useImperativeHandle(ref, () => ({
      refresh,
      validate: () => {
        if (busy || error || sourceId.trim())
          throw new Error(error || '请先读取并添加源记录，或清空源 WorkItem ID');
        const requested = new URLSearchParams(window.location.search).get('sourceWorkItemId');
        if (requested && !value.some(row => row.sourceWorkItemId === Number(requested)))
          throw new Error('请先读取并添加指定源记录，创建时将原子绑定关联');
      },
    }));
    useEffect(() => {
      const id = new URLSearchParams(window.location.search).get('sourceWorkItemId');
      if (id) setSourceId(id);
    }, []);
    const add = async () => {
      setBusy(true);
      try {
        const id = Number(sourceId);
        if (!Number.isSafeInteger(id) || id <= 0) throw new Error('请输入有效的源 WorkItem ID');
        if (value.some(row => row.sourceWorkItemId === id))
          throw new Error('同一源记录只能添加一次');
        const source = await read(id);
        setSources(current => ({ ...current, [id]: source }));
        onChange?.([
          ...value,
          {
            sourceWorkItemId: id,
            relationType: type,
            expectedVersion: source.version,
            ...(type === 'resolved_by_change' ? { metadata: { required } } : {}),
          },
        ]);
        setSourceId('');
        setError('');
      } catch (e) {
        setError(e instanceof Error ? e.message : '源记录读取失败');
      } finally {
        setBusy(false);
      }
    };
    return (
      <Space orientation='vertical' style={{ width: '100%' }}>
        <p>创建目标及关联在同一事务提交；源记录和历史保留。先读取源记录，再确认创建申请。</p>
        {error && <Alert type='error' title={error} />}
        {value.map(row => (
          <div key={row.sourceWorkItemId}>
            {sources[row.sourceWorkItemId]?.number} · WorkItem {row.sourceWorkItemId} · 版本{' '}
            {row.expectedVersion} → 新建变更 · {relationLabels[row.relationType]}{' '}
            {row.metadata?.required ? '（必需）' : ''}
            <Button
              disabled={busy}
              onClick={() =>
                onChange?.(value.filter(item => item.sourceWorkItemId !== row.sourceWorkItemId))
              }
            >
              移除源记录
            </Button>
          </div>
        ))}
        <label>
          源 WorkItem ID{' '}
          <input
            aria-label='源 WorkItem ID'
            type='number'
            value={sourceId}
            onChange={e => setSourceId(e.target.value)}
          />
        </label>
        <label>
          创建关系类型{' '}
          <select
            aria-label='创建关系类型'
            value={type}
            onChange={e => {
              setType(e.target.value as SourceRelation['relationType']);
              setRequired(false);
            }}
          >
            {Object.entries(relationLabels).map(([key, label]) => (
              <option key={key} value={key}>
                {label}（{key}）
              </option>
            ))}
          </select>
        </label>
        {type === 'resolved_by_change' && (
          <label>
            <input
              type='checkbox'
              checked={required}
              onChange={e => setRequired(e.target.checked)}
            />
            必须验证的变更依赖
          </label>
        )}
        <Button disabled={busy} onClick={() => void add()}>
          读取并添加源记录
        </Button>
        <Button disabled={busy || value.length === 0} onClick={() => void refresh()}>
          刷新源版本（保留表单）
        </Button>
      </Space>
    );
  }
);
