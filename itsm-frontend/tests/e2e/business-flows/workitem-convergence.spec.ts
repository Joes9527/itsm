import { test, expect, operation, isolatedEnvironment, type Domain, Journey, data } from '../fixtures/workitem-convergence';
import { establishSession, loginAndReturn, mutateWithCSRF } from '../auth-utils';

test.describe('V1 isolated WorkItem convergence', () => {
  test.describe.configure({ mode: 'serial', timeout: 120_000 });
  for (const domain of ['incidents', 'problems', 'changes'] satisfies Domain[]) {
    test(domain + ': reason, observed version, replay and persisted handover', async ({ journey }) => {
      const item = await journey.create(domain); const first = await journey.user(); const next = await journey.user();
      await journey.assign(item, first.id);
      if (domain === 'incidents') await journey.action(item, 'start', { reason: 'isolate progress preservation' });
      if (domain === 'problems') {
        await journey.action(item, 'investigate');
        const version = (await journey.detail(item)).version;
        await journey.write('PUT', '/problems/' + item.id, { version, operationId: operation(), rootCause: 'Root cause A', resolution: 'Permanent fix A' });
        await journey.action(item, 'verify-resolution', { verificationNote: 'Isolated regression passed' });
      }
      const before = await journey.detail(item); const missing = journey.assignment(item, before.version, next.id);
      expect((await journey.raw(missing.method, missing.path, missing.body)).status()).toBe(400);
      expect((await journey.detail(item)).version).toBe(before.version);
      const command = journey.assignment(item, before.version, next.id, 'Application team owns the next step');
      await journey.write(command.method, command.path, command.body);
      const after = await journey.detail(item);
      expect(after.assigneeId).toBe(next.id); expect(after.version).toBe(before.version + 1); expect(after.status).toBe(before.status);
      for (const key of domain === 'problems' ? ['rootCause', 'resolution', 'verifiedVersion', 'verificationNote'] :
        domain === 'changes' ? ['implementationPlan', 'rollbackPlan'] : ['resolution']) {
        expect(after[key], key).toEqual(before[key]);
      }
      await journey.write(command.method, command.path, command.body);
      expect((await journey.detail(item)).version).toBe(after.version);
      const stale = journey.assignment(item, before.version, first.id, 'Stale browser handover');
      expect((await journey.raw(stale.method, stale.path, stale.body)).status()).toBe(409);
      expect((await journey.detail(item)).assigneeId).toBe(next.id);
      if(domain==='changes') journey.assertChangeWorkflowOwnership(item,false);
      if (domain === 'problems') {
        expect(after.actions.resolve.allowed).toBe(true); await journey.action(item, 'resolve');
        expect((await journey.detail(item)).actions.close.allowed).toBe(true); await journey.action(item, 'close');
        expect((await journey.detail(item)).status).toBe('closed');
      }
    });
    test(domain + ': two browsers keep rejected handover input', async ({ journey, browser }) => {
      const env = isolatedEnvironment(); const item = await journey.create(domain);
      const owner = await journey.user(); const winner = await journey.user(); const loser = await journey.user();
      await journey.assign(item, owner.id);
      const contextA = await browser.newContext({ baseURL: env.baseURL }); const contextB = await browser.newContext({ baseURL: env.baseURL });
      try {
        const pageA = await contextA.newPage(); const pageB = await contextB.newPage();
        for (const page of [pageA, pageB]) {
          await loginAndReturn(page, env.credentials, '/' + domain + '/' + item.id);
          await page.getByTestId('workitem-assignment-open').click();
        }
        for (const [page, target, reason] of [[pageA, winner, 'Winning browser reason'], [pageB, loser, 'Preserve this conflict reason']] as const) {
          await page.getByTestId('workitem-assignment-assignee').selectOption(String(target.id));
          await page.getByTestId('workitem-assignment-reason').fill(reason);
        }
        await pageA.getByTestId('workitem-assignment-submit').click();
        await expect.poll(async () => (await journey.detail(item)).assigneeId).toBe(winner.id);
        const submissions: Record<string, unknown>[] = [];
        const responses: Promise<{status:number;message:string}>[] = [];
        pageB.on('request', req => {
          if (['PUT', 'POST'].includes(req.method()) && new URL(req.url()).pathname.startsWith('/api/v1/' + domain + '/' + item.id)) {
            submissions.push(req.postDataJSON());
            responses.push(req.response().then(async response=>({status:response?.status()??0,message:response?(await response.json()).message:''})));
          }
        });
        await pageB.getByTestId('workitem-assignment-submit').click();
        await expect(pageB.getByTestId('workitem-assignment-conflict')).toBeVisible();
        await expect(pageB.getByTestId('workitem-assignment-reason')).toHaveValue('Preserve this conflict reason');
        expect(submissions).toHaveLength(1); expect((await journey.detail(item)).assigneeId).toBe(winner.id);
        await expect(pageB.getByTestId('workitem-assignment-assignee')).toHaveValue(String(loser.id));
        await pageB.getByTestId('workitem-assignment-refresh').click();
        await expect(pageB.getByTestId('workitem-assignment-confirm')).toBeVisible();
        expect(submissions).toHaveLength(1);
        await pageB.getByTestId('workitem-assignment-confirm').click();
        expect(submissions).toHaveLength(1);
        await pageB.getByTestId('workitem-assignment-submit').click();
        await expect.poll(async () => (await journey.detail(item)).assigneeId).toBe(loser.id);
        const outcomes=await Promise.all(responses);
        // The shared HTTP client may refresh a rejected CSRF token once. This is
        // the same logical command, while a domain 409 must never be retried.
        expect(outcomes.map(result=>result.status)).toEqual(submissions.length===3?[409,403,200]:[409,200]);
        if(submissions.length===3) {
          expect(outcomes[1].message).toMatch(/csrf/i);
          expect(submissions[2]).toEqual(submissions[1]);
        }
        expect(submissions[1].operationId).not.toBe(submissions[0].operationId);
        const versionField = domain === 'changes' ? 'expectedVersion' : 'version';
        expect(Number(submissions[1][versionField])).toBeGreaterThan(Number(submissions[0][versionField]));
        expect((await journey.detail(item)).version).toBe(Number(submissions[1][versionField])+1);
        if(domain==='changes') journey.assertChangeWorkflowOwnership(item,false);
      } finally { await contextA.close(); await contextB.close(); }
    });
  }
  test('direct API cannot use a requester session to reassign a professional item', async ({ journey, playwright }) => {
    const item = await journey.create('problems'); const target = await journey.user(); const outsider = await journey.user('end_user');
    const env = isolatedEnvironment(); const request = await playwright.request.newContext();
    try {
      await establishSession(request, outsider.credentials, env.apiURL);
      const before = await journey.detail(item); const command = journey.assignment(item, before.version, target.id, 'Unauthorized attempt');
      const result = await mutateWithCSRF(request, command.method, env.apiURL + '/api/v1' + command.path, { data: command.body });
      expect([403, 404]).toContain(result.status()); expect((await journey.detail(item)).version).toBe(before.version);
    } finally { await request.dispose(); }
  });
});


test('V1 cross-domain recovery and approved Change outcomes retain explicit Problem verification', async ({ journey, playwright }) => {
  test.setTimeout(240000);
  const incident = await journey.create('incidents');
  const owner = await journey.user();
  await journey.assign(incident, owner.id);
  await journey.action(incident,'start');
  await journey.action(incident,'resolve',{resolution:'Temporary workaround restored isolated service'});
  const restored = await journey.detail(incident);
  expect(restored.status).toBe('resolved');
  const sla=await journey.write('POST','/sla/definitions',{name:operation(),serviceType:'problem',priority:'medium',responseTime:30,resolutionTime:240,isActive:true});
  const binding=await journey.write('POST','/process-bindings',{businessType:'problem',processDefinitionKey:'problem_management_flow',priority:10000,isDefault:true,isActive:true,slaPolicyId:String(sla.id)});
  journey.created.push({kind:'sla-definitions',id:sla.id},{kind:'process-bindings',id:binding.id});
  const problem = await journey.create('problems');
  expect((await journey.get('/tickets/'+problem.workItemId+'/sla')).cycleNumber).toBe(1);
  await journey.relation(incident,problem,'investigated_by');
  await journey.action(problem,'investigate');
  await journey.write('PUT','/problems/'+problem.id,{version:(await journey.detail(problem)).version,operationId:operation(),rootCause:'Faulty release root cause',resolution:'Replace faulty component'});
  const rejectUnverified = async () => {
    const before=await journey.detail(problem);
    const response=await journey.raw('POST','/problems/'+problem.id+'/resolve',{version:before.version,operationId:operation()});
    expect(response.status()).toBe(400);
    expect((await journey.detail(problem)).version).toBe(before.version);
  };
  await rejectUnverified();
  const approver = await journey.user();
  const roles=await journey.get('/roles?page=1&pageSize=100');
  const cabRole=roles.roles.find((role:any)=>role.code==='change_manager');
  expect(cabRole).toBeTruthy();
  await journey.write('PUT','/users/'+approver.id,{additionalRoleIds:[cabRole.id]});
  const approvalRequest=await playwright.request.newContext();
  const env=isolatedEnvironment();
  await establishSession(approvalRequest,approver.credentials,env.apiURL);
  const cab=new Journey(approvalRequest,env.apiURL);
  try {
    for (const outcome of ['failed','rolled_back','successful']) {
      const change=await journey.create('changes');
      const frozenWorkflow=journey.workflowDeliveryFacts(change).snapshots[0];
      await journey.assign(change,owner.id);
      expect((await journey.detail(change)).assigneeId).not.toBe(approver.id);
      await journey.relation(problem,change,'resolved_by_change',outcome==='successful');
      await journey.action(change,'submit');
      expect(journey.assertChangeWorkflowOwnership(change,true).snapshots[0]).toEqual(frozenWorkflow);
      await journey.changeTask(change,'assess','Activity_Assessment',{evidence:'Isolated assessment for '+outcome});
      await cab.changeTask(change,'approve','Activity_CABApproval');
      const assessed=await journey.detail(change);
      const assessment=journey.persistedAssessment(change);
      expect(assessment.assessment_evidence).toBe('Isolated assessment for '+outcome);
      expect(assessment.assessment_digest).toBeTruthy();
      expect(assessment.assessed_by).toBeGreaterThan(0);
      expect(Number.isFinite(Date.parse(assessment.assessed_at))).toBe(true);
      const currentTasks=await journey.tasks(change);
      const cabTask=currentTasks.find((task:any)=>task.taskDefinitionKey==='Activity_CABApproval');
      expect(cabTask).toBeTruthy();
      const historyPath='/bpmn/process-instances/'+encodeURIComponent(cabTask.processInstanceKey)+'/approval-history';
      const approvalHistory=await journey.get(historyPath);
      expect(approvalHistory.some((decision:any)=>decision.actorId===approver.id&&decision.decision==='approved')).toBe(true);
      const taskActors=currentTasks.map((task:any)=>({id:task.taskId,assignee:task.assignee,candidateUsers:task.candidateUsers,candidateGroups:task.candidateGroups})).sort((a:any,b:any)=>a.id.localeCompare(b.id));
      const replacement=await journey.user();
      await journey.assign(change,replacement.id,'Dedicated implementation team takes ownership');
      const reassigned=await journey.detail(change);
      expect(reassigned.assigneeId).toBe(replacement.id);
      expect(reassigned.assigneeId).not.toBe(approver.id);
      for(const field of ['status','implementationPlan','rollbackPlan']) expect(reassigned[field],field).toEqual(assessed[field]);
      expect(journey.persistedAssessment(change)).toEqual(assessment);
      expect(await journey.get(historyPath)).toEqual(approvalHistory);
      expect((await journey.tasks(change)).map((task:any)=>({id:task.taskId,assignee:task.assignee,candidateUsers:task.candidateUsers,candidateGroups:task.candidateGroups})).sort((a:any,b:any)=>a.id.localeCompare(b.id))).toEqual(taskActors);
      await journey.changeTask(change,'schedule','Activity_Schedule',{plannedStartDate:new Date(Date.now()-60000).toISOString(),plannedEndDate:new Date(Date.now()+3600000).toISOString()});
      await journey.changeTask(change,'implement','Activity_Implement');
      await journey.changeTask(change,'record-outcome','Activity_Verify',{outcome,evidence:'Observed '+outcome,actualEndDate:new Date().toISOString()});
      expect((await journey.detail(change)).outcome).toBe(outcome);
      expect(journey.assertChangeWorkflowOwnership(change,true).snapshots[0]).toEqual(frozenWorkflow);
      expect((await journey.detail(problem)).status).toBe('investigating');
      await rejectUnverified();
    }
    await journey.action(problem,'verify-resolution',{verificationNote:'Regression after successful Change passed'});
    await journey.action(problem,'resolve');
    await journey.action(problem,'close');
    const before=await journey.get('/tickets/'+problem.workItemId+'/sla');
    const createdAt=(await journey.detail(problem)).createdAt;
    await journey.action(problem,'reopen',{reason:'Regression requires a new investigation cycle'});
    const reopened=await journey.detail(problem);
    expect(reopened.status).toBe('investigating');
    expect(reopened.createdAt).toBe(createdAt);
    expect(reopened.actions.resolve.allowed).toBe(false);
    const after=await journey.get('/tickets/'+problem.workItemId+'/sla');
    expect(after.cycleNumber).toBe(before.cycleNumber+1);
    expect(after.history.length).toBe(before.history.length+1);
    expect((await journey.detail(incident)).resolution).toBe(restored.resolution);
  } finally { await approvalRequest.dispose(); }
});

test('V1 generic and Requested Item collaboration persists and approval uses BPMN', async ({ journey }) => {
  test.setTimeout(120000);
  const generic=await journey.generic();
  const actor=await journey.get('/auth/me');
  const workflowKey=operation();
  const bpmnXml='<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="v1"><process id="'+workflowKey+'" isExecutable="true"><startEvent id="start"/><userTask id="Activity_Accept" name="Accept isolated request" assignee="'+actor.id+'"/><userTask id="Activity_Approval" name="Approve isolated request" taskPurpose="approval" assignee="'+actor.id+'"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="Activity_Accept"/><sequenceFlow id="b" sourceRef="Activity_Accept" targetRef="Activity_Approval"/><sequenceFlow id="c" sourceRef="Activity_Approval" targetRef="end"/></process></definitions>';
  const workflow=await journey.write('POST','/bpmn/process-definitions',{key:workflowKey,name:workflowKey,bpmnXml});
  journey.created.push({kind:'process-definitions',id:workflow.id});
  const catalog=await journey.write('POST','/service-catalogs',{name:operation(),category:'V1 isolated',description:'Disposable approval catalog',targetClass:'service_request_item',serviceType:'general',status:'enabled',requiresApproval:true,processDefinitionKey:workflowKey});
  journey.created.push({kind:'service-catalogs',id:catalog.id});
  const definition=await journey.get('/service-catalogs/'+catalog.id);
  const requestKey=operation();
  const requested=await data(await journey.raw('POST','/service-requests',{catalogId:catalog.id,recordClass:'service_request_item',catalogVersion:definition.catalogVersion,formSchemaVersion:definition.formSchemaVersion,title:requestKey,reason:'Isolated approval regression',formData:{}},{'Idempotency-Key':requestKey}),201);
  journey.created.push({kind:'service-requests',id:requested.professionalReference.id,workItemId:requested.workItemId});
  const assignee=await journey.user();
  for (const receipt of [generic,requested]) {
    const wid=receipt.workItemId;
    const before=await journey.get('/tickets/'+wid);
    await journey.write('POST','/tickets/'+wid+'/assign',{assigneeId:assignee.id,version:before.version});
    expect((await journey.get('/tickets/'+wid)).assigneeId).toBe(assignee.id);
    const note=operation();
    await journey.write('POST','/tickets/'+wid+'/comments',{content:note,isInternal:false});
    const comments=await journey.get('/tickets/'+wid+'/comments');
    expect(comments.comments.some((comment:any)=>comment.content===note)).toBe(true);
    const csrf=await data(await journey.request.get(journey.apiURL+'/api/v1/csrf-token'));
    const uploaded=await data(await journey.request.post(journey.apiURL+'/api/v1/tickets/'+wid+'/attachments',{
      headers:{'X-CSRF-Token':csrf.csrf_token},
      multipart:{file:{name:note+'.txt',mimeType:'text/plain',buffer:Buffer.from('Isolated WorkItem attachment evidence')}}}));
    const attachments=await journey.get('/tickets/'+wid+'/attachments');
    expect(attachments.attachments.some((file:any)=>file.id===uploaded.id)).toBe(true);
    const downloaded=await journey.request.get(journey.apiURL+'/api/v1/tickets/'+wid+'/attachments/'+uploaded.id);
    expect(downloaded.status()).toBe(200);
    expect(await downloaded.text()).toBe('Isolated WorkItem attachment evidence');
  }
  const tasks=async()=> {
    const result=await journey.get('/bpmn/tasks?page=1&pageSize=100&businessType=service_request_item&businessId='+requested.workItemId);
    return result.data;
  };
  let accept:any;
  await expect.poll(async()=>{accept=(await tasks()).find((task:any)=>task.taskDefinitionKey==='Activity_Accept'&&task.businessId===requested.workItemId);return Boolean(accept);},{timeout:30000}).toBe(true);
  await journey.write('PUT','/bpmn/tasks/'+encodeURIComponent(accept.taskId)+'/complete',{variables:{}});
  let approval:any;
  await expect.poll(async()=>{approval=(await tasks()).find((task:any)=>task.taskDefinitionKey==='Activity_Approval'&&task.status!=='completed');return Boolean(approval);},{timeout:30000}).toBe(true);
  expect(approval.taskPurpose).toBe('approval');
  await journey.write('POST','/bpmn/tasks/'+encodeURIComponent(approval.taskId)+'/decisions',{action:'approve',comment:'Isolated service approval'});
  const persisted=await journey.get('/bpmn/tasks/'+encodeURIComponent(approval.taskId));
  expect(persisted.status).toBe('completed');
  const decisions=await journey.get('/bpmn/process-instances/'+encodeURIComponent(approval.processInstanceKey)+'/approval-history');
  expect(decisions.some((decision:any)=>decision.decision==='approved')).toBe(true);
  expect((await journey.get('/tickets/'+requested.workItemId)).recordClass).toBe('service_request_item');
  await journey.assertRequestedWorkflowDelivered(requested.workItemId);
});
