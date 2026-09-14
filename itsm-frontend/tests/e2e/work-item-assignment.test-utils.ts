import { createHash } from 'node:crypto';

export interface ApprovedAssignmentTarget {
 tenantId: number; catalogId: number; definitionKey: string; definitionId: number;
 definitionVersion: string; definitionSha256: string; configurationFrozen: boolean;
}
export interface AssignmentCatalog {
 id: number; targetClass: string; catalogVersion: number; formSchemaVersion: number; processDefinitionKey: string;
}
export interface AssignmentDefinition {
 id: number; tenantId: number; key: string; version: string; isActive: boolean; deploymentId: number; bpmnXml: string;
}
export interface AssignmentPreflightIO<T = unknown> {
 getCatalog(): Promise<AssignmentCatalog>;
 getActiveDefinitions(key: string): Promise<{ data: AssignmentDefinition[]; pagination: { page: number; pageSize: number; total: number } }>;
 submit(catalog: AssignmentCatalog): Promise<T>;
}
// Intake selects active definitions by deployed_at DESC, id DESC, not isLatest.
// This controlled fixture requires exactly one active row under an owned key;
// no ordering ambiguity is then possible. The operator freezes configuration
// throughout the run: the public APIs do not offer an atomic selection lease.
export async function submitApprovedAssignmentIntake<T>(io: AssignmentPreflightIO<T>, expected: ApprovedAssignmentTarget, approvedXml: string): Promise<T> {
 const positive = (value: number) => Number.isSafeInteger(value) && value > 0;
 if (!positive(expected.tenantId) || !positive(expected.catalogId) || !positive(expected.definitionId) ||
     !/^work_item_assignment_acceptance_[A-Za-z0-9_-]+$/.test(expected.definitionKey) ||
     !expected.definitionVersion || expected.configurationFrozen !== true ||
     createHash('sha256').update(approvedXml).digest('hex') !== expected.definitionSha256) {
  throw new Error('approved frozen assignment fixture identity/digest required');
 }
 const catalog = await io.getCatalog();
 if (catalog.id !== expected.catalogId || catalog.targetClass !== 'service_request_item' ||
     catalog.processDefinitionKey !== expected.definitionKey) {
  throw new Error('catalog must use the nonempty approved owned definition key');
 }
 const result = await io.getActiveDefinitions(expected.definitionKey);
 if (!result?.pagination || result.pagination.page !== 1 || result.pagination.pageSize !== 2 ||
     result.pagination.total !== 1 || !Array.isArray(result.data) || result.data.length !== 1) {
  throw new Error('exactly one active definition required; unresolved or ambiguous intake selection');
 }
 const selected = result.data[0];
 if (selected.id !== expected.definitionId || selected.version !== expected.definitionVersion ||
     selected.key !== expected.definitionKey || selected.tenantId !== expected.tenantId ||
     selected.isActive !== true || !positive(selected.deploymentId) || selected.bpmnXml !== approvedXml) {
  throw new Error('active intake definition must match approved ID/version/tenant/exact human-only XML');
 }
 return io.submit(catalog);
}
