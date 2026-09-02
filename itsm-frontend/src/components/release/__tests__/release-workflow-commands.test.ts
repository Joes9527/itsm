import { releaseWorkflowCommands } from '../release-workflow-commands';
import type { UserTask } from '@/lib/api/bpmn-workflow-api';

function makeTask(overrides: Partial<UserTask>): UserTask {
  return {
    id: 1,
    taskId: 'task-1',
    taskDefinitionKey: 'Activity_TechReview',
    taskName: '技术评审',
    taskType: 'userTask',
    status: 'created',
    priority: 'normal',
    assignee: '',
    candidateUsers: '',
    candidateGroups: '',
    processInstanceId: 1,
    processInstanceKey: 'PI-1',
    processDefinitionKey: 'release_approval_flow',
    businessKey: 'release-1',
    businessType: 'release',
    businessId: 1,
    taskPurpose: '',
    createdTime: '2026-09-01T00:00:00Z',
    ...overrides,
  };
}

describe('releaseWorkflowCommands', () => {
  it('returns all commands disabled when there are no active tasks', () => {
    expect(releaseWorkflowCommands([])).toEqual({
      techReview: false,
      schedule: false,
      execute: false,
      verify: false,
    });
  });

  it('enables only the command matching an active task definition key', () => {
    const tasks = [makeTask({ taskDefinitionKey: 'Activity_Schedule', status: 'assigned' })];
    expect(releaseWorkflowCommands(tasks)).toEqual({
      techReview: false,
      schedule: true,
      execute: false,
      verify: false,
    });
  });

  it('ignores completed and cancelled tasks', () => {
    const tasks = [
      makeTask({ taskDefinitionKey: 'Activity_TechReview', status: 'completed' }),
      makeTask({ taskDefinitionKey: 'Activity_Execute', status: 'cancelled' }),
    ];
    expect(releaseWorkflowCommands(tasks)).toEqual({
      techReview: false,
      schedule: false,
      execute: false,
      verify: false,
    });
  });

  it('enables verify when the verify task is started', () => {
    const tasks = [makeTask({ taskDefinitionKey: 'Activity_Verify', status: 'started' })];
    expect(releaseWorkflowCommands(tasks)).toEqual({
      techReview: false,
      schedule: false,
      execute: false,
      verify: true,
    });
  });

  it('does not enable any command for the approval task, which has no dedicated button', () => {
    const tasks = [makeTask({ taskDefinitionKey: 'Activity_Approval', status: 'created' })];
    expect(releaseWorkflowCommands(tasks)).toEqual({
      techReview: false,
      schedule: false,
      execute: false,
      verify: false,
    });
  });
});
