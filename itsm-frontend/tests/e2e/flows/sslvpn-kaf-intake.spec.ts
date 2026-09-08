import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import { accessSync, constants, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import {matchesSSLVPNPostcheck} from '../sslvpn-postcheck.test-utils';

const fixturePath = process.env.SSLVPN_C4_FIXTURE;
if (!fixturePath) throw new Error('SSLVPN_C4_FIXTURE must name the approved owned runtime fixture.');
accessSync(fixturePath, constants.R_OK);
const fixture = JSON.parse(readFileSync(fixturePath, 'utf8'));
for (const file of Object.values(fixture.states) as string[]) accessSync(file, constants.R_OK);
const managerActor = fixture.actors.find((a: {role: string}) => a.role === 'dept_manager');
const networkActor = fixture.actors.find((a: {role: string}) => a.role === 'network_eng');
const evidence = process.env.SSLVPN_C4_EVIDENCE;
if (!evidence) throw new Error('SSLVPN_C4_EVIDENCE must name a new restricted evidence directory.');
mkdirSync(evidence, { mode: 0o700, recursive: true });
test.use({ trace: 'off', video: 'off', screenshot: 'off' });
test.setTimeout(900_000);

async function data(page: Page, endpoint: string) {
  const response = await page.request.get(fixture.itsmBase + endpoint);
  expect(response.status()).toBe(200);
  const body = await response.json();
  expect(body.code).toBe(0);
  return body.data;
}

for (const scenario of ['rejection', 'grant'] as const) test('SSLVPN original KAF card ' + scenario + ' follows the same numbered approval chain', async ({ browser }) => {
  const positive = scenario === 'grant';
  const contexts: BrowserContext[] = [];
  let chatPage: Page | undefined;
  const result: Record<string, unknown> = { scenario, graphExpectedAdds: positive ? 1 : 0 };
  try {
    const requester = await browser.newContext({ baseURL: fixture.itsmBase, storageState: fixture.states.end_user });
    contexts.push(requester);
    const requestPage = await requester.newPage();
    const manager = await browser.newContext({ baseURL: fixture.itsmBase, storageState: fixture.states.dept_manager });
    contexts.push(manager);
    const managerPage = await manager.newPage();
    const network = await browser.newContext({ baseURL: fixture.itsmBase, storageState: fixture.states.network_eng });
    contexts.push(network);
    const networkPage = await network.newPage();
    const kaf = await browser.newContext({ baseURL: fixture.kafBase, storageState: fixture.states.kaf });
    contexts.push(kaf);
    const chat = await kaf.newPage();
    chatPage = chat;
    const interactions = new Map<string, {action_id: string; expires_at?: string; submission?: {state: string; receipt?: {workItemId: number; number: string}}}>();
    const observe = (event: {action_id?: string; submission?: unknown}) => {
      if (event.action_id && event.submission) interactions.set(event.action_id, event as never);
    };
    const connectedSessions = new Set<string>();
    const hydratedSessions = new Set<string>();
    chat.on('websocket', socket => socket.on('framereceived', frame => {
      try {
        const event = JSON.parse(String(frame.payload));
        if (event.type === 'connection' && event.session_id) connectedSessions.add(event.session_id);
        observe(event);
      } catch { /* Non-JSON control frames carry no receipt. */ }
    }));
    chat.on('response', async response => {
      const pathname = new URL(response.url()).pathname;
      if (response.status() === 200 && (/\/sessions\/[^/]+\/history$/.test(pathname) || pathname.endsWith('/sessions/open'))) {
        const body = await response.json();
        const historySession = pathname.match(/\/sessions\/([^/]+)\/history$/)?.[1];
        if (historySession) hydratedSessions.add(historySession);
        if (body.session?.id) hydratedSessions.add(body.session.id);
        for (const event of body.active_interactions ?? []) observe(event);
      }
    });

    for (const [page, id, role] of [[requestPage, fixture.requesterId, 'end_user'], [managerPage, managerActor.id, 'dept_manager'], [networkPage, networkActor.id, 'network_eng']] as const) {
      const me = await data(page, '/api/v1/auth/me');
      expect(me.id).toBe(id); expect(me.role).toBe(role); expect(me.tenantId).toBe(fixture.tenantId);
    }
    await chat.goto('/chat');
    await expect(chat.locator('textarea')).toBeVisible({ timeout: 20_000 });
    if (await chat.getByTitle('Expand sidebar', { exact: true }).isVisible()) {
      await chat.getByTitle('Expand sidebar', { exact: true }).click();
    }
    await expect(chat.getByRole('button', { name: /New session$/ })).toBeVisible();
    const reuseSession = process.env.SSLVPN_C4_REUSE_PENDING_SESSION;
    if (reuseSession) {
      const session = chat.locator('.cp-session-item').filter({ hasText: reuseSession });
      await expect(session).toHaveCount(1);
      await session.click();
      result.resumedPendingSession = reuseSession;
      const expectedSessionId = process.env.SSLVPN_C4_REUSE_SESSION_ID;
      if (expectedSessionId) {
        await expect.poll(() => hydratedSessions.has(expectedSessionId)).toBe(true);
        result.sessionId = expectedSessionId;
      }
    }
    if (!reuseSession || process.env.SSLVPN_C4_REUSE_EMPTY_SESSION_ID) {
    let targetSessionId = process.env.SSLVPN_C4_REUSE_EMPTY_SESSION_ID;
    if (!reuseSession) {
      const opening = chat.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/sessions/open') && r.request().postDataJSON()?.force_new === true);
      await chat.getByRole('button', { name: /New session$/ }).click();
      targetSessionId = (await (await opening).json()).session.id;
    }
    await expect.poll(() => connectedSessions.has(targetSessionId!)).toBe(true);
    await expect(chat.locator('textarea')).toBeEnabled();
    result.sessionId = targetSessionId;
    const title = 'C4 ' + scenario + ' ' + Date.now();
    await chat.locator('textarea').fill('我需要为自己申请 SSLVPN 访问权限，用于远程办公，为期一个月。申请标题为“' + title + '”。请读取我有权申请的 ITSM 服务目录，准备申请让我确认。');
    await expect(chat.locator('.cp-send-btn')).toBeEnabled();
    await chat.locator('.cp-send-btn').click();
    }
    if (!process.env.SSLVPN_C4_REUSE_CREATED_NUMBER) {
    const card = chat.locator('.cp-approval-card').filter({ has: chat.getByRole('button', { name: /Confirm$/ }) });
    await expect(card).toHaveCount(1, { timeout: 120_000 });
    await expect(card).toContainText('One month');
    await card.getByRole('button', { name: /Confirm$/ }).click();
    }
    await expect(chat.getByRole('status').filter({ hasText: /^Request created$/ })).toBeVisible({ timeout: 60_000 });
    const receipt = chat.locator('.cp-approval-card').getByText(/^TKT-\d{6}-\d{6}$/, { exact: true });
    await expect(receipt).toHaveCount(1);
    const number = (await receipt.innerText()).trim();
    result.number = number;
    if (process.env.SSLVPN_C4_REUSE_CREATED_NUMBER) expect(number).toBe(process.env.SSLVPN_C4_REUSE_CREATED_NUMBER);
    await expect.poll(() => [...interactions.values()].filter(event => event.submission?.receipt?.number === number)).toHaveLength(1);
    const original = [...interactions.values()].find(event => event.submission?.receipt?.number === number)!;
    result.interaction = { actionId: original.action_id, expiresAt: original.expires_at, receipt: original.submission!.receipt };
    const workItem = await data(requestPage, '/api/v1/tickets/' + original.submission!.receipt!.workItemId);
    expect(workItem.ticketNumber).toBe(number);
    result.workItemId = workItem.id;
    expect(workItem.recordClass).toBe('service_request_item');
    const sr = await data(requestPage, '/api/v1/service-requests/by-ticket/' + workItem.id);
    expect(sr.catalogId).toBe(fixture.catalogId);
    result.serviceRequestId = sr.id;
    const approvedEvidenceFile = process.env.SSLVPN_C4_RESUME_APPROVAL_EVIDENCE;
    if (approvedEvidenceFile) {
      expect(positive).toBe(true);
      accessSync(approvedEvidenceFile, constants.R_OK);
      const approved = JSON.parse(readFileSync(approvedEvidenceFile, 'utf8'));
      expect(approved.workItemId).toBe(workItem.id);
      expect(approved.number).toBe(number);
      expect(approved.interaction.actionId).toBe(original.action_id);
      expect(approved.claimedActorId).toBe(managerActor.id);
      expect(approved.networkActorId).toBe(networkActor.id);
      for (const [page, actor, taskId, key] of [
        [managerPage, managerActor, approved.task.id, 'UserTask_DeptManagerApproval'],
        [networkPage, networkActor, approved.networkTask.id, 'UserTask_L2NetworkOpsApproval'],
      ] as const) {
        const task = await data(page, '/api/v1/bpmn/tasks/' + taskId);
        expect(task.status).toBe('completed');
        expect(task.assignee).toBe(String(actor.id));
        expect(task.task_definition_key).toBe(key);
      }
      result.resumedOriginalApprovals = {evidence: approvedEvidenceFile, managerTask: approved.task.id, networkTask: approved.networkTask.id};
      await requestPage.goto('/tickets/' + workItem.id);
    } else {
    await expect.poll(async () => {
      const tasks = await data(managerPage, '/api/v1/bpmn/tasks?pageSize=100');
      return tasks.data.filter((t: { workItemNumber: string; status: string }) => t.workItemNumber === number && t.status !== 'completed');
    }, { timeout: 30_000 }).toHaveLength(1);
    const tasks = await data(managerPage, '/api/v1/bpmn/tasks?pageSize=100');
    const task = tasks.data.find((t: { workItemNumber: string }) => t.workItemNumber === number);
    expect(task.taskDefinitionKey).toBe('UserTask_DeptManagerApproval');
    if (!process.env.SSLVPN_C4_RESUME_CLAIMED_TASK) expect(task.assignee).toBeFalsy();
    result.task = { id: task.id, key: task.taskDefinitionKey, processDefinitionKey: task.processDefinitionKey };
    await managerPage.goto('/approvals');
    const row = managerPage.getByRole('row').filter({ has: managerPage.getByRole('link', { name: number, exact: true }) });
    await expect(row).toHaveCount(1);
    if (process.env.SSLVPN_C4_RESUME_CLAIMED_TASK) {
      expect(task.id).toBe(Number(process.env.SSLVPN_C4_RESUME_CLAIMED_TASK));
      expect(task.assignee).toBe(String(managerActor.id));
      expect(number).toBe(process.env.SSLVPN_C4_REUSE_CREATED_NUMBER);
      result.resumedClaimedTask = task.id;
    } else {
      const claimResponse = managerPage.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/bpmn/tasks/' + task.id + '/claim');
      await row.getByRole('button', { name: '领取', exact: true }).click();
      const response = await claimResponse;
      expect(response.status()).toBe(200); expect((await response.json()).code).toBe(0);
    }
    await expect(row.getByRole('button', { name: '领取', exact: true })).toHaveCount(0);
    const claimed = await data(managerPage, '/api/v1/bpmn/tasks/' + task.id);
    expect(claimed.assignee).toBe(String(managerActor.id));
    result.claimedActorId = managerActor.id;
    await requestPage.goto('/tickets/' + workItem.id);
    await expect(requestPage.getByRole('status').filter({ hasText: /^待审批$/ })).toBeVisible();
    await expect(requestPage.getByRole('button', { name: '开始交付', exact: true })).toHaveCount(0);
    const decision = managerPage.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/bpmn/tasks/' + task.id + '/decisions');
    await row.getByRole('button', { name: positive ? '批准' : '拒绝', exact: true }).click();
    const dialog = managerPage.getByRole('dialog', { name: positive ? '批准任务' : '拒绝任务', exact: true });
    await dialog.getByPlaceholder(positive ? '可填写审批意见' : '请填写拒绝原因').fill(positive ? 'C4 fixed Dev fixture: department approval.' : 'C4 controlled rejection: no external permission is authorized.');
    await dialog.getByRole('button', { name: positive ? '确认批准' : '确认拒绝', exact: true }).click();
    const response = await decision; expect(response.status()).toBe(200); expect((await response.json()).code).toBe(0);
    await expect(dialog).not.toBeVisible();
    if (positive) {
      expect(managerActor.id).not.toBe(networkActor.id);
      await expect.poll(async () => {
        const tasks = await data(networkPage, '/api/v1/bpmn/tasks?pageSize=100');
        return tasks.data.filter((t: {workItemNumber: string; status: string}) => t.workItemNumber === number && t.status !== 'completed');
      }).toHaveLength(1);
      const taskList = await data(networkPage, '/api/v1/bpmn/tasks?pageSize=100');
      const networkTask = taskList.data.find((t: {workItemNumber: string}) => t.workItemNumber === number);
      expect(networkTask.taskDefinitionKey).toBe('UserTask_L2NetworkOpsApproval');
      expect(networkTask.assignee).toBeFalsy();
      result.networkTask = {id: networkTask.id, key: networkTask.taskDefinitionKey};
      writeFileSync(path.join(evidence, 'ready-for-graph.json'), JSON.stringify({...result, workItemId: workItem.id}), {mode: 0o600});
      const authorization = process.env.SSLVPN_C4_AUTHORIZATION_FILE;
      if (!authorization) throw new Error('Controlled Graph run requires its independent preflight authorization file.');
      await expect.poll(() => {
        try {
          const approved = JSON.parse(readFileSync(authorization, 'utf8'));
          return approved.fixtureSnapshotVerified === true && approved.workItemId === workItem.id;
        } catch { return false; }
      }, {timeout: 600_000}).toBe(true);
      await networkPage.goto('/approvals');
      const networkRow = networkPage.getByRole('row').filter({has: networkPage.getByRole('link', {name: number, exact: true})});
      await expect(networkRow).toHaveCount(1);
      const claimResponse = networkPage.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/bpmn/tasks/' + networkTask.id + '/claim');
      await networkRow.getByRole('button', {name: '领取', exact: true}).click();
      const claim = await claimResponse; expect(claim.status()).toBe(200); expect((await claim.json()).code).toBe(0);
      const claimed = await data(networkPage, '/api/v1/bpmn/tasks/' + networkTask.id);
      expect(claimed.assignee).toBe(String(networkActor.id));
      result.networkActorId = networkActor.id;
      const approval = networkPage.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/bpmn/tasks/' + networkTask.id + '/decisions');
      await networkRow.getByRole('button', {name: '批准', exact: true}).click();
      const networkDialog = networkPage.getByRole('dialog', {name: '批准任务', exact: true});
      await networkDialog.getByPlaceholder('可填写审批意见').fill('C4 approved fixed Dev Graph fixture; restore after verification.');
      await networkDialog.getByRole('button', {name: '确认批准', exact: true}).click();
      const accepted = await approval; expect(accepted.status()).toBe(200); expect((await accepted.json()).code).toBe(0);
      await expect(networkDialog).not.toBeVisible();
      }
    }
    if (positive) {
      await expect.poll(async () => (await data(requestPage, '/api/v1/service-requests/by-ticket/' + workItem.id)).fulfillmentState, {timeout: 240_000}).toBe('completed');
      const completed = await data(requestPage, '/api/v1/service-requests/by-ticket/' + workItem.id);
      expect(completed.accessResult.outcome).toBe('granted');
      expect(completed.accessResult.verifiedAt).toBeTruthy();
      expect(completed.accessResult.expiresAt).toBeTruthy();
      result.accessResult = completed.accessResult;
      await chat.getByRole('button', {name: /查看申请详情$/}).click();
      await expect(chat.locator('.cp-approval-card')).toContainText('权限已开通', {timeout: 20_000});
      await requestPage.reload();
      await expect(requestPage.getByRole('status').filter({has: requestPage.getByText('已完成', {exact: true})})).toBeVisible();
      await expect(requestPage.getByRole('button', {name: '开始交付', exact: true})).toHaveCount(0);
      await requestPage.screenshot({path: path.join(evidence, 'granted-professional-detail.png')});
      await chat.locator('.cp-approval-card').screenshot({path: path.join(evidence, 'granted-current-card.png')});
      writeFileSync(path.join(evidence, 'completed-before-replay.json'), JSON.stringify(result), {mode: 0o600});
      const postcheck = process.env.SSLVPN_C4_POSTCHECK_FILE;
      if (!postcheck) throw new Error('Controlled Graph run requires provider and replay evidence.');
      await expect.poll(() => {
        return matchesSSLVPNPostcheck(postcheck, workItem.id, completed.accessResult);
      }, {timeout: 300_000}).toBe(true);
    } else {
    await expect.poll(async () => (await data(requestPage, '/api/v1/service-requests/by-ticket/' + workItem.id)).fulfillmentState).toBe('rejected');
    await chat.getByRole('button', { name: '查看申请详情', exact: true }).click();
    await expect(chat.locator('.cp-approval-card')).toContainText('申请已拒绝');
    await requestPage.reload();
    await expect(requestPage.getByRole('status').filter({ hasText: /^已拒绝$/ })).toBeVisible();
    await expect(requestPage.getByRole('button', { name: '开始交付', exact: true })).toHaveCount(0);
    const downstream = await data(networkPage, '/api/v1/bpmn/tasks?pageSize=100');
    expect(downstream.data.filter((t: { workItemNumber: string }) => t.workItemNumber === number)).toHaveLength(0);
    await requestPage.screenshot({ path: path.join(evidence, 'rejected-professional-detail.png') });
    await chat.locator('.cp-approval-card').screenshot({ path: path.join(evidence, 'rejected-current-card.png') });
    }
    result.status = 'passed';
  } catch (error) {
    result.status = 'failed';
    if (chatPage) {
      result.finalPath = new URL(chatPage.url()).pathname;
      await chatPage.screenshot({ path: path.join(evidence, 'failure-page.png') });
    }
    throw error;
  } finally {
    if (chatPage) {
      const stateFile = path.join(path.dirname(fixturePath), path.basename(evidence) + '-kaf-state.json');
      writeFileSync(stateFile, JSON.stringify(await chatPage.context().storageState()), {mode: 0o600});
      result.resumeKafState = stateFile;
    }
    for (const context of contexts) await context.close();
    writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(result, null, 2), { mode: 0o600 });
  }
});
