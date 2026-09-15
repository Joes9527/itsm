import json

import pytest

from migration.profile import Credential, TargetSpec
from migration.target import Target, TargetError

SPEC = TargetSpec(access='docker', user='ga_owner', database='itsm_ga_ready',
                  container='ga-itsm-20260914', credential=Credential(from_env='TARGET_PW'),
                  api_base='http://localhost:3010', api_user_env='ITSM_ADMIN_USER',
                  api_credential=Credential(from_env='ADMIN_PW'))


class Recorder:
    def __init__(self, stdout='A|B\n', code=0):
        self.calls = []
        self.stdout = stdout
        self.code = code

    def __call__(self, argv, **kwargs):
        self.calls.append((argv, kwargs))
        return type('R', (), {'returncode': self.code, 'stdout': self.stdout, 'stderr': 'err'})()


class FakeResponse:
    status = 200

    def __init__(self, payload, cookies=()):
        self._payload = json.dumps(payload).encode()
        self.headers = type('H', (), {'get_all': lambda self, name: list(cookies)})()

    def read(self):
        return self._payload

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def test_query_wraps_in_a_read_only_transaction(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder('1|2\n')
    target = Target(SPEC, runner=runner)
    assert target.query('SELECT 1, 2') == [['1', '2']]
    sent = runner.calls[0][1]['input']
    assert sent.startswith('BEGIN READ ONLY;')
    assert sent.rstrip().endswith('COMMIT;')


def test_password_never_appears_in_argv(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder()
    Target(SPEC, runner=runner).query('SELECT 1')
    argv = runner.calls[0][0]
    assert 'secret' not in ' '.join(argv), 'the password must not be visible in argv'
    assert 'PGPASSWORD' in argv, 'the variable name is forwarded so docker can read it from the env'
    assert runner.calls[0][1]['env']['PGPASSWORD'] == 'secret'


def test_missing_credential_env_is_reported(monkeypatch):
    monkeypatch.delenv('TARGET_PW', raising=False)
    with pytest.raises(TargetError, match='TARGET_PW'):
        Target(SPEC, runner=Recorder()).query('SELECT 1')


def test_query_error_is_raised_with_the_statement(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    with pytest.raises(TargetError, match='SELECT bad'):
        Target(SPEC, runner=Recorder(code=1)).query('SELECT bad')


def test_query_rows_maps_columns(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder('A|Root|\nB|Child|A\n')
    rows = Target(SPEC, runner=runner).query_rows('SELECT ...', ('code', 'name', 'parent_id'))
    assert rows == [{'code': 'A', 'name': 'Root', 'parent_id': ''},
                    {'code': 'B', 'name': 'Child', 'parent_id': 'A'}]


def test_department_ids_by_code_maps_codes_to_ids(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    target = Target(SPEC, runner=Recorder('U1|635\nU2|not-a-number\n'))
    assert target.department_ids_by_code() == {'U1': 635}


def test_dsn_access_resolves_the_dsn_from_the_environment(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    monkeypatch.setenv('TARGET_DSN', 'postgres://u@h/db')
    spec = TargetSpec(access='dsn', user='u', database='d', dsn_env='TARGET_DSN',
                      credential=Credential(from_env='TARGET_PW'))
    runner = Recorder()
    Target(spec, runner=runner).query('SELECT 1')
    assert runner.calls[0][0][-1] == 'postgres://u@h/db'


def test_dsn_access_without_the_variable_is_reported(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    monkeypatch.delenv('TARGET_DSN', raising=False)
    spec = TargetSpec(access='dsn', user='u', database='d', dsn_env='TARGET_DSN',
                      credential=Credential(from_env='TARGET_PW'))
    with pytest.raises(TargetError, match='TARGET_DSN'):
        Target(spec, runner=Recorder()).query('SELECT 1')


def test_api_login_requires_the_user_environment(monkeypatch):
    monkeypatch.setenv('ADMIN_PW', 'pw')
    monkeypatch.delenv('ITSM_ADMIN_USER', raising=False)
    with pytest.raises(TargetError, match='ITSM_ADMIN_USER'):
        Target(SPEC, runner=Recorder()).api_login()


def test_api_login_returns_a_csrf_token_and_post_sends_it(monkeypatch):
    monkeypatch.setenv('ADMIN_PW', 'pw')
    monkeypatch.setenv('ITSM_ADMIN_USER', 'admin')
    seen = []

    def fake_urlopen(request, timeout=None):
        seen.append(request)
        if request.full_url.endswith('/api/v1/auth/login'):
            return FakeResponse({'code': 0}, cookies=['session=abc; Path=/'])
        if request.full_url.endswith('/api/v1/csrf-token'):
            return FakeResponse({'code': 0, 'data': {'csrf_token': 'tok-1'}})
        return FakeResponse({'code': 0, 'data': {'id': 42}})

    monkeypatch.setattr('urllib.request.urlopen', fake_urlopen)
    target = Target(SPEC, runner=Recorder())
    csrf = target.api_login()
    assert csrf == 'tok-1'
    status, payload = target.api_post('/api/v1/users', {'username': 'X'}, csrf)
    assert (status, payload['data']['id']) == (200, 42)
    assert seen[-1].get_header('X-csrf-token') == 'tok-1'
    assert 'pw' not in str(seen[-1].data)
