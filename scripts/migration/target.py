"""The only module that talks to the database (read-only) and to the product API (writes)."""
from __future__ import annotations

import json
import os
import subprocess
import urllib.error
import urllib.request

from .profile import Credential, TargetSpec

LOGIN_PATH = '/api/v1/auth/login'
CSRF_PATH = '/api/v1/csrf-token'


class TargetError(Exception):
    """Raised when the target database or API can not be used as declared."""


class Target:
    def __init__(self, spec: TargetSpec, runner=None):
        self.spec = spec
        self.runner = runner or subprocess.run

    # --- read-only database -------------------------------------------------------------------
    def _argv(self, password: str) -> list[str]:
        if self.spec.access == 'docker':
            # the variable name only: the value travels through the child's environment, so it
            # never shows up in `ps` output for other users on the host
            return ['docker', 'exec', '-i', '-e', 'PGPASSWORD', self.spec.container,
                    'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-F', '|',
                    '-U', self.spec.user, '-d', self.spec.database]
        dsn = os.environ.get(self.spec.dsn_env or '')
        if not dsn:
            raise TargetError('environment variable %s is required for the target DSN'
                              % self.spec.dsn_env)
        return ['psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-F', '|', '-d', dsn]

    def query(self, sql: str) -> list[list[str]]:
        try:
            password = self.spec.credential.resolve('the target database')
        except Exception as exc:                                  # ProfileError
            raise TargetError(str(exc)) from exc
        statement = 'BEGIN READ ONLY;\n%s;\nCOMMIT;' % sql.rstrip().rstrip(';')
        environment = dict(os.environ, PGPASSWORD=password)
        completed = self.runner(self._argv(password), input=statement, text=True,
                                capture_output=True, env=environment)
        if completed.returncode != 0:
            raise TargetError('query failed (%s): %s'
                              % (sql.strip()[:60], completed.stderr.strip()[:200]))
        return [line.split('|') for line in completed.stdout.splitlines() if '|' in line]

    def query_rows(self, sql: str, columns: tuple[str, ...], parent_lookup=None) -> list[dict]:
        return [dict(zip(columns, row)) for row in self.query(sql)]

    def department_ids_by_code(self) -> dict[str, int]:
        """Map department code to id so a write plan can resolve `department_by:<field>`."""
        rows = self.query('SELECT code, id::text FROM departments')
        return {code: int(identifier) for code, identifier in rows if identifier.isdigit()}

    # --- product API (writes only) -------------------------------------------------------------
    def _api_request(self, request):
        with urllib.request.urlopen(request, timeout=30) as response:
            return response, response.headers.get_all('Set-Cookie') or []

    def api_login(self) -> str:
        credential = self.spec.api_credential or Credential()
        user = os.environ.get(self.spec.api_user_env or '', '')
        if not user:
            raise TargetError('target.api_user_env (%s) must be set in the environment'
                              % self.spec.api_user_env)
        password = credential.resolve('the product API')
        login = urllib.request.Request(
            '%s%s' % (self.spec.api_base, LOGIN_PATH),
            data=json.dumps({'tenantCode': 'default', 'username': user,
                             'password': password}).encode(),
            method='POST', headers={'Content-Type': 'application/json'})
        _, cookies = self._api_request(login)
        csrf = urllib.request.Request(
            '%s%s' % (self.spec.api_base, CSRF_PATH),
            headers={'Cookie': '; '.join(cookie.split(';')[0] for cookie in cookies)})
        response, _ = self._api_request(csrf)
        return json.loads(response.read().decode())['data']['csrf_token']

    def api_post(self, path: str, body: dict, csrf: str) -> tuple[int, dict]:
        request = urllib.request.Request(
            '%s%s' % (self.spec.api_base, path), data=json.dumps(body).encode(), method='POST',
            headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf})
        try:
            response, _ = self._api_request(request)
            return response.status, json.loads(response.read().decode())
        except urllib.error.HTTPError as exc:
            return exc.code, json.loads(exc.read().decode() or '{}')
