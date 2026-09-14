import { test, expect, type Page, type BrowserContext } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { mutateWithCSRF } from '../auth-utils';

type Role = 'requester' | 'helpdesk' | 'a' | 'b' | 'manager';
type Fixture = {
  baseURL: string; tenantId: number; catalogId: number; definitionKey: string;
  states: Record<Role, string>; actors: Record<Role, number>;
};
type Task = {
  id: number; taskName: string; status: string; assigneeSource: string;
  responsibleUserId: number; actorId: number; assignmentState: string;
  processInstanceId: number; processInstanceKey: string; uiActions: { claim: boolean; complete: boolean };
};

// Run only after the controlled deployment gate. This test never publishes a
// definition, changes grants, or contacts a provisioning/notification transport.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });
test('pure-human WorkItem assignment A to B preserves independent responsibilities', async ({ browser }) => {
  test.setTimeout(180_000);
  const file = process.env.WORK_ITEM_ASSIGNMENT_FIXTURE;
  if (!file) throw new Error('WORK_ITEM_ASSIGNMENT_FIXTURE must name the approved private runtime fixture');
  const fixture = JSON.parse(readFileSync(file, 'utf8')) as Fixture;
  expect(fixture.baseURL).toBe(process.env.PLAYWRIGHT_BASE_URL);
  const contexts: BrowserContext[] = [];
  const pages = {} as Record<Role, Page>;
  const get = async <T,>(page: Page, path: string): Promise<T> => {
    const response = await page.request.get(fixture.baseURL + path);
    expect(response.status()).toBe(200);
    const body = await response.json(); expect(body.code).toBe(0); return body.data as T;
  };
  const mutate = async (page: Page, method: 'POST' | 'PUT' | 'DELETE', path: string, data: unknown = {}) => {
    const response = await mutateWithCSRF(page.request, method, fixture.baseURL + path, { data });
    expect(response.ok()).toBe(true); const body = await response.json(); expect(body.code).toBe(0); return body.data;
  };
  let workItemId: number | undefined;
  let completed = false;
  try {
    for (const role of ['requester', 'helpdesk', 'a', 'b', 'manager'] as Role[]) {
      const context = await browser.newContext({ baseURL: fixture.baseURL, storageState: fixture.states[role] });
      contexts.push(context); pages[role] = await context.newPage();
      const me = await get<{ id: number; tenantId: number }>(pages[role], '/api/v1/auth/me');
      expect(me.id).toBe(fixture.actors[role]); expect(me.tenantId).toBe(fixture.tenantId);
    }
    expect(new Set(Object.values(fixture.actors)).size).toBe(5);
    const definition = await get<{ bpmnXml: string }>(pages.helpdesk, `/api/v1/bpmn/process-definitions/${fixture.definitionKey}`);
    expect(definition.bpmnXml).toContain('assigneeSource="work_item_assignee"');
    expect(definition.bpmnXml).not.toMatch(/serviceTask|callActivity|implementation=|operationRef=/);
    const catalog = await get<{ targetClass: string; catalogVersion: number; formSchemaVersion: number }>(pages.requester, `/api/v1/service-catalogs/${fixture.catalogId}`);
    expect(catalog.targetClass).toBe('service_request_item');
    const submitted = await mutateWithCSRF(pages.requester.request, 'POST', fixture.baseURL + '/api/v1/service-requests', {
      data: { catalogId: fixture.catalogId, recordClass: catalog.targetClass, catalogVersion: catalog.catalogVersion,
        formSchemaVersion: catalog.formSchemaVersion, title: `Assignment acceptance ${randomUUID()}`, reason: 'Pure human assignment validation', complianceAck: true, formData: {} },
      headers: { 'Idempotency-Key': randomUUID() },
    });
    expect(submitted.status()).toBe(201);
    const envelope = await submitted.json(); expect(envelope.code).toBe(0);
    workItemId = envelope.data.workItemId; expect(workItemId).toBeGreaterThan(0);
    const tasks = async (page: Page) => (await get<{ data: Task[] }>(page, `/api/v1/bpmn/tasks?businessType=service_request&businessId=${workItemId}&page=1&pageSize=100`)).data;
    const live = async (page: Page, name: string) => {
      const rows = (await tasks(page)).filter(row => row.taskName === name && !['completed', 'cancelled'].includes(row.status));
      expect(rows).toHaveLength(1); return rows[0];
    };
    const helpdesk = await live(pages.helpdesk, 'Assignment acceptance Helpdesk');
    expect(helpdesk.assigneeSource).toBe('');
    await mutate(pages.helpdesk, 'PUT', `/api/v1/bpmn/tasks/${helpdesk.id}/claim`);
    await mutate(pages.helpdesk, 'POST', `/api/v1/tickets/${workItemId}/assign`, { assigneeId: fixture.actors.a, reason: 'Initial assignment' });
    await mutate(pages.helpdesk, 'PUT', `/api/v1/bpmn/tasks/${helpdesk.id}/complete`);
    const fulfillment = await live(pages.a, 'Assignment acceptance fulfillment');
    expect(fulfillment.responsibleUserId).toBe(fixture.actors.a);
    expect(fulfillment.uiActions.claim).toBe(false);
    await pages.a.goto(`/tickets/${workItemId}`);
    const panelA = pages.a.getByRole('region', { name: '当前流程任务' });
    await expect(panelA.getByRole('button', { name: '完成任务', exact: true })).toBeVisible();
    await mutate(pages.helpdesk, 'POST', `/api/v1/tickets/${workItemId}/assign`, { assigneeId: fixture.actors.b, reason: 'A to B' });
    await pages.a.reload();
    await expect(pages.a.getByRole('button', { name: '完成任务', exact: true })).toHaveCount(0);
    const denied = await mutateWithCSRF(pages.a.request, 'PUT', fixture.baseURL + `/api/v1/bpmn/tasks/${fulfillment.id}/complete`, { data: {} });
    expect(denied.status()).toBe(403); expect((await denied.json()).code).not.toBe(0);
    await pages.b.goto(`/tickets/${workItemId}`);
    const panelB = pages.b.getByRole('region', { name: '当前流程任务' });
    await expect(panelB.getByText('已由工单分配')).toBeVisible();
    await expect(panelB.getByRole('button', { name: '领取任务', exact: true })).toHaveCount(0);
    await panelB.getByRole('button', { name: '完成任务', exact: true }).click();
    const completion = pages.b.waitForResponse(response => response.request().method() === 'PUT' && response.url().endsWith(`/bpmn/tasks/${fulfillment.id}/complete`));
    await pages.b.getByRole('button', { name: '确认完成', exact: true }).click();
    expect((await (await completion).json()).code).toBe(0);
    const manager = await live(pages.manager, 'Assignment acceptance manager');
    expect(manager.assigneeSource).toBe('');
    await mutate(pages.manager, 'PUT', `/api/v1/bpmn/tasks/${manager.id}/claim`);
    await mutate(pages.manager, 'PUT', `/api/v1/bpmn/tasks/${manager.id}/complete`);
    const confirmation = await live(pages.requester, 'Assignment acceptance confirmation');
    expect(confirmation.assigneeSource).toBe(''); expect(confirmation.responsibleUserId).toBe(fixture.actors.requester);
    await mutate(pages.helpdesk, 'POST', `/api/v1/tickets/${workItemId}/assign`, { assigneeId: fixture.actors.a, reason: 'After terminal task' });
    const historical = await get<Task>(pages.helpdesk, `/api/v1/bpmn/tasks/${fulfillment.id}`);
    expect(historical.status).toBe('completed'); expect(historical.assignmentState).toBe('terminal');
    expect(historical.responsibleUserId).toBe(fixture.actors.b); expect(historical.actorId).toBe(fixture.actors.b);
    await mutate(pages.requester, 'PUT', `/api/v1/bpmn/tasks/${confirmation.id}/complete`);
    const instance = await get<{ status: string }>(pages.helpdesk, `/api/v1/bpmn/process-instances/${fulfillment.processInstanceKey}`);
    expect(instance.status).toBe('completed');
    await test.info().attach('assignment-receipt', { body: JSON.stringify({ workItemId, taskId: fulfillment.id, instanceKey: fulfillment.processInstanceKey, tenantId: fixture.tenantId, responsibleUserId: historical.responsibleUserId, actorId: historical.actorId }), contentType: 'application/json' });
    completed = true;
  } finally {
    // Preserve a failed record for audit/read-only DB inspection; successful
    // runtime acceptance owns cleanup through the ordinary ticket API.
    if (workItemId && completed) {
      await mutate(pages.helpdesk, 'DELETE', `/api/v1/tickets/${workItemId}`);
      expect((await pages.helpdesk.request.get(fixture.baseURL + `/api/v1/tickets/${workItemId}`)).status()).toBe(404);
    }
    for (const context of contexts) await context.close();
  }
});
