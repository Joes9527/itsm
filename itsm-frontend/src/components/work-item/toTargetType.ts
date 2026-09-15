import type { TargetType } from '@/components/business/detail-tabs';
import type { WorkItemCommon } from './WorkItemTypes';

// toTargetType 把 WorkItem 的 recordClass 映射到 detail-tabs 通用组件（CommentPanel/
// AttachmentPanel）用的 TargetType。与后端 middleware.resourceForRecordClass
// 的权限资源不是同一个概念：此函数仅选择展示/适配器目标，Requested Item
// 仍复用 ticket 附件接口；权限资源在 getAttachmentPermissions 中单独处理。
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

// Mirrors the registered backend resource boundary for coarse UI gating only.
// Row scope and ownership are still checked by the attachment service.
export function getAttachmentPermissions(
  recordClass: string | undefined,
  hasPermission: (permission: string) => boolean
) {
  const registered = [
    'generic',
    'service_request_item',
    'incident',
    'problem',
    'change_request',
    'catalog_task',
  ];
  if (!recordClass || !registered.includes(recordClass))
    return { canRead: false, canUpload: false, canDelete: false };
  if (recordClass === 'service_request_item') {
    const canRead = hasPermission('service_request:read');
    return {
      canRead,
      // Coarse affordance only: the server checks requester/current assignee on every write.
      canUpload: canRead && (hasPermission('service_request:write') || hasPermission('service_request:provision')),
      canDelete: hasPermission('service_request:delete'),
    };
  }
  const resource = recordClass === 'catalog_task' ? 'service_request' : toTargetType(recordClass as WorkItemCommon['recordClass']);
  const uploadAction = ['incident', 'problem', 'change_request'].includes(recordClass) ? 'write' : 'create';
  return {
    canRead: hasPermission(`${resource}:read`),
    canUpload: hasPermission(`${resource}:${uploadAction}`),
    canDelete: hasPermission(`${resource}:delete`),
  };
}
