const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..');
const generator = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'generate_b4_sql.py');
const dataDir = path.join(repoRoot, 'scripts', 'migrate_config_seed', 'data');

function run(businessHours) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'b4-'));
  const dataPath = path.join(dir, 'bh.json');
  fs.writeFileSync(dataPath, JSON.stringify(businessHours), 'utf8');
  const outPath = path.join(dir, 'out.sql');
  execFileSync('python3', [generator, '--business-hours', dataPath, '--tenant-id', '1', '--out', outPath, '--batch-context', path.join(__dirname, 'fixtures/batch-context.json')], { encoding: 'utf8' });
  return fs.readFileSync(outPath, 'utf8');
}

test('emits a single guarded UPDATE on sla_definitions', () => {
  const sql = run({ work_days: [1, 2, 3, 4, 5], start_time: '09:00', end_time: '18:00', holiday_list: [] });
  const count = (sql.match(/^UPDATE sla_definitions /gm) || []).length;
  assert.equal(count, 1);
  assert.match(sql, /^BEGIN;$/m);
  assert.match(sql, /^COMMIT;$/m);
  assert.match(sql, /AND business_hours IS DISTINCT FROM '.*'::jsonb;/);
  for (const t of ['field_definitions', 'ticket_categories', 'configuration_items', 'departments', 'users']) {
    assert.ok(!sql.includes(`UPDATE ${t} `), `unexpected UPDATE ${t}`);
  }
});

test('committed business-hours data matches the parser contract', () => {
  const bh = JSON.parse(fs.readFileSync(path.join(dataDir, 'b4_business_hours.json'), 'utf8'));
  assert.deepEqual(bh.work_days, [1, 2, 3, 4, 5]);
  assert.match(bh.start_time, /^\d{2}:\d{2}$/);
  assert.match(bh.end_time, /^\d{2}:\d{2}$/);
  assert.equal(bh.time_zone, 'Asia/Shanghai');
  assert.equal(bh.holiday_list.length, 89);
  assert.equal(new Set(bh.holiday_list).size, 89);
  for (const d of bh.holiday_list) assert.match(d, /^20(24|25|26)-\d{2}-\d{2}$/);
});

test('makeup days are recorded separately and never inside holiday_list', () => {
  const bh = JSON.parse(fs.readFileSync(path.join(dataDir, 'b4_business_hours.json'), 'utf8'));
  const makeup = JSON.parse(fs.readFileSync(path.join(dataDir, 'b4_makeup_days.json'), 'utf8'));
  assert.equal(makeup.length, 19);
  for (const d of makeup) assert.ok(!bh.holiday_list.includes(d), `${d} must not be a holiday`);
});
