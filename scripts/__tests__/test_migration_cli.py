import json
from pathlib import Path

from migration import __main__ as cli

FIXTURES = Path('scripts/migration/fixtures')


def test_exit_code_precedence_prefers_preflight_then_drift_then_unattributed():
    assert cli.combine_exit_codes([]) == cli.EXIT_OK
    assert cli.combine_exit_codes([cli.EXIT_UNATTRIBUTED]) == cli.EXIT_UNATTRIBUTED
    assert cli.combine_exit_codes([cli.EXIT_UNATTRIBUTED, cli.EXIT_DRIFT]) == cli.EXIT_DRIFT
    assert cli.combine_exit_codes([cli.EXIT_DRIFT, cli.EXIT_PREFLIGHT]) == cli.EXIT_PREFLIGHT
    assert cli.combine_exit_codes([cli.EXIT_PREFLIGHT, cli.EXIT_ERROR]) == cli.EXIT_ERROR


def test_unknown_command_lists_the_available_ones(capsys):
    assert cli.main(['nonsense']) == cli.EXIT_ERROR
    printed = capsys.readouterr().out
    for command in ('verify', 'self-test', 'backfill', 'verify-profile'):
        assert command in printed


def test_missing_profile_is_an_error(capsys):
    assert cli.main(['verify']) == cli.EXIT_ERROR
    assert '--profile' in capsys.readouterr().out


def test_self_test_runs_offline_and_passes(capsys):
    assert cli.main(['self-test']) == cli.EXIT_OK
    assert 'self-test ok' in capsys.readouterr().out


def test_verify_against_the_offline_fixture_reports_the_expected_counts(tmp_path, capsys):
    out = tmp_path / 'evidence.json'
    code = cli.main(['verify', '--profile', str(FIXTURES / 'self_test.yaml'),
                     '--offline-fixture', '--evidence-out', str(out)])
    assert code in {cli.EXIT_OK, cli.EXIT_UNATTRIBUTED}
    payload = json.loads(out.read_text())
    expected = json.loads((FIXTURES / 'expected-evidence.json').read_text())
    assert payload['entities']['departments']['matched'] == expected['entities']['departments']['matched']
    assert payload['entities']['users']['matched'] == expected['entities']['users']['matched']
    assert payload['exit_code'] == code


def test_tree_and_discriminate_run_against_the_fixture(tmp_path):
    for command, entity in (('tree', 'departments'), ('discriminate', 'users')):
        out = tmp_path / ('%s.json' % command)
        args = [command, '--profile', str(FIXTURES / 'self_test.yaml'), '--offline-fixture',
                '--entity', entity, '--evidence-out', str(out)]
        assert cli.main(args) == cli.EXIT_OK, command
        assert out.exists(), command


def test_backfill_dry_run_against_the_fixture_needs_no_credential(tmp_path):
    out = tmp_path / 'backfill.json'
    args = ['backfill', '--profile', str(FIXTURES / 'self_test.yaml'), '--offline-fixture',
            '--entity', 'users', '--evidence-out', str(out)]
    assert cli.main(args) == cli.EXIT_OK


def test_verify_profile_aggregates_all_entities_and_honors_selection(tmp_path):
    for selected in ([], ['--entity','users']):
        out=tmp_path/('selected.json' if selected else 'both.json')
        code=cli.main(['verify-profile','--profile',str(FIXTURES/'self_test.yaml'),
                       '--offline-fixture','--evidence-out',str(out),*selected])
        evidence=json.loads(out.read_text())
        assert code==cli.EXIT_OK
        assert evidence['drift']==[]
        assert set(evidence['measurements'])==({'users'} if selected else {'users','departments'})


def test_lineage_preserves_tenant_scope():
    from types import SimpleNamespace
    from migration.profile import Credential, TargetSpec, LineageSpec
    profile=SimpleNamespace(target=TargetSpec(access='docker',container='target',user='u',database='d',
                            credential=Credential(),scope='tenant',tenant_filter=7),
                            lineage=[LineageSpec(label='ancestor',container='clone',user='u',database='old',credential=Credential())])
    target=cli._lineage_targets(profile)[0][1]
    assert target.spec.scope=='tenant' and target.spec.tenant_filter==7


def test_verify_cycle_failure_cannot_be_overridden_as_unattributed(monkeypatch,tmp_path):
    original=cli._offline_target
    def corrupt():
        obj=original()
        read=obj.query_rows
        def rows(sql,columns,parent_lookup=None):
            result=read(sql,columns,parent_lookup)
            if 'departments' in sql:
                for row in result:row['parent_id']=row['code']
            return result
        obj.query_rows=rows
        return obj
    monkeypatch.setattr(cli,'_offline_target',corrupt)
    out=tmp_path/'cycle.json'
    code=cli.main(['verify','--profile',str(FIXTURES/'self_test.yaml'),'--offline-fixture',
                   '--entity','departments','--allow-unattributed','--evidence-out',str(out)])
    assert code==cli.EXIT_PREFLIGHT
    assert 'tree_single_root' in json.loads(out.read_text())['entities']['departments']['failures']


def test_verify_field_mismatch_and_undeclared_unresolvable_are_failures(monkeypatch,tmp_path):
    from dataclasses import replace
    from migration.profile import FieldCheck
    original=cli._load
    def load(args):
        profile,indexes=original(args)
        spec=replace(profile.entities['users'],field_checks=[FieldCheck('name','realName','role'),
            FieldCheck('missing-map','status','active','map',map={}),
            FieldCheck('accepted-unknown','leaderId','manager_id','unresolvable')])
        return replace(profile,entities={'users':spec}),indexes
    monkeypatch.setattr(cli,'_load',load)
    out=tmp_path/'fields.json'
    code=cli.main(['verify','--profile',str(FIXTURES/'self_test.yaml'),'--offline-fixture',
                  '--evidence-out',str(out),'--allow-unattributed'])
    assert code==cli.EXIT_PREFLIGHT
    failures=json.loads(out.read_text())['entities']['users']['failures']
    assert 'field:name' in failures and 'field:missing-map' in failures
    assert 'field:accepted-unknown' not in failures
