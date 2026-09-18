import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { submitApprovedAssignmentIntake, type AssignmentPreflightIO, type ApprovedAssignmentTarget } from '../work-item-assignment.test-utils';

test.use({ trace: 'off', video: 'off', screenshot: 'off' });

const xml = readFileSync(path.join(__dirname, '../fixtures/work-item-assignment.bpmn'), 'utf8');
const expected: ApprovedAssignmentTarget = { tenantId: 7, catalogId: 9, definitionKey: 'work_item_assignment_acceptance_owned', definitionId: 11, definitionVersion: '1.0.0', definitionSha256: createHash('sha256').update(xml).digest('hex'), configurationFrozen: true };
function setup() {
  let posts = 0;
  const catalog = { id: 9, targetClass: 'service_request_item', catalogVersion: 3, formSchemaVersion: 1, processDefinitionKey: expected.definitionKey };
  const definition = { id: 11, tenantId: 7, key: expected.definitionKey, version: '1.0.0', isActive: true, deploymentId: 12, bpmnXml: xml };
  const listing = { data: [definition], pagination: { page: 1, pageSize: 2, total: 1 } };
  const io: AssignmentPreflightIO<number> = {
    getCatalog: async () => catalog,
    getActiveDefinitions: async () => listing,
    submit: async snapshot => { expect(snapshot).toEqual(catalog); posts++; return 42; },
  };
  return { catalog, definition, listing, io, posts: () => posts };
}

test('submits only the sole active owned definition matching exact approved XML', async () => {
  const f = setup();
  expect(await submitApprovedAssignmentIntake(f.io, expected, xml)).toBe(42);
  expect(f.posts()).toBe(1);
});

for (const condition of ['empty-key', 'wrong-key', 'id', 'version', 'tenant', 'inactive', 'undeployed', 'missing', 'ambiguous', 'truncated', 'digest', 'not-frozen', 'read-error', 'definition-key', 'wrong-catalog', 'wrong-page', 'wrong-page-size'] as const) {
  test(`rejects ${condition} before intake POST`, async () => {
    const f = setup(); const target = { ...expected };
    switch (condition) {
      case 'empty-key': f.catalog.processDefinitionKey = ''; break;
      case 'wrong-key': f.catalog.processDefinitionKey = 'external_workflow'; break;
      case 'id': f.definition.id++; break;
      case 'version': f.definition.version = '2.0.0'; break;
      case 'tenant': f.definition.tenantId++; break;
      case 'inactive': f.definition.isActive = false; break;
      case 'undeployed': f.definition.deploymentId = 0; break;
      case 'missing': f.listing.data = []; f.listing.pagination.total = 0; break;
      case 'ambiguous': f.listing.data.push({ ...f.definition, id: 22 }); f.listing.pagination.total = 2; break;
      case 'truncated': f.listing.pagination.total = 2; break;
      case 'digest': target.definitionSha256 = '0'.repeat(64); break;
      case 'not-frozen': target.configurationFrozen = false; break;
      case 'definition-key': f.definition.key = 'unrelated_key'; break;
      case 'wrong-catalog': f.catalog.id++; break;
      case 'wrong-page': f.listing.pagination.page = 2; break;
      case 'wrong-page-size': f.listing.pagination.pageSize = 20; break;
      case 'read-error': f.io.getActiveDefinitions = async () => { throw new Error('definition unavailable'); }; break;
    }
    await expect(submitApprovedAssignmentIntake(f.io, target, xml)).rejects.toThrow();
    expect(f.posts()).toBe(0);
  });
}
for (const element of ['serviceTask', 'scriptTask', 'businessRuleTask', 'subProcess', 'callActivity']) {
  for (const forgedDigest of [false, true]) {
    test(`rejects ${element} (forged digest ${forgedDigest}) before POST`, async () => {
      const f = setup(); f.definition.bpmnXml = xml.replace('</process>', `<${element} id="unsafe"/></process>`);
      const target = { ...expected, definitionSha256: forgedDigest ? createHash('sha256').update(f.definition.bpmnXml).digest('hex') : expected.definitionSha256 };
      await expect(submitApprovedAssignmentIntake(f.io, target, xml)).rejects.toThrow();
      expect(f.posts()).toBe(0);
    });
  }
}
