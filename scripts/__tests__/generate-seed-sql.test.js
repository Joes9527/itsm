const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..');
const generator = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'generate_seed_sql.py');

function fixtureSeed() {
  return {
    ticket_categories: [
      { name: "O'Brien 服务", code: 'L1', level: 1, sort_order: 1, description: 'root' },
      { name: '子类', code: 'L1-A', level: 2, sort_order: 1, parent_code: 'L1' },
    ],
    ticket_templates: [
      { name: '模板A', category: 'L1', priority: 'P3', category_codes: ['L1-A'], fields: [{ name: 'f1', label: '字段1', field_type: 'select', required: true, options: [{ label: 'x', value: 'x' }] }] },
    ],
    sla_definitions: [{ name: 'SLA1', response_time: 10, resolution_time: 60 }],
    service_catalog: [{ name: '目录1', target_class: 'service_request_item' }],
    ci_types: [{ name: 'business_system', description: '业务系统' }],
    standard_changes: [{ title: '变更1', implementation_plan: 'p', rollback_plan: 'r' }],
    known_errors: [{ title: 'KE1', status: 'template' }],
    ticket_tags: [{ name: '标签1', code: 'tag1' }],
    ticket_views: [{ name: '视图1', columns: ['id'] }],
    // must be excluded
    departments: [{ name: 'IT' }],
    teams: [{ name: 'L1' }],
    roles: [{ name: 'admin' }],
    process_bindings: [{ business_type: 'generic', process_definition_key: 'flow' }],
    sla_policies: [{ name: 'P1' }],
    incident_categories: [{ name: '硬件故障', code: 'hardware' }],
    incidents: [], problems: [], changes: [], knowledge_articles: [],
  };
}

function runGenerator(seed) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'b0-seed-'));
  const seedPath = path.join(dir, 'seed.json');
  const outPath = path.join(dir, 'out.sql');
  fs.writeFileSync(seedPath, JSON.stringify(seed), 'utf8');
  execFileSync('python3', [generator, '--seed', seedPath, '--tenant-id', '1', '--created-by', '1', '--out', outPath], { encoding: 'utf8' });
  return fs.readFileSync(outPath, 'utf8');
}

test('generates one INSERT per included row and wraps in a transaction', () => {
  const sql = runGenerator(fixtureSeed());
  const count = (re) => (sql.match(re) || []).length;
  assert.equal(count(/^INSERT INTO ticket_categories /gm), 2);
  assert.equal(count(/^INSERT INTO ticket_templates /gm), 1);
  assert.equal(count(/^INSERT INTO field_definitions /gm), 1);
  assert.equal(count(/^INSERT INTO sla_definitions /gm), 1);
  assert.equal(count(/^INSERT INTO service_catalogs /gm), 1);
  assert.equal(count(/^INSERT INTO ci_types /gm), 1);
  assert.equal(count(/^INSERT INTO standard_changes /gm), 1);
  assert.equal(count(/^INSERT INTO known_errors /gm), 1);
  assert.equal(count(/^INSERT INTO ticket_tags /gm), 1);
  assert.equal(count(/^INSERT INTO ticket_views /gm), 1);
  assert.match(sql, /^BEGIN;$/m);
  assert.match(sql, /^COMMIT;$/m);
});

test('excludes identity, history and out-of-scope sections', () => {
  const sql = runGenerator(fixtureSeed());
  for (const table of ['departments', 'teams', 'roles', 'process_bindings', 'sla_policies', 'incident_categories', 'incidents', 'problems', 'changes', 'knowledge_articles']) {
    assert.ok(!sql.includes(`INSERT INTO ${table} `), `unexpected INSERT INTO ${table}`);
  }
});

test('escapes single quotes in literals', () => {
  const sql = runGenerator(fixtureSeed());
  assert.match(sql, /'O''Brien 服务'/);
  assert.ok(!/INSERT INTO ticket_categories[^\n]*'O'Brien/.test(sql));
});

test('is idempotent: categories upsert on code, others guarded by NOT EXISTS', () => {
  const sql = runGenerator(fixtureSeed());
  assert.match(sql, /INSERT INTO ticket_categories [^;]*ON CONFLICT \(code\) DO NOTHING;/);
  assert.match(sql, /INSERT INTO ticket_templates [^;]*WHERE NOT EXISTS \(SELECT 1 FROM ticket_templates WHERE tenant_id=1 AND name='模板A'\);/);
});

test('parent categories are resolved by code and roots precede children', () => {
  const sql = runGenerator(fixtureSeed());
  const rootIdx = sql.indexOf("'L1', 1");
  const childIdx = sql.indexOf("'L1-A', 2");
  assert.ok(rootIdx > -1 && childIdx > rootIdx, 'root category must be emitted before its child');
  assert.match(sql, /\(SELECT id FROM ticket_categories WHERE code='L1'\)/);
});
