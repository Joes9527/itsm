import type { UserTask } from '@/lib/api/bpmn-workflow-api';

const ACTIVE_TASK_STATUSES = new Set(['created', 'assigned', 'started']);

const RELEASE_TASK_DEFINITION_KEYS = {
  techReview: 'Activity_TechReview',
  schedule: 'Activity_Schedule',
  execute: 'Activity_Execute',
  verify: 'Activity_Verify',
} as const;

export interface ReleaseWorkflowCommands {
  techReview: boolean;
  schedule: boolean;
  execute: boolean;
  verify: boolean;
}

function hasActiveTask(tasks: UserTask[], taskDefinitionKey: string): boolean {
  return tasks.some(
    (task) => task.taskDefinitionKey === taskDefinitionKey && ACTIVE_TASK_STATUSES.has(task.status),
  );
}

export function releaseWorkflowCommands(tasks: UserTask[]): ReleaseWorkflowCommands {
  return {
    techReview: hasActiveTask(tasks, RELEASE_TASK_DEFINITION_KEYS.techReview),
    schedule: hasActiveTask(tasks, RELEASE_TASK_DEFINITION_KEYS.schedule),
    execute: hasActiveTask(tasks, RELEASE_TASK_DEFINITION_KEYS.execute),
    verify: hasActiveTask(tasks, RELEASE_TASK_DEFINITION_KEYS.verify),
  };
}
