import type { ProcessDefinition, ProcessInstance, ProcessVersion, UserTask } from '@/lib/api/bpmn-workflow-api';
import {
  WorkflowInstanceStatus,
  WorkflowStatus,
  WorkflowType,
  type NodeInstance,
  type WorkflowDefinition,
  type WorkflowInstance,
} from '@/types/workflow';

export function processDefinitionToWorkflowDefinition(definition: ProcessDefinition): WorkflowDefinition {
  return {
    id: String(definition.id),
    code: definition.key,
    name: definition.name,
    description: definition.description,
    category: definition.category,
    type: WorkflowType.TICKET,
    version: Number.parseFloat(definition.version) || 1,
    status: definition.isActive ? WorkflowStatus.ACTIVE : WorkflowStatus.DRAFT,
    bpmnXml: definition.bpmnXml,
    nodes: [],
    connections: [],
    variables: [],
    triggers: [],
    settings: {
      allowParallelInstances: true,
      enableVersionControl: true,
      enableAuditLog: true,
    },
    createdBy: 0,
    createdByName: '',
    createdAt: new Date(definition.createdAt),
    updatedAt: new Date(definition.updatedAt),
  };
}

export function processInstanceToWorkflowInstance(instance: ProcessInstance): WorkflowInstance {
  return {
    id: instance.processInstanceId,
    workflowId: instance.processDefinitionKey,
    workflowName: instance.processDefinitionKey,
    version: 1,
    status: instance.status as WorkflowInstanceStatus,
    variables: instance.variables ?? {},
    startTime: new Date(instance.startTime ?? instance.createdAt),
    endTime: instance.endTime ? new Date(instance.endTime) : undefined,
    startedBy: 0,
    startedByName: instance.initiator ?? '',
  };
}

export function processVersionToWorkflowDefinition(version: ProcessVersion): WorkflowDefinition {
  return {
    id: version.id,
    code: version.processDefinitionKey,
    name: version.name,
    description: version.description,
    type: WorkflowType.TICKET,
    version: Number.parseFloat(version.version) || 1,
    status: version.isActive ? WorkflowStatus.ACTIVE : WorkflowStatus.DRAFT,
    bpmnXml: version.bpmnXml,
    nodes: [],
    connections: [],
    variables: [],
    triggers: [],
    settings: {
      allowParallelInstances: true,
      enableVersionControl: true,
      enableAuditLog: true,
    },
    createdBy: 0,
    createdByName: version.createdBy,
    createdAt: new Date(version.createdAt),
    updatedAt: new Date(version.updatedAt),
  };
}

export function userTaskToNodeInstance(task: UserTask): NodeInstance {
  return {
    id: String(task.id),
    instanceId: String(task.processInstanceId),
    nodeId: task.taskId || task.taskDefinitionKey,
    nodeName: task.taskName,
    status: task.status as NodeInstance['status'],
    assignee: task.assignee ? Number(task.assignee) : undefined,
    startTime: new Date(task.createdTime),
    input: task.taskVariables ?? {},
    retryCount: 0,
  };
}
