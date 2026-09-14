const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { validateResults } = require('../secret-scan');
const fixtures = require('../secret-scan-fixtures.json');
const records = Object.keys(fixtures).map(file => {
  const content = fs.readFileSync(path.resolve(__dirname, '../..', file), 'utf8');
  const Raw = content.match(/https:\/\/[^"\s]+:[^"\s]+@[^"\s]+/)[0];
  return { Raw, Verified: false, DetectorName: 'URI', SourceMetadata: { Data: { Git: { file } } } };
});
const jsonl = rows => rows.map(row => JSON.stringify(row)).join('\n');
test('accepts clean scans and both exact synthetic fixtures', () => {
  assert.equal(validateResults('', 0), 0);
  assert.equal(validateResults(jsonl(records), 183), 2);
  assert.equal(validateResults(jsonl(records), 0), 2);
});
for (const [name, mutate] of Object.entries({
  verified: r => { r.Verified = true; },
  missingVerification: r => { delete r.Verified; },
  changedValue: r => { r.Raw += '/changed'; },
  changedDetector: r => { r.DetectorName = 'Other'; },
  changedPath: r => { r.SourceMetadata.Data.Git.file += '.other'; },
  missingMetadata: r => { delete r.SourceMetadata; },
})) {
  test(`rejects ${name}, even alongside an allowed fixture`, () => {
    const row = structuredClone(records[0]); mutate(row);
    assert.throws(() => validateResults(jsonl([records[1], row]), 183));
    assert.throws(() => validateResults(jsonl([row]), 0));
  });
}
test('fails closed on scanner failures, missing results, malformed JSON and schema', () => {
  for (const status of [null, 1, 2, 125, 137]) assert.throws(() => validateResults(jsonl(records), status));
  assert.throws(() => validateResults('', 183));
  for (const value of ['{', 'null', '[]', '{}', jsonl(records) + '\ninvalid']) {
    assert.throws(() => validateResults(value, 0));
  }
});
