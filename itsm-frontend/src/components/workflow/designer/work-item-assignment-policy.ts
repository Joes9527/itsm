const delegatedHandlerMetadata = new Set([
  'service_task_type',
  'action',
  'allowed_actions',
  'callback_config_ref',
  'callback_optional',
]);

interface ExtensionValue {
  $type?: string;
  name?: string;
}

interface ExtensionElements {
  $model?: { create: (type: string, properties: Record<string, unknown>) => unknown };
  values?: ExtensionValue[];
}

export function buildWorkItemAssigneePatch(businessObject: Record<string, unknown>): Record<string, unknown> {
  const patch: Record<string, unknown> = {
    assigneeSource: 'work_item_assignee',
    assignee: '', assigneeRole: '', assigneeDeptId: undefined,
    assigneeTeamId: undefined, assigneeProjectId: undefined,
    assigneeTempTeamId: undefined, assigneeGmChain: undefined,
    // 直属上级/层级也是"找人方式"之一：绑定工单当前处理人时必须一并清掉，
    // 否则会留下两种方式并存的定义，发布校验会直接拒绝。
    assigneeDirectManager: undefined, assigneeManagerLevel: undefined,
    candidateUsers: '', candidateGroups: '',
    approvalMode: undefined, approvalThreshold: undefined,
    rejectStrategy: undefined, timeoutAction: undefined,
    allowDelegate: undefined, allowAddApprover: undefined,
    commentRequiredOnReject: undefined,
  };
  const extensionElements = businessObject.extensionElements as ExtensionElements | undefined;
  if (!Array.isArray(extensionElements?.values)) return patch;

  const values = extensionElements.values.filter(value =>
    value.$type?.toLowerCase() !== 'bpmn:metadata' || !value.name || !delegatedHandlerMetadata.has(value.name)
  );
  if (values.length === extensionElements.values.length) return patch;
  if (values.length === 0) return { ...patch, extensionElements: undefined };

  const model = extensionElements.$model ?? (businessObject.$model as ExtensionElements['$model']);
  const replacement = model?.create('bpmn:ExtensionElements', { values }) ?? {
    ...extensionElements,
    values,
  };
  return { ...patch, extensionElements: replacement };
}
