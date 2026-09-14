import { httpClient } from './http-client';
import type { WorkItemRecordClass } from './work-item-creation';
export type RelationType =
  | 'related_to'
  | 'investigated_by'
  | 'resolved_by_change'
  | 'caused_by'
  | 'requested_change'
  | 'fulfilled_by'
  | 'duplicate_of'
  | 'parent_child';
export interface RelationEndpoint {
  workItemId: number;
  number: string;
  recordClass: WorkItemRecordClass;
  title: string;
  status: string;
  version: number;
}
export interface RelationView {
  id: number;
  relationType: RelationType;
  required: boolean;
  source: RelationEndpoint;
  target: RelationEndpoint;
}
export interface SourceRelation {
  sourceWorkItemId: number;
  relationType: RelationType;
  expectedVersion: number;
  metadata?: { required: boolean };
}
export interface RelationCommand extends SourceRelation {
  targetWorkItemId: number;
  operationId: string;
}
export interface RelationReceipt {
  workItemId: number;
  version: number;
  status: string;
  replayed: boolean;
}
export interface RelationContext {
  source: RelationEndpoint;
  mutation: { allowed: boolean; reason?: string };
}
export const WorkItemRelationsApi = {
  list: (id: number) => httpClient.get<RelationView[]>(`/api/v1/work-items/${id}/relations`),
  context: (id: number) =>
    httpClient.get<RelationContext>(`/api/v1/work-items/${id}/relation-context`),
  add: (body: RelationCommand, assertSubmissionContext?: () => void) =>
    httpClient.post<RelationReceipt>(
      `/api/v1/work-items/${body.sourceWorkItemId}/relations`,
      body,
      { skipCamelCaseBody: true, ...(assertSubmissionContext ? { assertSubmissionContext } : {}) }
    ),
  remove: (body: RelationCommand, assertSubmissionContext?: () => void) =>
    httpClient.request<RelationReceipt>(`/api/v1/work-items/${body.sourceWorkItemId}/relations`, {
      method: 'DELETE',
      body: JSON.stringify(body),
      skipCamelCaseBody: true,
      ...(assertSubmissionContext ? { assertSubmissionContext } : {}),
    }),
};
