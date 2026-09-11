import type { AssignmentInput } from '@/components/work-item/WorkItemAssignment';
import { IncidentAPI } from './incident-api';
import { ProblemApi } from './problem-api';
import { ChangeApi } from './change-api';

export const assignIncidentWorkItem = (professionalId: number, input: AssignmentInput) =>
  IncidentAPI.assignIncident(professionalId, input);
export const assignProblemWorkItem = (
  professionalId: number,
  { reason, ...input }: AssignmentInput
) => ProblemApi.updateProblem(professionalId, { ...input, assignmentReason: reason });
export const assignChangeWorkItem = (
  professionalId: number,
  { reason, version, ...input }: AssignmentInput
) =>
  ChangeApi.assignChange(professionalId, {
    ...input,
    assignmentReason: reason,
    expectedVersion: version,
  });
