const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { parseRemovalOnlyFiles, testCandidatesFor } = require('../test-coverage-guard');
const mappings = require('../test-coverage-mappings.json');

test('only pure text removals are exempt; mixed edits, additions and binary changes stay guarded', () => {
  const result = parseRemovalOnlyFiles('0\t79\tremoved.go\n1\t79\tmixed.go\n3\t0\tadded.go\n-\t-\tbinary.go\n0\t0\tempty.go\n');
  assert.deepEqual([...result], ['removed.go']);
});
test('reviewed domain mappings resolve real source and test files', () => {
  for (const [source, tests] of Object.entries(mappings)) {
    assert.ok(fs.existsSync(path.resolve(__dirname, '../..', source)), source);
    assert.ok(tests.length > 0, source);
    assert.deepEqual(testCandidatesFor(source), tests);
    for (const file of tests) {
      assert.match(file, /(?:_test\.go|\.test\.tsx?)$/);
      assert.ok(fs.existsSync(path.resolve(__dirname, '../..', file)), file);
    }
  }
});
test('unmapped implementations retain normal filename requirements', () => {
  assert.deepEqual(testCandidatesFor('itsm-backend/service/new_feature.go'), [
    'itsm-backend/service/new_feature_test.go', 'itsm-backend/service/new_feature_test.go'
  ]);
});
