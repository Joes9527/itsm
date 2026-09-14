'use client';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Space, Tag } from 'antd';
import {
  WorkItemRelationsApi,
  type RelationCommand,
  type RelationContext,
  type RelationReceipt,
  type RelationType,
  type RelationView,
} from '@/lib/api/workitem-relations';
import { useAuthStore } from '@/lib/store/auth-store';

export const relationLabels: Record<RelationType, string> = {
  related_to: '相关',
  investigated_by: '由问题调查',
  resolved_by_change: '由变更解决',
  caused_by: '由问题引起',
  requested_change: '请求变更',
  fulfilled_by: '由任务交付',
  duplicate_of: '重复',
  parent_child: '父子',
};
const errorText = (error: unknown) => (error instanceof Error ? error.message : '请求失败');
function actorContext() {
  const { user, currentTenant, isAuthenticated } = useAuthStore.getState();
  if (!isAuthenticated || !user?.id || !currentTenant?.id) throw new Error('请登录并确认当前租户');
  return `${user.id}:${currentTenant.id}`;
}
type Attempt = { body: RelationCommand; remove: boolean; actor: string };

export function WorkItemRelations({ workItemId }: { workItemId: number }) {
  const [context, setContext] = useState<RelationContext>();
  const [relations, setRelations] = useState<RelationView[]>();
  const [readError, setReadError] = useState('');
  const [effectError, setEffectError] = useState('');
  const [receipt, setReceipt] = useState<{ result: RelationReceipt; remove: boolean }>();
  const [targetId, setTargetId] = useState('');
  const [type, setType] = useState<RelationType>('related_to');
  const [required, setRequired] = useState(false);
  const [busy, setBusy] = useState(false);
  const [preparing, setPreparing] = useState<{ row: RelationView; context: RelationContext }>();
  const [attempt, setAttempt] = useState<Attempt>();
  const [unconfirmed, setUnconfirmed] = useState<Attempt[]>([]);
  const inFlight = useRef(false);
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    try {
      const [nextContext, nextRelations] = await Promise.all([
        WorkItemRelationsApi.context(workItemId),
        WorkItemRelationsApi.list(workItemId),
      ]);
      if (current !== generation.current) return false;
      setContext(nextContext);
      setRelations(nextRelations);
      setReadError('');
      return true;
    } catch (error) {
      if (current === generation.current)
        setReadError(`刷新失败：${errorText(error)}；之前显示的数据可能已过期`);
      return false;
    }
  }, [workItemId]);
  useEffect(() => {
    setContext(undefined);
    setRelations(undefined);
    setAttempt(undefined);
    setUnconfirmed([]);
    setReceipt(undefined);
    void refresh();
    return () => {
      generation.current++;
    };
  }, [refresh]);
  const run = async (operation: Attempt) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setEffectError('');
    try {
      const assertContext = () => {
        if (actorContext() !== operation.actor)
          throw new Error('账号或租户已切换，请回到原上下文重试或重新确认');
      };
      assertContext();
      const result = await (operation.remove
        ? WorkItemRelationsApi.remove(operation.body, assertContext)
        : WorkItemRelationsApi.add(operation.body, assertContext));
      setReceipt({ result, remove: operation.remove });
      setAttempt(current =>
        current?.body.operationId === operation.body.operationId ? undefined : current
      );
      setUnconfirmed(current =>
        current.filter(item => item.body.operationId !== operation.body.operationId)
      );
      setPreparing(undefined);
      await refresh();
    } catch (error) {
      setEffectError(errorText(error));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  const confirm = (remove: boolean) => {
    try {
      const source = remove ? preparing?.context : context;
      if (!source?.mutation.allowed || readError)
        throw new Error(source?.mutation.reason || '请先刷新当前关联权限');
      const row = preparing?.row;
      const target =
        remove && row
          ? row.source.workItemId === source.source.workItemId
            ? row.target.workItemId
            : row.source.workItemId
          : Number(targetId);
      if (!Number.isSafeInteger(target) || target <= 0)
        throw new Error('请输入有效的目标 WorkItem ID');
      const relationType = remove && row ? row.relationType : type;
      const body: RelationCommand = {
        sourceWorkItemId: source.source.workItemId,
        targetWorkItemId: target,
        relationType,
        expectedVersion: source.source.version,
        operationId: crypto.randomUUID(),
        ...(relationType === 'resolved_by_change'
          ? { metadata: { required: remove && row ? row.required : required } }
          : {}),
      };
      const operation = { body, remove, actor: actorContext() };
      setAttempt(operation);
      void run(operation);
    } catch (error) {
      setEffectError(errorText(error));
    }
  };
  const prepareRemoval = async (row: RelationView) => {
    setEffectError('');
    try {
      // Directed relations must be removed from their authoritative source. A
      // symmetric relation can use the current endpoint and its observed version.
      const id = row.relationType === 'related_to' ? workItemId : row.source.workItemId;
      const current = await WorkItemRelationsApi.context(id);
      setPreparing({ row, context: current });
    } catch (error) {
      setEffectError(`无法准备解除关联：${errorText(error)}`);
    }
  };
  return (
    <Space orientation='vertical' style={{ width: '100%' }}>
      {context && (
        <p>
          {context.source.number} · WorkItem {context.source.workItemId} · 版本{' '}
          {context.source.version}
        </p>
      )}
      {receipt && (
        <Alert
          type='success'
          title={`${receipt.remove ? '解除关联' : '关联'}已确认 · WorkItem ${receipt.result.workItemId} · 版本 ${receipt.result.version}${receipt.result.replayed ? '（原操作回执）' : ''}`}
        />
      )}
      {readError && <Alert type='error' title={readError} />}
      {effectError && <Alert type='error' title={effectError} />}
      <Button
        disabled={busy}
        onClick={async () => {
          if (await refresh()) {
            if (attempt) setUnconfirmed(current => [...current, attempt]);
            setAttempt(undefined);
            setPreparing(undefined);
            setEffectError('');
          }
        }}
      >
        刷新并重新确认
      </Button>
      {relations?.length === 0 && !readError && <p>暂无关联</p>}
      {relations?.map(row => (
        <div key={row.id}>
          <p>
            {row.source.number}（WorkItem {row.source.workItemId}）
            {row.relationType === 'related_to' ? ' ↔ ' : ' → '}
            {row.target.number}（WorkItem {row.target.workItemId}）
          </p>
          <Tag>
            {relationLabels[row.relationType]} · {row.relationType}
          </Tag>
          {row.relationType === 'resolved_by_change' && (
            <Tag>{row.required ? '必须验证的变更依赖' : '非必需变更依赖'}</Tag>
          )}
          <span>
            {row.source.title} / {row.target.title}
          </span>
          <Button
            disabled={busy || !!attempt || !!readError}
            onClick={() => void prepareRemoval(row)}
          >
            准备解除关联
          </Button>
        </div>
      ))}
      {preparing && (
        <Alert
          type='warning'
          title={`解除 ${preparing.row.source.number} → ${preparing.row.target.number} · 源版本 ${preparing.context.source.version}`}
          description={
            <>
              <p>{preparing.context.mutation.reason}</p>
              <Button
                disabled={busy || !!attempt || !preparing.context.mutation.allowed}
                onClick={() => confirm(true)}
              >
                确认解除关联
              </Button>
            </>
          }
        />
      )}
      {!context?.mutation.allowed && (
        <Alert type='warning' title={context?.mutation.reason || '当前关联操作权限尚未确认'} />
      )}
      <label>
        目标 WorkItem ID{' '}
        <input
          aria-label='目标 WorkItem ID'
          type='number'
          min='1'
          value={targetId}
          onChange={e => setTargetId(e.target.value)}
        />
      </label>
      <label>
        关系类型{' '}
        <select
          aria-label='关系类型'
          value={type}
          onChange={e => {
            setType(e.target.value as RelationType);
            setRequired(false);
          }}
        >
          {Object.entries(relationLabels).map(([value, label]) => (
            <option key={value} value={value}>
              {label}（{value}）
            </option>
          ))}
        </select>
      </label>
      {type === 'resolved_by_change' && (
        <label>
          <input type='checkbox' checked={required} onChange={e => setRequired(e.target.checked)} />
          必须验证的变更依赖
        </label>
      )}
      <p>
        方向为当前记录指向目标；目标读取权限及关系规则由服务器校验。变更关闭不表示变更结果成功。
      </p>
      <Button
        disabled={busy || !!attempt || !!readError || !context?.mutation.allowed}
        onClick={() => confirm(false)}
      >
        确认关联
      </Button>
      {unconfirmed.map(operation => (
        <Alert
          key={operation.body.operationId}
          type='warning'
          title={`先前操作尚未确认：${operation.body.sourceWorkItemId} → ${operation.body.targetWorkItemId} · ${operation.body.relationType} · 源版本 ${operation.body.expectedVersion}`}
          description={
            <Button disabled={busy} onClick={() => void run(operation)}>
              重试先前操作 {operation.body.operationId}
            </Button>
          }
        />
      ))}
      {attempt && (
        <>
          <p>原操作保留已确认目标、源版本与申请标识；编辑不会修改原操作。</p>
          <Button disabled={busy} onClick={() => void run(attempt)}>
            重试原操作
          </Button>
        </>
      )}
      {context?.mutation.allowed && !readError && (
        <Space>
          <a href={`/changes/new?sourceWorkItemId=${workItemId}`}>创建关联变更</a>
        </Space>
      )}
    </Space>
  );
}
