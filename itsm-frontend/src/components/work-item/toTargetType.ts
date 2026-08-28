import type { TargetType } from '@/components/business/detail-tabs';
import type { WorkItemCommon } from './WorkItemTypes';

// toTargetType 把 WorkItem 的 recordClass 映射到 detail-tabs 通用组件（CommentPanel/
// AttachmentPanel）用的 TargetType。与后端 middleware.resourceForRecordClass
// （itsm-backend/middleware/workitem_rbac.go）刻意保持同一组映射规则：incident/problem/
// change_request 三个专业域各自对应，其余 recordClass 统一落到 "ticket"。
export function toTargetType(recordClass: WorkItemCommon['recordClass']): TargetType {
  switch (recordClass) {
    case 'incident':
      return 'incident';
    case 'problem':
      return 'problem';
    case 'change_request':
      return 'change';
    default:
      return 'ticket';
  }
}
