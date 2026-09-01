import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { httpClient } from '@/lib/api/http-client';

jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn(), post: jest.fn(), put: jest.fn(), delete: jest.fn() },
}));

const get = httpClient.get as jest.Mock;
const post = httpClient.post as jest.Mock;
const put = httpClient.put as jest.Mock;
const remove = httpClient.delete as jest.Mock;

describe('BPMNWorkflowApi canonical contracts', () => {
  beforeEach(() => jest.clearAllMocks());

  it('maps the backend ListResponse for process definitions exactly once', async () => {
    const definition = { id: 1, key: 'ticket', version: '1.0.0' };
    get.mockResolvedValue({ data: [definition], pagination: { total: 1, page: 1, pageSize: 20 } });
    await expect(
      BPMNWorkflowApi.listProcessDefinitions({ page: 1, pageSize: 20 })
    ).resolves.toEqual({
      items: [definition],
      total: 1,
      page: 1,
      pageSize: 20,
    });
    expect(get).toHaveBeenCalledWith('/api/v1/bpmn/process-definitions', {
      page: '1',
      pageSize: '20',
    });
  });

  it('requires the definition version for update and deletion', async () => {
    put.mockResolvedValue({});
    remove.mockResolvedValue(undefined);
    await BPMNWorkflowApi.updateProcessDefinition('ticket flow', '2.0.0', { name: 'Ticket' });
    await BPMNWorkflowApi.deleteProcessDefinition('ticket flow', '2.0.0');
    expect(put).toHaveBeenCalledWith(
      '/api/v1/bpmn/process-definitions/ticket%20flow?version=2.0.0',
      { name: 'Ticket' }
    );
    expect(remove).toHaveBeenCalledWith(
      '/api/v1/bpmn/process-definitions/ticket%20flow?version=2.0.0'
    );
  });

  it('maps the task list and uses PUT for claim', async () => {
    const task = { id: 7, taskId: 'task-7', taskName: 'Manager approval' };
    get.mockResolvedValue({ data: [task], pagination: { total: 1, page: 1, pageSize: 10 } });
    await expect(BPMNWorkflowApi.listUserTasks({ page: 1, pageSize: 10 })).resolves.toEqual({
      items: [task],
      total: 1,
      page: 1,
      pageSize: 10,
    });
    await BPMNWorkflowApi.claimTask(7);
    expect(put).toHaveBeenCalledWith('/api/v1/bpmn/tasks/7/claim', {});
  });

  it('submits approval only through the ProcessTask decision command', async () => {
    const decision = { action: 'reject' as const, comment: 'insufficient evidence' };
    await BPMNWorkflowApi.submitApprovalDecision(7, decision);
    expect(post).toHaveBeenCalledWith('/api/v1/bpmn/tasks/7/decisions', decision);
  });

  it('uses canonical version query names', async () => {
    get.mockResolvedValue([]);
    await BPMNWorkflowApi.listVersions('ticket');
    await BPMNWorkflowApi.compareVersions('ticket', '1.0.0', '2.0.0');
    expect(get).toHaveBeenNthCalledWith(1, '/api/v1/bpmn/versions', { process_key: 'ticket' });
    expect(get).toHaveBeenNthCalledWith(2, '/api/v1/bpmn/versions/ticket/compare', {
      base_version: '1.0.0',
      target_version: '2.0.0',
    });
  });
});
