import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { test as businessTest, expect } from './auth';
import { establishSession, mutateWithCSRF, type LoginCredentials } from '../auth-utils';
import type { APIRequestContext, APIResponse } from '@playwright/test';

export type Domain = 'incidents' | 'problems' | 'changes';
export type Item = { id: number; workItemId: number; number: string; domain: Domain };
export const prefix = 'V1-WORKITEM-';
export function operation() { return prefix + randomUUID(); }

export function isolatedEnvironment() {
  const baseURL = process.env.PLAYWRIGHT_BASE_URL;
  const apiURL = process.env.NEXT_PUBLIC_API_URL;
  const manifestPath=process.env.PLAYWRIGHT_V1_RESOURCE_MANIFEST;
  if(!manifestPath) throw new Error('Use the dedicated disposable V1 runner; resource manifest required.');
  const manifest=JSON.parse(readFileSync(manifestPath,'utf8'));
  if (!/^workitem-v1-[a-f0-9]{12}$/.test(manifest.runId) ||
      process.env.PLAYWRIGHT_V1_ISOLATED !== manifest.runId ||
      baseURL !== manifest.baseURL || apiURL !== manifest.apiURL ||
      !baseURL?.startsWith('http://127.0.0.1:') || !apiURL?.startsWith('http://127.0.0.1:') ||
      !(manifest.postgresContainer === 'codex-'+manifest.runId+'-pg' || (manifest.postgresContainer === 'codex-'+manifest.runId+'-restored-pg' && manifest.recoveryTarget?.stage === 'restored' && /^[a-f0-9]{64}$/.test(manifest.recoveryTarget?.containerID))) ||
      process.env.PLAYWRIGHT_EXTERNAL_SERVER !== '1') {
    throw new Error('V1 requires its dedicated disposable environment; shared/default servers are forbidden.');
  }
  const password = process.env.PLAYWRIGHT_V1_ADMIN_PASSWORD;
  if (!password) throw new Error('V1 private bootstrap credentials are required.');
  return { baseURL, apiURL, manifest, credentials: { tenantCode: 'default', username: 'admin', password } };
}

export async function data(response: APIResponse, status = 200): Promise<any> {
  expect(response.status(), 'HTTP ' + response.status() + ': ' + await response.text()).toBe(status);
  const body = await response.json(); expect(body.code).toBe(0); return body.data;
}

export class Journey {
  readonly created: Array<{ kind: string; id: number; workItemId?: number }> = [];
  constructor(readonly request: APIRequestContext, readonly apiURL: string) {}
  get(path: string) { return this.request.get(this.apiURL + '/api/v1' + path).then(r => data(r)); }
  raw(method: 'POST' | 'PUT' | 'DELETE', path: string, body: unknown, headers?: Record<string, string>) {
    return mutateWithCSRF(this.request, method, this.apiURL + '/api/v1' + path, { data: body, headers });
  }
  async write(method: 'POST' | 'PUT' | 'DELETE', path: string, body: unknown, status = 200) {
    return data(await this.raw(method, path, body), status);
  }
  async generic() {
    const key = operation();
    const receipt = await data(await this.raw('POST', '/tickets', {title:key, description:'Isolated generic collaboration record.', priority:'medium',type:'ticket'}, {'Idempotency-Key':key}),201);
    this.created.push({kind:'tickets',id:receipt.workItemId,workItemId:receipt.workItemId});
    return receipt;
  }
  async create(domain: Domain): Promise<Item> {
    await this.generic(); // Force professionally scoped IDs to differ from shared WorkItem IDs.
    const key = operation();
    const body = { title: key, description: 'Isolated WorkItem convergence journey evidence.',
      priority: 'medium', ...(domain === 'changes' ? { type: 'normal', justification: 'Restore the isolated service', impactScope: 'low', riskLevel: 'low', implementationPlan: 'Deploy isolated fix', rollbackPlan: 'Restore isolated version' } : {}) };
    const receipt = await data(await this.raw('POST', '/' + domain, body, { 'Idempotency-Key': key }), 201);
    expect(receipt.professionalReference.id).toBeGreaterThan(0); expect(receipt.workItemId).toBeGreaterThan(0);
    const item = { domain, id: receipt.professionalReference.id, workItemId: receipt.workItemId, number: receipt.number };
    this.created.push({ kind: domain, id: item.id, workItemId: item.workItemId });
    const saved = await this.detail(item);
    expect(item.id).not.toBe(item.workItemId);
    expect(saved.workItemId).toBe(item.workItemId); expect(saved.number).toBe(item.number); expect(saved.version).toBeGreaterThan(0);
    if(domain==='changes') {
      expect(receipt.workflowStartStatus).toBe('awaiting_submit');
      this.assertChangeWorkflowOwnership(item,false);
    }
    return item;
  }
  detail(item: Item) { return this.get('/' + item.domain + '/' + item.id); }
  async user(role = 'super_admin') {
    const key = 'v1_' + randomUUID().replaceAll('-', '').slice(0, 16);
    const password = randomUUID() + 'aA!7';
    const created = await this.write('POST', '/users', { username: key, name: key, email: key + '@example.test', password, role }, 200);
    this.created.push({ kind: 'users', id: created.id });
    return { id: created.id, name: key, credentials: { tenantCode: 'default', username: key, password } satisfies LoginCredentials };
  }
  assignment(item: Item, version: number, assigneeId: number, reason?: string, operationId = operation()) {
    const body = item.domain === 'changes' ? { expectedVersion: version, assigneeId, assignmentReason: reason, operationId } :
      item.domain === 'problems' ? { version, assigneeId, assignmentReason: reason, operationId } : { version, assigneeId, reason, operationId };
    return { path: '/' + item.domain + '/' + item.id + (item.domain === 'problems' ? '' : '/assign'),
      method: item.domain === 'problems' ? 'PUT' as const : 'POST' as const, body };
  }
  async assign(item: Item, target: number, reason?: string) {
    const before = await this.detail(item); const req = this.assignment(item, before.version, target, reason);
    await this.write(req.method, req.path, req.body); return this.detail(item);
  }
  async action(item: Item, action: string, facts: Record<string, unknown> = {}) {
    const before = await this.detail(item);
    return this.write('POST', '/' + item.domain + '/' + item.id + '/' + action, {
      [item.domain === 'changes' ? 'expectedVersion' : 'version']: before.version, operationId: operation(), ...facts });
  }
  async relation(source: Item, target: Item, relationType: string, required = false, remove = false) {
    const current = await this.get('/tickets/' + source.workItemId);
    return this.write(remove ? 'DELETE' : 'POST', '/work-items/' + source.workItemId + '/relations',
      { sourceWorkItemId: source.workItemId, targetWorkItemId: target.workItemId, relationType,
        expectedVersion: current.version, operationId: operation(), metadata: { required } });
  }
  async changeTask(item: Item, action: string, node: string, facts: Record<string, unknown> = {}) {
    let selected: any;
    await expect.poll(async () => {
      selected = (await this.tasks(item)).find((task: any) => task.taskDefinitionKey === node && ['created','pending','in_progress','assigned','active'].includes(task.status));
      return Boolean(selected);
    }, {timeout:30000, message:'Current persisted task for '+node}).toBe(true);
    const before = await this.detail(item);
    const result = await this.action(item,action,{taskId:selected.taskId,...facts});
    expect(['completed','accepted','idempotent']).toContain(result.progress);
    if (result.progress === 'accepted') {
      await expect.poll(async () => (await this.detail(item)).version,{timeout:30000}).toBeGreaterThan(before.version);
    }
    return result;
  }
  private readIsolatedJSON(sql: string) {
    const {manifest}=isolatedEnvironment();
    const container=JSON.parse(execFileSync('docker',['inspect','--format','{{json .}}',manifest.postgresContainer],{encoding:'utf8'}));
    if((manifest.recoveryTarget && container.Id !== manifest.recoveryTarget.containerID) || container.Config.Labels?.['codex.workitem.v1']!==manifest.runId ||
       container.State.Running!==true || container.NetworkSettings.Ports['5432/tcp']?.[0]?.HostIp!=='127.0.0.1' ||
       container.NetworkSettings.Ports['5432/tcp']?.[0]?.HostPort!==String(manifest.ports[4])) throw new Error('Disposable database identity mismatch');
    return JSON.parse(execFileSync('docker',['exec',manifest.postgresContainer,'psql','-U','v1owner','-d','workitem_v1','-Atc',sql],{encoding:'utf8'}));
  }
  private inspectChange(item: Item, mode: 'assessment' | 'workflow') {
    if(item.domain!=='changes'||!Number.isSafeInteger(item.id)||item.id<=0||!Number.isSafeInteger(item.workItemId)||item.workItemId<=0) throw new Error('Professional Change ID required');
    // Read-only oracle: HTTP is the sole mutation path; never connect to any shared database.
    const selected="SELECT c.id,c.work_item_id,t.tenant_id FROM changes c JOIN tickets t ON t.id=c.work_item_id WHERE c.id="+item.id+" AND c.work_item_id="+item.workItemId+" AND t.record_class='change_request'";
    const sql=mode==='assessment'
      ? 'SELECT row_to_json(facts) FROM (SELECT assessment_digest,assessment_evidence,assessed_by,assessed_at FROM changes WHERE id='+item.id+' AND work_item_id='+item.workItemId+') facts'
      : "WITH selected AS ("+selected+") SELECT json_build_object("+
        "'events',COALESCE((SELECT json_agg(json_build_object('eventId',e.event_id,'status',e.status,'attemptCount',e.attempt_count,'publishedAt',e.published_at,'hasLastError',COALESCE(e.last_error,'')<>'','definitionId',e.payload->>'workflowDefinitionId','definitionKey',e.payload->>'workflowDefinitionKey','definitionVersion',e.payload->>'workflowDefinitionVersion')) FROM outbox_events e JOIN selected s ON e.tenant_id=s.tenant_id AND e.aggregate_id=s.work_item_id::text WHERE e.aggregate_type='work_item' AND e.event_type='workflow.start.requested'),'[]'::json),"+
        "'instances',COALESCE((SELECT json_agg(json_build_object('id',p.process_instance_id,'definitionId',p.process_definition_id,'definitionKey',p.process_definition_key,'businessKey',p.business_key,'status',p.status)) FROM process_instances p JOIN selected s ON p.tenant_id=s.tenant_id AND p.business_id=s.work_item_id WHERE p.business_type='change_request'),'[]'::json),"+
        "'snapshots',COALESCE((SELECT json_agg(json_build_object('definitionId',i.workflow_definition_id,'definitionKey',i.workflow_definition_key,'definitionVersion',i.workflow_definition_version,'definitionDigest',i.workflow_definition_digest,'noProcess',i.no_process)) FROM intake_resolution_snapshots i JOIN selected s ON i.tenant_id=s.tenant_id AND i.work_item_id=s.work_item_id),'[]'::json))";
    return this.readIsolatedJSON(sql);
  }
  async assertRequestedWorkflowDelivered(workItemId: number) {
    if(!Number.isSafeInteger(workItemId)||workItemId<=0) throw new Error('Requested Item WorkItem ID required');
    const sql="SELECT COALESCE(json_agg(json_build_object('eventId',e.event_id,'status',e.status,'publishedAt',e.published_at,'hasLastError',COALESCE(e.last_error,'')<>'')),'[]'::json) FROM outbox_events e JOIN tickets t ON t.id::text=e.aggregate_id AND t.tenant_id=e.tenant_id WHERE t.id="+workItemId+" AND t.record_class='service_request_item' AND e.aggregate_type='work_item' AND e.event_type='workflow.start.requested'";
    let events:any;
    try {
      await expect.poll(()=>{events=this.readIsolatedJSON(sql);return events.length===1&&events[0].status==='published';},{timeout:45000,intervals:[250,500,1000]}).toBe(true);
    } catch(error) {
      throw new Error('Requested Item workflow delivery did not complete: '+JSON.stringify(events),{cause:error});
    }
    expect(events).toHaveLength(1);
    expect(events[0].hasLastError).toBe(false);
    expect(Number.isFinite(Date.parse(events[0].publishedAt))).toBe(true);
  }
  workflowDeliveryFacts(item: Item) { return this.inspectChange(item,'workflow'); }
  persistedAssessment(item: Item) { return this.inspectChange(item,'assessment'); }
  assertChangeWorkflowOwnership(item: Item, submitted: boolean) {
    const facts=this.inspectChange(item,'workflow');
    // Change is owned by professional submit. Draft creation freezes the
    // binding but must never enqueue a competing workflow-start event.
    expect(facts.events,'No competing Change start event: '+JSON.stringify(facts)).toHaveLength(0);
    expect(facts.snapshots).toHaveLength(1);
    const snapshot=facts.snapshots[0];
    expect(snapshot.noProcess).toBe(false);
    expect(snapshot.definitionId).toBeGreaterThan(0);
    expect(snapshot.definitionKey).toBeTruthy();
    expect(snapshot.definitionVersion).toBeTruthy();
    expect(snapshot.definitionDigest).toMatch(/^[a-f0-9]{64}$/);
    expect(facts.instances,'Professional submit owns exactly one instance: '+JSON.stringify(facts)).toHaveLength(submitted?1:0);
    if(submitted) {
      expect(facts.instances[0].businessKey).toBe('change_request:'+item.workItemId);
      expect(facts.instances[0].definitionId).toBe(snapshot.definitionId);
      expect(facts.instances[0].definitionKey).toBe(snapshot.definitionKey);
    }
    return facts;
  }
  async tasks(item: Item) {
    const result = await this.get('/bpmn/tasks?page=1&pageSize=100&businessId='+item.workItemId+'&businessType='+(item.domain === 'changes' ? 'change_request' : item.domain === 'problems' ? 'problem' : 'incident'));
    return result.data.filter((task: any) => task.businessId === item.workItemId && task.businessType === (item.domain === 'changes' ? 'change_request' : item.domain === 'problems' ? 'problem' : 'incident'));
  }
}
export const test = businessTest.extend<{ journey: Journey }>({
  journey: async ({ request }, use, testInfo) => {
    // Keep the production 500/minute limiter enabled. Heavy journeys start in
    // a fresh rate window after preceding browser polling; never retry mutations.
    if(testInfo.title.startsWith('V1 cross-domain')||testInfo.title.startsWith('V1 generic')) {
      testInfo.setTimeout(300000);
      await new Promise(resolve=>setTimeout(resolve,61000));
    }
    const env = isolatedEnvironment(); await establishSession(request, env.credentials, env.apiURL);
    const journey = new Journey(request, env.apiURL); await use(journey);
    // The dedicated runner destroys only its disposable environment, never arbitrary rows.
    await testInfo.attach('workflow-delivery-state', {
      body:JSON.stringify(journey.created.filter(record=>record.kind==='changes').map(record=>({
        changeId:record.id,workItemId:record.workItemId,
        delivery:journey.workflowDeliveryFacts({domain:'changes',id:record.id,workItemId:record.workItemId!,number:''}),
      })),null,2),contentType:'application/json',
    });
    await testInfo.attach('created-workitems', { body: JSON.stringify(journey.created, null, 2), contentType: 'application/json' });
  },
});
export { expect };
