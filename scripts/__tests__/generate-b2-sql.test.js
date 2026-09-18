const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..');
const generator = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'generate_b2_sql.py');

function run() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'b2-'));
  const outPath = path.join(dir, 'out.sql');
  execFileSync('python3', [generator, '--tenant-id', '1', '--out', outPath, '--batch-context', path.join(__dirname, 'fixtures/batch-context.json')], { encoding: 'utf8' });
  return fs.readFileSync(outPath, 'utf8');
}

test('emits one guarded UPDATE per accepted new option', () => {
  const sql = run();
  const count = (sql.match(/^UPDATE field_definitions /gm) || []).length;
  assert.equal(count, 25); // 21 target_system + 2 service_type + 2 operation
  assert.match(sql, /^BEGIN;$/m);
  assert.match(sql, /^COMMIT;$/m);
});

test('targets field_definitions only and resolves the template by name', () => {
  const sql = run();
  assert.match(sql, /\(SELECT id FROM ticket_templates WHERE tenant_id = 1 AND name = '通用服务申请'\)/);
  for (const t of ['ticket_categories', 'configuration_items', 'departments', 'users', 'roles', 'sla_definitions']) {
    assert.ok(!sql.includes(`UPDATE ${t} `), `unexpected UPDATE ${t}`);
    assert.ok(!sql.includes(`INSERT INTO ${t} `), `unexpected INSERT INTO ${t}`);
  }
});

test('is idempotent via a jsonb containment guard', () => {
  const sql = run();
  assert.ok(
    sql.includes('AND NOT (f.options @> \'[{"label": "BMS系统", "value": "bms"}]\'::jsonb);'),
    'expected a containment guard for the BMS option',
  );
  assert.match(sql, /options = f\.options \|\| '\[\{"label": "群组邮箱", "value": "group_mailbox"\}\]'::jsonb/);
});

test('covers the accepted service_type and mailbox options', () => {
  const sql = run();
  assert.match(sql, /"label": "数据导出", "value": "data_export"/);
  assert.match(sql, /"label": "资产采购", "value": "asset_purchase"/);
  assert.match(sql, /"label": "公共邮箱", "value": "public_mailbox"/);
});
