'use client';

import React, { createContext, useContext, useEffect, useMemo, useState } from 'react';
import type { UserTask } from '@/lib/api/bpmn-workflow-api';

type Store = {
  owner?: string;
  tasks?: UserTask[];
  publish: (owner: string, tasks?: UserTask[]) => void;
};

const Context = createContext<Store | undefined>(undefined);

/**
 * 详情页只读取一次流程任务（TicketProcessTasks 是唯一读取方），需要解释"为什么没有审批决策"
 * 的面板复用这一份结果。不新开第二次读取：同一个接口、同一张工单再拉一遍只会让两块面板
 * 有机会显示互相矛盾的状态，也会让每次刷新的请求数翻倍。
 *
 * 没有 Provider 时发布是空操作，TicketProcessTasks 单独渲染的用法（例如它自己的测试）不受影响。
 */
export function WorkItemProcessTasksProvider({ children }: { children: React.ReactNode }) {
  const [published, setPublished] = useState<{ owner: string; tasks?: UserTask[] }>();
  const publish = useMemo<Store['publish']>(
    () => (owner, tasks) =>
      setPublished(previous =>
        previous?.owner === owner && previous.tasks === tasks ? previous : { owner, tasks }
      ),
    []
  );
  const value = useMemo<Store>(
    () => ({ owner: published?.owner, tasks: published?.tasks, publish }),
    [published, publish]
  );
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

/**
 * 读取方发布自己已确认的结果。未就绪、读取失败或为空时发布 undefined——上层据此退回
 * 原有的空态文案，而不是基于读不到的数据编造原因。
 */
export function usePublishWorkItemProcessTasks(owner: string, tasks: UserTask[] | undefined): void {
  const publish = useContext(Context)?.publish;
  useEffect(() => {
    if (publish) publish(owner, tasks);
  }, [publish, owner, tasks]);
  // 卸载（含会话或工单切换导致的重新挂载）时必须撤回，否则新页面的空态会引用旧工单的任务。
  useEffect(() => {
    if (!publish) return;
    return () => publish(owner, undefined);
  }, [publish, owner]);
}

/** 读取方已确认的任务；未读取、读取失败或未挂载读取方时为 undefined。 */
export function usePublishedWorkItemProcessTasks(): UserTask[] | undefined {
  return useContext(Context)?.tasks;
}
