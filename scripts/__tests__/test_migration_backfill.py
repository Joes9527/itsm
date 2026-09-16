import pytest

from migration.backfill import preflight, run
from migration.checks.base import WriteIntent
from migration.profile import Credential, EntitySpec, WriteSpec


def spec_with(enabled=False, preflight_checks=('username_absent',),
              password_env='MIGRATED_DEFAULT_PASSWORD'):
    return EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                      keys_target='username', write=WriteSpec(
                          enabled=enabled, action='create_missing', endpoint='POST /api/v1/users',
                          fields={'username': 'userName'}, password=Credential(from_env=password_env),
                          preflight=list(preflight_checks),
                          rollback={'disable': 'PUT /api/v1/users/:id/status'}))


class FakeTarget:
    def __init__(self):
        self.posted = []
        self.logins = 0

    def api_login(self):
        self.logins += 1
        return 'csrf-%d' % self.logins

    def api_post(self, path, body, csrf):
        self.posted.append((path, body, csrf))
        return 200, {'code': 0, 'data': {'id': 100 + len(self.posted)}}


class FakeCheck:
    def __init__(self, intents):
        self.intents = intents

    def render_write_plan(self, src, tgt, spec):
        return self.intents


class RecordingEvidence:
    def __init__(self):
        self.keys = []

    def add(self, key, value):
        self.keys.append(key)
        setattr(self, key, value)


def test_preflight_blocks_on_an_existing_key():
    intents = [WriteIntent(key='A', payload={'username': 'A'})]
    allowed, blocked = preflight(intents, spec_with(), [{'username': 'A'}])
    assert allowed == []
    assert blocked[0]['key'] == 'A' and 'username_absent' in blocked[0]['reason']


def test_preflight_blocks_on_a_duplicate_email():
    intents = [WriteIntent(key='B', payload={'username': 'B', 'email': 'x@keas.kln.comm'})]
    spec = spec_with(preflight_checks=('email_absent',))
    allowed, blocked = preflight(intents, spec, [{'username': 'A', 'email': 'x@keas.kln.comm'}])
    assert allowed == [] and 'email_absent' in blocked[0]['reason']


def test_preflight_blocks_when_the_department_did_not_resolve():
    intents = [WriteIntent(key='B', payload={'username': 'B', 'departmentId': 0})]
    spec = spec_with(preflight_checks=('department_resolves',))
    allowed, blocked = preflight(intents, spec, [])
    assert allowed == [] and 'department_resolves' in blocked[0]['reason']


def test_preflight_passes_an_unknown_key():
    allowed, blocked = preflight([WriteIntent(key='B', payload={'username': 'B'})], spec_with(),
                                 [{'username': 'A'}])
    assert [intent.key for intent in allowed] == ['B'] and blocked == []


def test_dry_run_does_not_write(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(profile=None, entity_name='users', apply=False, target=target,
               check=FakeCheck([WriteIntent(key='B', payload={'username': 'B'})]),
               src=None, tgt=[{'username': 'A'}], evidence=None, spec=spec_with(enabled=True))
    assert code == 0 and target.posted == []


@pytest.mark.parametrize('enabled', [False, True])
def test_apply_is_blocked_without_target_binding_proof(monkeypatch, capsys, enabled):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(None, 'users', True, target,
               FakeCheck([WriteIntent(key='B', payload={'username':'B'})]),
               None, [], None, spec_with(enabled=enabled))
    assert code == 2 and target.posted == [] and target.logins == 0
    assert 'target binding' in capsys.readouterr().out


def test_blocked_preflight_returns_two_and_writes_nothing(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(profile=None, entity_name='users', apply=True, target=target,
               check=FakeCheck([WriteIntent(key='A', payload={'username': 'A'})]),
               src=None, tgt=[{'username': 'A'}], evidence=None, spec=spec_with(enabled=True))
    assert code == 2 and target.posted == []


def test_write_disabled_in_the_profile_does_nothing(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(profile=None, entity_name='users', apply=True, target=target,
               check=FakeCheck([WriteIntent(key='B', payload={'username': 'B'})]),
               src=None, tgt=[], evidence=None, spec=spec_with(enabled=False))
    assert code == 2 and target.posted == []


def test_nothing_to_create_reports_success(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    code = run(profile=None, entity_name='users', apply=False, target=FakeTarget(),
               check=FakeCheck([]), src=None, tgt=[], evidence=None, spec=spec_with(enabled=True))
    assert code == 0
