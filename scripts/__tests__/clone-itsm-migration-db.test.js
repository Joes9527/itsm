'use strict';

// Regression tests for scripts/clone_itsm_migration_db.sh.
//
// The clone tool runs against a live database container, so these tests drive it
// through a fake `docker` shim on PATH. The shim answers the exact psql / sh calls
// the script makes and records every invocation, letting us assert fail-closed
// behaviour without a real PostgreSQL instance.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const test = require('node:test');

const root = path.resolve(__dirname, '..', '..');
const SCRIPT = 'scripts/clone_itsm_migration_db.sh';
const SOURCE_DB = 'itsm';
const TARGET_DB = 'itsm_migration_20260914';

function fakeDockerEnvironment() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'itsm-clone-script-'));
  const state = path.join(dir, 'state');
  const log = path.join(dir, 'docker.log');
  const docker = path.join(dir, 'docker');
  fs.mkdirSync(state, { recursive: true });

  fs.writeFileSync(
    docker,
    `#!/bin/sh
printf '%s\\n' "$*" >> "$FAKE_LOG"
[ "$1" = "exec" ] && shift
container="$1"; shift
if [ "$1" = "psql" ]; then
  shift
  db=""; sql=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -U) shift 2 ;;
      -d) db="$2"; shift 2 ;;
      -t|-A) shift ;;
      -c) sql="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  lower=$(printf '%s' "$sql" | tr 'A-Z' 'a-z')
  case "$lower" in
    *pg_database*)
      if [ -f "$FAKE_STATE/target_exists" ]; then echo 1; fi
      ;;
    *"create database"*)
      case "$lower" in
        *template*)
          if [ -f "$FAKE_STATE/template_fail" ]; then
            echo "ERROR: source database is being accessed by other users" >&2
            exit 1
          fi
          ;;
      esac
      touch "$FAKE_STATE/target_exists"
      ;;
    *_migration_clone_manifest*)
      case "$lower" in
        *"create table"*) touch "$FAKE_STATE/marker" ;;
        *) if [ -f "$FAKE_STATE/marker" ]; then printf '%s\\n' "$FAKE_SOURCE_DB"; fi ;;
      esac
      ;;
    *count*)
      if [ "$db" = "$FAKE_SOURCE_DB" ] || [ ! -f "$FAKE_STATE/incomplete" ]; then
        echo 7
      else
        echo "ERROR: relation \\"ticket_templates\\" does not exist" >&2
        exit 1
      fi
      ;;
  esac
  exit 0
fi
if [ "$1" = "sh" ]; then
  shift
  [ "$1" = "-c" ] && shift
  cmd="$1"
  case "$cmd" in
    *pg_dump*) exit 0 ;;
    *pg_restore*)
      if [ -f "$FAKE_STATE/restore_fail" ]; then
        echo "pg_restore: error: connection to server failed" >&2
        touch "$FAKE_STATE/incomplete"
        exit 1
      fi
      touch "$FAKE_STATE/marker"
      exit 0
      ;;
  esac
fi
exit 0
`,
    { mode: 0o755 }
  );

  return {
    state,
    log,
    env: {
      ...process.env,
      PATH: `${dir}:${process.env.PATH}`,
      FAKE_LOG: log,
      FAKE_STATE: state,
      FAKE_SOURCE_DB: SOURCE_DB,
      NO_COLOR: '1',
    },
    readLog() {
      return fs.existsSync(log) ? fs.readFileSync(log, 'utf8') : '';
    },
    mark(name) {
      fs.writeFileSync(path.join(state, name), '');
    },
  };
}

function run(fixture, args, extraEnv = {}) {
  return spawnSync('bash', [SCRIPT, ...args], {
    cwd: root,
    env: { ...fixture.env, ...extraEnv },
    encoding: 'utf8',
  });
}

test('rejects an abnormal target database identifier before invoking docker', () => {
  const fixture = fakeDockerEnvironment();
  const result = run(fixture, [`${TARGET_DB};DROP DATABASE itsm`]);

  assert.equal(result.status, 2, result.stderr || result.stdout);
  assert.match(result.stderr, /invalid/i);
  assert.equal(fixture.readLog(), '', 'no docker call may happen before identifier validation');
});

test('rejects uppercase database aliases before any destructive or read operation', () => {
  for (const args of [['ITSM', 'itsm'], [TARGET_DB, 'ITSM']]) {
    const fixture = fakeDockerEnvironment();
    fixture.mark('target_exists');
    fixture.mark('incomplete');
    const result = run(fixture, args, { RECREATE_INCOMPLETE: '1' });
    assert.equal(result.status, 2, result.stderr || result.stdout);
    assert.equal(fixture.readLog(), '', 'ambiguous names must never reach docker');
  }
});

test('rejects identical source and target database names', () => {
  const fixture = fakeDockerEnvironment();
  const result = run(fixture, [SOURCE_DB, SOURCE_DB]);

  assert.equal(result.status, 2, result.stderr || result.stdout);
  assert.match(result.stderr, /invalid|same|equal|differ/i);
  assert.equal(fixture.readLog(), '', 'no docker call may happen before name validation');
});

test('refuses to report success for an existing but incomplete target database', () => {
  const fixture = fakeDockerEnvironment();
  fixture.mark('target_exists');
  fixture.mark('incomplete');

  const result = run(fixture, [TARGET_DB, SOURCE_DB]);

  assert.notEqual(result.status, 0, 'an incomplete target must not exit zero');
  assert.match(result.stderr, /incomplete|verification|failed/i);
  assert.doesNotMatch(fixture.readLog(), /CREATE DATABASE/i, 'must not overwrite without explicit opt-in');
});

test('skips cleanly when an existing target passes full verification', () => {
  const fixture = fakeDockerEnvironment();
  fixture.mark('target_exists');
  fixture.mark('marker');

  const result = run(fixture, [TARGET_DB, SOURCE_DB]);

  assert.equal(result.status, 0, result.stderr || result.stdout);
  assert.match(result.stdout, /skip|verified/i);
  assert.doesNotMatch(fixture.readLog(), /CREATE DATABASE/i);
});

test('fresh clone creates, records a verification marker and confirms completeness', () => {
  const fixture = fakeDockerEnvironment();
  const result = run(fixture, [TARGET_DB, SOURCE_DB]);

  assert.equal(result.status, 0, result.stderr || result.stdout);
  const log = fixture.readLog();
  assert.match(log, /CREATE DATABASE[^\n]*TEMPLATE/i);
  assert.match(log, /_migration_clone_manifest/i, 'a verification marker must be written');
  assert.match(result.stdout, /clone|verified|complete/i);
});

test('restore failure is not reported as success and a rerun keeps failing', () => {
  const fixture = fakeDockerEnvironment();
  fixture.mark('template_fail');
  fixture.mark('restore_fail');

  const first = run(fixture, [TARGET_DB, SOURCE_DB]);
  assert.notEqual(first.status, 0, 'a failed restore must exit non-zero');
  assert.match(fixture.readLog(), /pg_restore/);

  const second = run(fixture, [TARGET_DB, SOURCE_DB]);
  assert.notEqual(second.status, 0, 'a rerun over a half-restored target must not report success');
  assert.match(second.stderr, /incomplete|verification|failed/i);
});
