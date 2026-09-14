const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..');
const generator = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'generate_process_sql.py');

const XML_TICKET = '<?xml version="1.0" encoding="UTF-8"?><definitions id="d"><process id="ticket_general_flow"/></definitions>\n';
const XML_CUSTOM = '<?xml version="1.0" encoding="UTF-8"?><definitions id="d"><process id="my_custom_flow"/></definitions>\n';

function run(bindings) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'proc-'));
  const bpmn = path.join(dir, 'bpmn');
  fs.mkdirSync(bpmn);
  fs.writeFileSync(path.join(bpmn, 'ticket_general_flow.bpmn'), XML_TICKET, 'utf8');
  fs.writeFileSync(path.join(bpmn, 'my_custom_flow.bpmn'), XML_CUSTOM, 'utf8');
  const seedPath = path.join(dir, 'seed.json');
  fs.writeFileSync(seedPath, JSON.stringify({ process_bindings: bindings }), 'utf8');
  const outPath = path.join(dir, 'out.sql');
  execFileSync('python3', [generator, '--bpmn-dir', bpmn, '--seed', seedPath, '--tenant-id', '1', '--out', outPath], { encoding: 'utf8' });
  return fs.readFileSync(outPath, 'utf8');
}

const BINDINGS = [
  { business_type: 'generic', process_definition_key: 'ticket_general_flow', is_default: true },
  { business_type: 'change_request', business_sub_type: 'emergency', process_definition_key: 'my_custom_flow', is_default: false },
];

test('emits one deployment + definition per template and one row per binding', () => {
  const sql = run(BINDINGS);
  const count = (re) => (sql.match(re) || []).length;
  assert.equal(count(/^INSERT INTO process_deployments /gm), 2);
  assert.equal(count(/^INSERT INTO process_definitions /gm), 2);
  assert.equal(count(/^INSERT INTO process_bindings /gm), 2);
  assert.match(sql, /^BEGIN;$/m);
  assert.match(sql, /^COMMIT;$/m);
});

test('maps known templates and falls back to key/default for unknown ones', () => {
  const sql = run(BINDINGS);
  assert.match(sql, /'ticket_general_flow', '通用工单流程', '通用工单处理流程', '1\.0\.0', 'ticket'/);
  assert.match(sql, /'my_custom_flow', 'my_custom_flow', '', '1\.0\.0', 'default'/);
});

test('bpmn_xml stores the base64 of the exact file bytes inside jsonb', () => {
  const sql = run(BINDINGS);
  const m = sql.match(/to_jsonb\('([A-Za-z0-9+/=]+)'::text\)/);
  assert.ok(m, 'expected a to_jsonb(base64) literal');
  const decoded = Buffer.from(m[1], 'base64').toString('utf8');
  assert.ok(decoded === XML_TICKET || decoded === XML_CUSTOM);
});

test('deployment ids are <key>-v1 and both inserts are guarded', () => {
  const sql = run(BINDINGS);
  assert.match(sql, /'ticket_general_flow-v1', '通用工单流程 v1'/);
  assert.match(sql, /WHERE NOT EXISTS \(SELECT 1 FROM process_deployments WHERE deployment_id='ticket_general_flow-v1'\)/);
  assert.match(sql, /WHERE NOT EXISTS \(SELECT 1 FROM process_definitions WHERE key='ticket_general_flow' AND tenant_id=1\)/);
});

test('bindings keep sub_type semantics and are idempotent-guarded', () => {
  const sql = run(BINDINGS);
  assert.match(sql, /business_sub_type IS NOT DISTINCT FROM NULL/);
  assert.match(sql, /business_sub_type IS NOT DISTINCT FROM 'emergency'/);
  assert.match(sql, /WHERE NOT EXISTS \(SELECT 1 FROM process_bindings WHERE tenant_id=1 AND business_type='generic' AND business_sub_type IS NOT DISTINCT FROM NULL AND process_definition_key='ticket_general_flow'\)/);
});
