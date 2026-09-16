import json
from dataclasses import replace
from types import SimpleNamespace

import pytest

from migration.target import Target, TargetError
from migration.profile import TargetSpec, Credential, EntitySpec
from migration.checks.departments import DepartmentsCheck
from migration.checks.users import UsersCheck
from migration.backfill import run
from migration.checks.base import WriteIntent


def target_spec(**kwargs):
    return replace(TargetSpec(access='docker', container='fixture', user='reader', database='fixture',
                              credential=Credential(from_env='TEST_DB_PASSWORD'),
                              scope='tenant', tenant_filter=7), **kwargs)


def test_read_protocol_preserves_delimiters_newlines_and_sets_timeouts(monkeypatch):
    monkeypatch.setenv('TEST_DB_PASSWORD', 'secret')
    calls = []
    def runner(argv, **kw):
        calls.append((argv, kw))
        return SimpleNamespace(returncode=0, stderr='', stdout='"a|b","two\nlines","quote""x"\n')
    rows = Target(target_spec(), runner).query_rows('SELECT x,y,z FROM fixture', ('a','b','c'))
    assert rows == [{'a':'a|b', 'b':'two\nlines', 'c':'quote"x'}]
    sql = calls[0][1]['input']
    assert 'REPEATABLE READ READ ONLY' in sql
    assert "statement_timeout = '30s'" in sql
    assert "lock_timeout = '2s'" in sql


@pytest.mark.parametrize('scope,tenant', [('tenant',None),('bogus',7),('tenant',True),('tenant',0),('full-database',7)])
def test_invalid_target_scope_fails_closed(scope, tenant):
    with pytest.raises(TargetError):
        Target(target_spec(scope=scope, tenant_filter=tenant))


def test_entity_queries_use_tenant_and_department_parent_business_key(monkeypatch):
    monkeypatch.setenv('TEST_DB_PASSWORD', 'secret')
    sql = []
    def runner(argv, **kw):
        sql.append(kw['input'])
        return SimpleNamespace(returncode=0, stderr='', stdout='')
    t = Target(target_spec(), runner)
    for name, check in [('departments',DepartmentsCheck()),('users',UsersCheck())]:
        check.fetch_target(t, EntitySpec(name=name,source=name,target_table=name,keys_source='id',keys_target='code'))
    assert 'p.code' in sql[0] and 'p.tenant_id = d.tenant_id' in sql[0]
    assert 'd.tenant_id = 7' in sql[0]
    assert 'u.tenant_id = 7' in sql[1]


def test_query_error_does_not_echo_data_or_credentials(monkeypatch):
    monkeypatch.setenv('TEST_DB_PASSWORD','secret')
    runner=lambda *a,**k:SimpleNamespace(returncode=1,stdout='',stderr='person@example.test secret')
    with pytest.raises(TargetError) as error:
        Target(target_spec(),runner).query("SELECT 'person@example.test'")
    assert 'person@' not in str(error.value) and 'secret' not in str(error.value)


def test_dry_run_output_and_evidence_do_not_contain_personal_data(capsys):
    from migration.profile import WriteSpec
    spec=EntitySpec(name='users',source='users',target_table='users',keys_source='id',keys_target='username',
        write=WriteSpec(enabled=True,action='create_missing',endpoint='POST /api/v1/users',fields={},
                        password=Credential(),preflight=[],rollback={}))
    raw={'username':'private-user','email':'private@example.test','name':'Private Name'}
    check=SimpleNamespace(render_write_plan=lambda *a:[WriteIntent(key='private-user',payload=raw)])
    captured={}
    evidence=SimpleNamespace(add=lambda k,v:captured.update({k:v}))
    assert run(None,'users',False,None,check,None,[],evidence,spec)==0
    text=capsys.readouterr().out+json.dumps(captured)
    assert all(value not in text for value in raw.values())


def test_scope_rejected_when_loading_profile_before_any_target(tmp_path):
    from test_migration_profile import MINIMAL, write_profile
    from migration.profile import load_profile, ProfileError
    with pytest.raises(ProfileError, match='tenant_filter'):
        load_profile(write_profile(tmp_path, MINIMAL.replace("  tenant_filter: 1\n", "")))


def test_privacy_refusal_does_not_repeat_the_rejected_personal_value():
    from migration.report import assert_pii_free, PIIError
    with pytest.raises(PIIError) as error:
        assert_pii_free({'sample':'private@example.test'})
    assert 'private@example.test' not in str(error.value)


def test_target_row_width_mismatch_is_rejected(monkeypatch):
    monkeypatch.setenv('TEST_DB_PASSWORD','secret')
    runner=lambda *a,**k:SimpleNamespace(returncode=0,stdout='one,two,three\n',stderr='')
    with pytest.raises(TargetError, match='column count'):
        Target(target_spec(),runner).query_rows('SELECT a,b',('a','b'))


def test_user_tenant_structure_uses_the_declared_tenant():
    spec=EntitySpec(name='users',source='users',target_table='users',keys_source='id',keys_target='username', expected_tenant=7)
    facts=UsersCheck().check_structure([{'username':'fixture','tenant_id':'7','hash_prefix':'$2b$'}],spec)
    assert facts['foreign_tenant_rows']==0


def test_profile_refuses_sql_in_target_table(tmp_path):
    from test_migration_profile import MINIMAL, write_profile
    from migration.profile import load_profile, ProfileError
    with pytest.raises(ProfileError, match='target_table'):
        load_profile(write_profile(tmp_path, MINIMAL.replace('target_table: departments',
            'target_table: "departments); COMMIT; SELECT 1 --"')))
