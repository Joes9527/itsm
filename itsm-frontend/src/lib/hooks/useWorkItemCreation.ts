'use client';

import { useRef, useState } from 'react';
import { message } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
import {
  creationReceiptMessage,
  type CreationRequestOptions,
  type CreateWorkItemResult,
} from '@/lib/api/work-item-creation';

type Attempt = {
  key: string;
  context: string;
  title: string;
  state: 'sending' | 'unknown' | 'rejected' | 'committed';
  error?: string;
  receipt?: CreateWorkItemResult;
  send: (options: CreationRequestOptions) => Promise<CreateWorkItemResult>;
  onCommitted?: (receipt: CreateWorkItemResult) => void;
};
function submissionContext(): string {
  const { user, currentTenant, isAuthenticated } = useAuthStore.getState();
  if (!isAuthenticated || !user?.id || !currentTenant?.id)
    throw new Error('请登录并确认当前租户后提交');
  return `${user.id}:${currentTenant.id}`;
}

// Owns confirmed attempts only; the owning Form remains the editable draft authority.
export function useWorkItemCreation() {
  const attemptsRef = useRef<Attempt[]>([]);
  const activeRef = useRef<Attempt | undefined>(undefined);
  const busyRef = useRef(false);
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const publish = () => setAttempts(attemptsRef.current.map(attempt => ({ ...attempt })));

  const run = async (attempt: Attempt): Promise<CreateWorkItemResult | undefined> => {
    if (busyRef.current || attempt.state === 'committed') return undefined;
    busyRef.current = true;
    const hadUnknownOutcome = attempt.state === 'unknown';
    const assertSubmissionContext = () => {
      if (submissionContext() !== attempt.context)
        throw new Error('账号或租户已切换，请重新确认当前申请；原申请仍可在原上下文重试');
    };
    try {
      assertSubmissionContext();
      attempt.state = 'sending';
      publish();
      const receipt = await attempt.send({ idempotencyKey: attempt.key, assertSubmissionContext });
      attempt.receipt = receipt;
      attempt.state = 'committed';
      if (activeRef.current === attempt) activeRef.current = undefined;
      message.success(creationReceiptMessage(receipt));
      // Receipt is authoritative even when subsequent navigation/detail handling fails.
      try {
        assertSubmissionContext();
        attempt.onCommitted?.(receipt);
      } catch (error) {
        message.warning(error instanceof Error ? error.message : '已创建，详情暂不可用');
      }
      return receipt;
    } catch (error) {
      attempt.state =
        !hadUnknownOutcome && error instanceof ApiError && error.status >= 400 && error.status < 500 && !error.retryable
          ? 'rejected'
          : 'unknown';
      attempt.error = error instanceof Error ? error.message : '提交结果未知';
      message.error(attempt.error);
      return undefined;
    } finally {
      busyRef.current = false;
      publish();
    }
  };
  const submit = async <T>(
    payload: T,
    send: (payload: T, options: CreationRequestOptions) => Promise<CreateWorkItemResult>,
    onCommitted?: (receipt: CreateWorkItemResult) => void
  ) => {
    if (busyRef.current) return undefined;
    if (activeRef.current) return run(activeRef.current);
    let context: string;
    try {
      context = submissionContext();
    } catch (error) {
      message.error((error as Error).message);
      return undefined;
    }
    // JSON is the actual wire representation, including ISO dates and omitted undefineds.
    const serialized = JSON.stringify(payload);
    const snapshot = JSON.parse(serialized) as { title?: string };
    const attempt: Attempt = {
      key: crypto.randomUUID(),
      context,
      title: snapshot.title || '已确认申请',
      state: 'sending',
      send: options => send(JSON.parse(serialized) as T, options),
      onCommitted,
    };
    activeRef.current = attempt;
    attemptsRef.current.push(attempt);
    return run(attempt);
  };
  const retry = async (key: string) => {
    const attempt = attemptsRef.current.find(candidate => candidate.key === key);
    if (attempt) return run(attempt);
  };
  const newConfirmation = () => {
    if (!busyRef.current) {
      activeRef.current = undefined;
      publish();
    }
  };
  return {
    submit,
    retry,
    newConfirmation,
    attempts,
    submitting: attempts.some(attempt => attempt.state === 'sending'),
  };
}
export type WorkItemCreation = ReturnType<typeof useWorkItemCreation>;
