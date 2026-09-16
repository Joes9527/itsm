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
