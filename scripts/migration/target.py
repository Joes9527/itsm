"""The only module that talks to the database (read-only) and to the product API (writes)."""
from __future__ import annotations

import csv
import io
import json
from http.cookies import SimpleCookie
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
        if spec.scope not in {'tenant', 'full-database'}:
            raise TargetError('scope must be tenant or full-database')
        if spec.scope == 'tenant' and (type(spec.tenant_filter) is not int or spec.tenant_filter <= 0):
            raise TargetError('tenant scope requires a positive tenant_filter')
        if spec.scope == 'full-database' and spec.tenant_filter is not None:
            raise TargetError('full-database scope cannot include tenant_filter')
        self.spec = spec
        self._cookies = SimpleCookie()
        self.runner = runner or subprocess.run

    # --- read-only database -------------------------------------------------------------------
    def _argv(self, password: str) -> list[str]:
        if self.spec.access == 'docker':
            # the variable name only: the value travels through the child's environment, so it
            # never shows up in `ps` output for other users on the host
            return ['docker', 'exec', '-i', '-e', 'PGPASSWORD', self.spec.container,
                    'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t',
                    '-U', self.spec.user, '-d', self.spec.database]
        dsn = os.environ.get(self.spec.dsn_env or '')
        if not dsn:
            raise TargetError('environment variable %s is required for the target DSN'
                              % self.spec.dsn_env)
        return ['psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t']

    def query(self, sql: str) -> list[list[str]]:
        try:
            password = self.spec.credential.resolve('the target database')
        except Exception as exc:                                  # ProfileError
            raise TargetError(str(exc)) from exc
        statement = ("BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\n"
                     "SET LOCAL statement_timeout = '30s';\n"
                     "SET LOCAL lock_timeout = '2s';\n"
                     "SET LOCAL search_path = public, pg_catalog, pg_temp;\n"
                     "COPY (%s) TO STDOUT WITH (FORMAT CSV);\nCOMMIT;") % sql.rstrip().rstrip(';')
        environment = dict(os.environ, PGPASSWORD=password)
        if self.spec.access == 'dsn':
            environment['PGDATABASE'] = os.environ.get(self.spec.dsn_env or '', '')
        try:
            completed = self.runner(self._argv(password), input=statement, text=True,
                                    capture_output=True, env=environment, timeout=40)
        except (subprocess.SubprocessError, OSError) as exc:
            raise TargetError('read-only database command failed or timed out') from None
        if completed.returncode != 0:
            raise TargetError('read-only database query failed; inspect protected database logs')
        return list(csv.reader(io.StringIO(completed.stdout), strict=True))

    def query_rows(self, sql: str, columns: tuple[str, ...], parent_lookup=None) -> list[dict]:
        rows = self.query(sql)
        if any(len(row) != len(columns) for row in rows):
            raise TargetError('database result column count does not match the declared contract')
        return [dict(zip(columns, row)) for row in rows]

    def scope_predicate(self, alias: str) -> str:
        return ('true' if self.spec.scope == 'full-database'
                else '%s.tenant_id = %d' % (alias, self.spec.tenant_filter))

    def department_ids_by_code(self) -> dict[str, int]:
        """Map department code to id so a write plan can resolve `department_by:<field>`."""
        rows = self.query('SELECT d.code, d.id::text FROM departments d WHERE ' + self.scope_predicate('d'))
        if len({row[0] for row in rows}) != len(rows):
            raise TargetError('department business keys are ambiguous; use tenant scope')
        return {code: int(identifier) for code, identifier in rows if identifier.isdigit()}

    # --- product API (writes only) -------------------------------------------------------------
    def _api_request(self, request):
        if self._cookies:
            request.add_header('Cookie', '; '.join(m.OutputString(attrs=[]) for m in self._cookies.values()))
        with urllib.request.urlopen(request, timeout=30) as response:
            for cookie in response.headers.get_all('Set-Cookie') or []:
                self._cookies.load(cookie)
            payload = response.read()
            return response.status, payload

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
        status, payload = self._api_request(login)
        if status != 200 or json.loads(payload).get('code') != 0 or not self._cookies:
            raise TargetError('product API login did not establish an authenticated session')
        csrf = urllib.request.Request('%s%s' % (self.spec.api_base, CSRF_PATH))
        status, payload = self._api_request(csrf)
        if status != 200:
            raise TargetError('product API CSRF request failed')
        return json.loads(payload)['data']['csrf_token']

    def api_post(self, path: str, body: dict, csrf: str) -> tuple[int, dict]:
        request = urllib.request.Request(
            '%s%s' % (self.spec.api_base, path), data=json.dumps(body).encode(), method='POST',
            headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf})
        try:
            status, payload = self._api_request(request)
            return status, json.loads(payload)
        except urllib.error.HTTPError as exc:
            return exc.code, json.loads(exc.read().decode() or '{}')
