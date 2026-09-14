#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { execFileSync, spawnSync } = require('node:child_process');
const fixtures = require('./secret-scan-fixtures.json');

function validateResults(text, status) {
  if (status !== 0 && status !== 183) throw new Error('Secret scanner failed; results cannot be accepted.');
  const rows = text.split(/\r?\n/).filter(line => line.trim());
  if (status === 183 && rows.length === 0) throw new Error('Scanner reported findings without results.');
  let accepted = 0;
  for (const line of rows) {
    let row;
    try { row = JSON.parse(line); } catch { throw new Error('Malformed secret scanner output.'); }
    if (!row || typeof row !== 'object' || Array.isArray(row) ||
        typeof row.Raw !== 'string' || typeof row.Verified !== 'boolean' ||
        typeof row.DetectorName !== 'string' || typeof row.SourceMetadata?.Data?.Git?.file !== 'string') {
      throw new Error('Unexpected secret scanner result schema.');
    }
    const file = row.SourceMetadata.Data.Git.file;
    const hash = createHash('sha256').update(row.Raw, 'utf8').digest('hex');
    // These exact values are synthetic malformed-URL rejection fixtures on reserved
    // test domains. Verification errors do not turn them into real credentials.
    // A verified result, any other value/path, or any other detector still fails.
    if (row.Verified !== false || row.DetectorName !== 'URI' || fixtures[file] !== hash) {
      throw new Error('Secret scan found a result outside the reviewed synthetic fixtures.');
    }
    accepted++;
  }
  return accepted;
}

function run() {
  const event = JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  let base = '', head = process.env.GITHUB_SHA;
  if (process.env.GITHUB_EVENT_NAME === 'pull_request') {
    base = event.pull_request.base.sha;
    head = event.pull_request.head.sha;
  } else if (process.env.GITHUB_EVENT_NAME === 'push') {
    base = /^0+$/.test(event.before) ? '' : event.before;
    head = event.after;
  }
  for (const sha of [base, head].filter(Boolean)) {
    if (!/^[a-f0-9]{40}$/.test(sha)) throw new Error('Invalid scan revision.');
    execFileSync('git', ['cat-file', '-e', `${sha}^{commit}`], { stdio: 'pipe' });
  }
  if (!head || (base && base === head)) throw new Error('Empty scan range.');
  const temp = fs.mkdtempSync(path.join(process.env.RUNNER_TEMP || os.tmpdir(), 'itsm-secret-scan-'));
  let out, err;
  try {
    out = fs.openSync(path.join(temp, 'results.jsonl'), 'w', 0o600);
    err = fs.openSync(path.join(temp, 'scanner.log'), 'w', 0o600);
    const args = ['run', '--rm', '-v', `${process.cwd()}:/repo:ro`, '-w', '/repo',
      'ghcr.io/trufflesecurity/trufflehog:3.97.4', 'git', 'file:///repo/',
      '--branch', head, '--fail', '--fail-on-scan-errors', '--no-update', '--json'];
    if (base) args.push('--since-commit', base);
    const result = spawnSync('docker', args, { stdio: ['ignore', out, err] });
    fs.closeSync(out); out = undefined;
    fs.closeSync(err); err = undefined;
    const accepted = validateResults(fs.readFileSync(path.join(temp, 'results.jsonl'), 'utf8'), result.status);
    console.log(`Secret scan passed; ${accepted} exact synthetic URL fixture findings reviewed.`);
  } finally {
    if (out !== undefined) fs.closeSync(out);
    if (err !== undefined) fs.closeSync(err);
    fs.rmSync(temp, { recursive: true, force: true });
  }
}
if (require.main === module) {
  try { run(); } catch (error) {
    // Never print scanner output, raw findings, or child-process error details.
    console.error(error.message.startsWith('Secret scan') || error.message.startsWith('Scanner reported') ||
      error.message.startsWith('Malformed secret') || error.message.startsWith('Unexpected secret') ?
      error.message : 'Secret scan could not complete; inspect runner configuration.');
    process.exitCode = 1;
  }
}
module.exports = { validateResults };
