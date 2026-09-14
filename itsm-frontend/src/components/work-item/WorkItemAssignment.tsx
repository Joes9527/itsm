'use client';
import React, { useRef, useState } from 'react';
import { Alert, Button, Modal, Space } from 'antd';

export type AssignmentInput = {
  assigneeId: number;
  reason: string;
  version: number;
  operationId: string;
};
export type AssignmentSnapshot = {
  version: number;
  currentAssigneeId?: number;
  allowed: boolean;
  disabledReason?: string;
};
export type AssignmentProps = AssignmentSnapshot & {
  candidates: Array<{ id: number; label: string }>;
  submit: (input: AssignmentInput) => Promise<void>;
  refresh: () => Promise<AssignmentSnapshot>;
};

export function WorkItemAssignment(props: AssignmentProps) {
  const [open, setOpen] = useState(false);
  const [observed, setObserved] = useState<AssignmentSnapshot>(props);
  const [target, setTarget] = useState('');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [fresh, setFresh] = useState<AssignmentSnapshot>();
  const intent = useRef<{ payload: string; operationId: string } | undefined>(undefined);
  const inFlight = useRef(false);
  const begin = () => {
    if (!open && !intent.current) {
      setObserved(props);
      setError('');
      setConflict(false);
      setFresh(undefined);
    }
    setOpen(true);
  };
  const submit = async () => {
    if (inFlight.current) return;
    const payload = JSON.stringify({
      assigneeId: Number(target),
      reason: reason.trim(),
      version: observed.version,
    });
    if (!intent.current || intent.current.payload !== payload)
      intent.current = {
        payload,
        operationId: Array.from(crypto.getRandomValues(new Uint8Array(16)), byte =>
          byte.toString(16).padStart(2, '0')
        ).join(''),
      };
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      await props.submit({
        assigneeId: Number(target),
        reason: reason.trim(),
        version: observed.version,
        operationId: intent.current.operationId,
      });
      intent.current = undefined;
      setOpen(false);
      setTarget('');
      setReason('');
      setConflict(false);
    } catch (error) {
      if ((error as { status?: number }).status === 409) {
        setConflict(true);
        setFresh(undefined);
        setError('记录已更新，请刷新并确认最新版本后再提交。');
      } else setError(error instanceof Error ? error.message : '提交失败，重试将沿用本次请求标识');
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  const refresh = async () => {
    setBusy(true);
    try {
      setFresh(await props.refresh());
      setError('已获取最新记录，请确认后继续。');
    } catch (error) {
      setError(error instanceof Error ? error.message : '刷新失败');
    } finally {
      setBusy(false);
    }
  };
  const blocked =
    !props.allowed ||
    !observed.allowed ||
    !target ||
    Number(target) === observed.currentAssigneeId ||
    (Boolean(observed.currentAssigneeId) && !reason.trim()) ||
    conflict ||
    busy;
  return (
    <>
      <Button data-testid='workitem-assignment-open' disabled={!props.allowed} onClick={begin}>
        转派负责人
      </Button>
      {!props.allowed && <span>{props.disabledReason || '当前不可转派'}</span>}
      <Modal title='转派负责人' open={open} onCancel={() => setOpen(false)} footer={null}>
        <Space orientation='vertical' style={{ width: '100%' }}>
          <label>
            负责人
            <select
              aria-label='负责人'
              data-testid='workitem-assignment-assignee'
              value={target}
              disabled={busy}
              onChange={e => setTarget(e.target.value)}
              style={{ display: 'block', width: '100%', padding: 8 }}
            >
              <option value=''>请选择负责人</option>
              {props.candidates.map(c => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            转派原因
            <textarea
              aria-label='转派原因'
              data-testid='workitem-assignment-reason'
              value={reason}
              disabled={busy}
              onChange={e => setReason(e.target.value)}
              rows={3}
              style={{ display: 'block', width: '100%' }}
            />
          </label>
          <span>观察版本：{observed.version}</span>
          {error && (
            <Alert
              data-testid={conflict ? 'workitem-assignment-conflict' : 'workitem-assignment-error'}
              type='warning'
              title={error}
            />
          )}
          {conflict && (
            <Button data-testid='workitem-assignment-refresh' disabled={busy} onClick={refresh}>
              刷新最新记录
            </Button>
          )}
          {fresh && (
            <Button
              data-testid='workitem-assignment-confirm'
              disabled={busy}
              onClick={() => {
                setObserved(fresh);
                setFresh(undefined);
                intent.current = undefined;
                setConflict(false);
                setError('已确认最新版本，请检查目标和原因后提交。');
              }}
            >
              确认最新版本并继续
            </Button>
          )}
          {!observed.allowed && <span>{observed.disabledReason || '当前不可转派'}</span>}
          <Button
            type='primary'
            data-testid='workitem-assignment-submit'
            disabled={blocked}
            loading={busy}
            onClick={submit}
          >
            确认转派
          </Button>
        </Space>
      </Modal>
    </>
  );
}
