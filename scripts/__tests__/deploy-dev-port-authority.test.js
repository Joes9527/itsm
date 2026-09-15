'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const test = require('node:test');

const root = path.resolve(__dirname, '..', '..');

function writeExecutable(file, body) {
  fs.writeFileSync(file, body, { mode: 0o755 });
}

function createLocalRuntimeFixture() {
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'itsm-deploy-dev-port-'));
  const bin = path.join(fixture, 'bin');
  const scripts = path.join(fixture, 'scripts');
  const frontendBin = path.join(fixture, 'itsm-frontend', 'node_modules', '.bin');

  fs.mkdirSync(path.join(scripts, 'lib'), { recursive: true });
  fs.mkdirSync(path.join(fixture, 'itsm-backend'), { recursive: true });
  fs.mkdirSync(frontendBin, { recursive: true });
  fs.mkdirSync(bin, { recursive: true });
  fs.copyFileSync(path.join(root, 'scripts', 'deploy-dev.sh'), path.join(scripts, 'deploy-dev.sh'));
  fs.copyFileSync(path.join(root, 'scripts', 'lib', 'common.sh'), path.join(scripts, 'lib', 'common.sh'));

  writeExecutable(
    path.join(bin, 'docker'),
    '#!/bin/sh\n[ "$1" = "ps" ] && exit 0\nexit 1\n'
  );
  writeExecutable(
    path.join(bin, 'lsof'),
    '#!/bin/sh\ncase "$*" in *5432*|*6379*) exit 0;; *) exit 1;; esac\n'
  );
  writeExecutable(
    path.join(bin, 'curl'),
    `#!/bin/sh
url=""
for arg in "$@"; do url="$arg"; done
printf '%s\n' "$url" >> "$ITSM_TEST_CURL_LOG"
case "$url" in
  *8090*) [ -f "$ITSM_TEST_BACKEND_READY" ] && printf '200' && exit 0;;
  *3010*) [ -f "$ITSM_TEST_FRONTEND_READY" ] && printf '200' && exit 0;;
esac
exit 1
`
  );
  writeExecutable(
    path.join(bin, 'go'),
    `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then output="$2"; shift 2; else shift; fi
done
cat > "$output" <<'EOF'
#!/bin/sh
touch "$ITSM_TEST_BACKEND_READY"
while :; do sleep 1; done
EOF
chmod +x "$output"
`
  );
  writeExecutable(
    path.join(frontendBin, 'next'),
    `#!/bin/sh
printf '%s\n' "$*" > "$ITSM_TEST_NEXT_LOG"
touch "$ITSM_TEST_FRONTEND_READY"
while :; do sleep 1; done
`
  );

  return {
    fixture,
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      NO_COLOR: '1',
      ITSM_TEST_BACKEND_READY: path.join(fixture, 'backend.ready'),
      ITSM_TEST_FRONTEND_READY: path.join(fixture, 'frontend.ready'),
      ITSM_TEST_CURL_LOG: path.join(fixture, 'curl.log'),
      ITSM_TEST_NEXT_LOG: path.join(fixture, 'next.log'),
    },
  };
}

test('local startup launches and checks the ITSM frontend on port 3010', () => {
  const runtime = createLocalRuntimeFixture();
  const deploy = path.join(runtime.fixture, 'scripts', 'deploy-dev.sh');

  try {
    const up = spawnSync('bash', [deploy, 'up', '--local', '--skip-deps'], {
      cwd: runtime.fixture,
      env: runtime.env,
      encoding: 'utf8',
      timeout: 30_000,
    });

    assert.equal(up.status, 0, up.stderr || up.stdout);
    assert.equal(fs.readFileSync(runtime.env.ITSM_TEST_NEXT_LOG, 'utf8').trim(), 'dev --port 3010');
    assert.match(fs.readFileSync(runtime.env.ITSM_TEST_CURL_LOG, 'utf8'), /http:\/\/localhost:3010/);
    assert.match(up.stdout, /Frontend is healthy/);

    const secondUp = spawnSync('bash', [deploy, 'up', '--local', '--skip-deps'], {
      cwd: runtime.fixture,
      env: runtime.env,
      encoding: 'utf8',
      timeout: 15_000,
    });
    assert.equal(secondUp.status, 0, secondUp.stderr || secondUp.stdout);
    assert.match(secondUp.stdout, /Managed frontend already running on port 3010/);

    const down = spawnSync('bash', [deploy, 'down', '--local'], {
      cwd: runtime.fixture,
      env: runtime.env,
      encoding: 'utf8',
      timeout: 5_000,
    });
    assert.equal(down.status, 0, down.stderr || down.stdout);
    assert.match(down.stdout, /Frontend stopped/);
  } finally {
    spawnSync('bash', [deploy, 'down', '--local'], {
      cwd: runtime.fixture,
      env: runtime.env,
      encoding: 'utf8',
      timeout: 5_000,
    });
    fs.rmSync(runtime.fixture, { recursive: true, force: true });
  }
});
