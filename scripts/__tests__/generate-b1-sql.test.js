const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..');
const generator = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'generate_b1_sql.py');

const ASSETS = [
  { legacyCtiId: 'a'.repeat(32), legacyPath: 'KAPP系统', name: 'KAPP系统', ci_type: 'business_system', routeRefs: 0 },
  { legacyCtiId: 'b'.repeat(32), legacyPath: '基础架构 / 数据库', name: '数据库', ci_type: 'database', routeRefs: 11 },
];

function run(assets) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'b1-'));
  const assetsPath = path.join(dir, 'assets.json');
  fs.writeFileSync(assetsPath, JSON.stringify(assets), 'utf8');
  const outPath = path.join(dir, 'out.sql');
  execFileSync('python3', [generator, '--assets', assetsPath, '--tenant-id', '1', '--out', outPath, '--batch-context', path.join(__dirname, 'fixtures/batch-context.json')], { encoding: 'utf8' });
  return fs.readFileSync(outPath, 'utf8');
}

test('emits three new categories and one CI per asset', () => {
  const sql = run(ASSETS);
  const count = (re) => (sql.match(re) || []).length;
  assert.equal(count(/^INSERT INTO ticket_categories /gm), 3);
  assert.equal(count(/^INSERT INTO configuration_items /gm), 2);
  assert.match(sql, /^BEGIN;$/m);
  assert.match(sql, /^COMMIT;$/m);
});

test('new categories use the confirmed codes and parents', () => {
  const sql = run(ASSETS);
  assert.match(sql, /'COL-MAIL-004', 3, 40[^)]*'Request', 'P3', '标准服务'/);
  assert.match(sql, /'ACC-LCM-003', 3, 40[^)]*'P3', '标准服务'/);
  assert.match(sql, /'APP-GEN-SVC-001', 3, 40[^)]*'P3', '应用支持服务'/);
  assert.match(sql, /\(SELECT id FROM ticket_categories WHERE code='COL-MAIL' AND tenant_id=1\)/);
});

test('CI type id is resolved by name and legacy id guards idempotency', () => {
  const sql = run(ASSETS);
  assert.match(sql, /\(SELECT id FROM ci_types WHERE name='business_system' AND tenant_id=1 LIMIT 1\)/);
  assert.match(sql, /\(SELECT id FROM ci_types WHERE name='database' AND tenant_id=1 LIMIT 1\)/);
  assert.match(sql, /WHERE NOT EXISTS \(SELECT 1 FROM configuration_items WHERE tenant_id=1 AND attributes->>'legacyCtiId'='a{32}'\)/);
});

test('attributes carry linkage and source system', () => {
  const sql = run(ASSETS);
  const m = sql.match(/'(\{[^']*legacyCtiId[^']*\})'::jsonb/);
  assert.ok(m, 'expected a jsonb attributes literal');
  const attrs = JSON.parse(m[1].replace(/''/g, "'"));
  assert.equal(attrs.legacyCtiId, 'a'.repeat(32));
  assert.equal(attrs.legacyPath, 'KAPP系统');
  assert.equal(attrs.sourceSystem, 'keas-itsm-test');
  assert.equal(attrs.routeRefs, 0);
});

test('does not touch identity, history or mapping tables', () => {
  const sql = run(ASSETS);
  for (const t of ['departments', 'users', 'roles', 'permissions', 'work_item_migration_evidence', 'process_definitions']) {
    assert.ok(!sql.includes(`INSERT INTO ${t} `), `unexpected INSERT INTO ${t}`);
  }
});

test('committed asset data is clean and complete', () => {
  // Regression guard: an earlier extraction kept a trailing markdown "|" in names.
  const dataPath = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'data', 'b1_ci_assets.json');
  const assets = JSON.parse(fs.readFileSync(dataPath, 'utf8'));
  assert.equal(assets.length, 46);
  assert.equal(assets.filter((a) => a.ci_type === 'business_system').length, 43);
  assert.deepEqual(
    assets.filter((a) => a.ci_type !== 'business_system').map((a) => a.ci_type).sort(),
    ['database', 'network', 'server'],
  );
  assert.equal(new Set(assets.map((a) => a.legacyCtiId)).size, 46);
  for (const a of assets) {
    assert.match(a.legacyCtiId, /^[0-9a-f]{32}$/);
    assert.ok(a.name && !a.name.includes('|'), `bad name: ${JSON.stringify(a.name)}`);
    assert.ok(a.legacyPath && !a.legacyPath.includes('|'));
  }
});
