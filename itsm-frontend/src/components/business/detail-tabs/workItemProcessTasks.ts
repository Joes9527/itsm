import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';

// Wire identity mapping only; lifecycle and authorization remain in BPMN.
const businessTypes: Record<string, string> = {
  generic: 'generic', service_request_item: 'service_request_item', incident: 'incident',
  problem: 'problem', change_request: 'change_request', catalog_task: 'catalog_task',
};

export const terminalTaskStatuses = new Set(['completed', 'cancelled']);

/**
 * 工单流程任务的唯一读取入口。审批决策的空态说明与「当前流程任务」面板必须看到同一份
 * 数据，否则会出现"任务面板显示卡在派单、审批 Tab 却说没有审批节点"这类互相矛盾的提示。
 * 分页校验保持严格：任何一页不完整都抛错，不允许用截断的结果去解释空态。
 */
export async function readWorkItemProcessTasks(ticketId: number, recordClass: string): Promise<UserTask[]> {
  const businessType = businessTypes[recordClass];
  if (!businessType) throw new Error('暂不支持此工单类型的流程任务查询');
  const tasks: UserTask[] = [];
  const seen = new Set<number>();
  const pageSize = 100;
  for (let page = 1; ; page++) {
    const result = await BPMNWorkflowApi.listUserTasks({ businessType, businessId: ticketId, page, pageSize });
    if (!Array.isArray(result.items) || !Number.isSafeInteger(result.total) || result.total < 0 ||
        result.page !== page || result.pageSize !== pageSize) throw new Error('任务分页响应异常，请重试');
    const expected = Math.max(0, Math.min(pageSize, result.total - (page - 1) * pageSize));
    if (result.items.length !== expected) throw new Error('任务分页不完整，请重试');
    for (const task of result.items) {
      if (task.businessType !== businessType || task.businessId !== ticketId) throw new Error('任务关联不一致，请重试');
      if (!Number.isSafeInteger(task.id) || task.id <= 0 || seen.has(task.id)) throw new Error('任务分页重复或无效，请重试');
      seen.add(task.id);
      tasks.push(task);
    }
    if (page * pageSize >= result.total) return tasks;
  }
}
