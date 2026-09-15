import textwrap
from pathlib import Path

import pytest

from migration.profile import ProfileError, load_profile


@pytest.fixture(autouse=True)
def register_entity_stub():
    """Profile loading validates entity names against the plug-in registry.

    Task 1 ships an empty registry, so the loader's check is exercised with a stub. Once a real
    plug-in registers the same name the stub is ignored (`setdefault`), so this stays valid later.
    """
    from migration.checks import REGISTRY
    REGISTRY.setdefault('departments', type('StubCheck', (), {'name': 'departments'})())
    yield


MINIMAL = """
version: 1
name: demo
source:
  dir: {dir}
  files:
    departments: {file: departments.json, records: 1, id_field: departmentId}
target:
  access: docker
  container: c
  user: u
  database: d
  credential: {from_env: TARGET_PW}
entities:
  - name: departments
    source: departments
    target_table: departments
    keys: {source: departmentId, target: code}
    structure_checks: [tree_single_root]
"""


def write_profile(tmp_path: Path, body: str, dept_rows: str = '[{"departmentId": "A"}]') -> Path:
    (tmp_path / 'departments.json').write_text(dept_rows, encoding='utf-8')
    path = tmp_path / 'p.yaml'
    # replace() rather than format(): injected snippets contain literal braces
    path.write_text(textwrap.dedent(body).replace('{dir}', str(tmp_path)), encoding='utf-8')
    return path


def test_loads_valid_profile(tmp_path):
    profile = load_profile(write_profile(tmp_path, MINIMAL))
    assert profile.version == 1
    assert profile.entities['departments'].keys_source == 'departmentId'
    assert profile.target.credential.from_env == 'TARGET_PW'


def test_unknown_top_level_key_is_rejected(tmp_path):
    bad = MINIMAL.replace('name: demo', 'name: demo\ntypo_key: 1')
    with pytest.raises(ProfileError, match='typo_key'):
        load_profile(write_profile(tmp_path, bad))


def test_unknown_rule_is_rejected(tmp_path):
    bad = MINIMAL.replace(
        'structure_checks: [tree_single_root]',
        'structure_checks: [tree_single_root]\n    field_checks: [{name: n, source: s, target: t, rule: guess}]')
    with pytest.raises(ProfileError, match='guess'):
        load_profile(write_profile(tmp_path, bad))


def test_unknown_entity_is_rejected(tmp_path):
    bad = MINIMAL.replace('- name: departments', '- name: tickets')
    with pytest.raises(ProfileError, match='tickets'):
        load_profile(write_profile(tmp_path, bad))


def test_missing_source_file_is_rejected(tmp_path):
    path = write_profile(tmp_path, MINIMAL)
    (tmp_path / 'departments.json').unlink()
    with pytest.raises(ProfileError, match='departments.json'):
        load_profile(path)


def test_record_mismatch_is_rejected_unless_allowed(tmp_path):
    three = '[{"departmentId": "A"}, {"departmentId": "B"}, {"departmentId": "C"}]'
    profile_path = write_profile(tmp_path, MINIMAL, dept_rows=three)
    with pytest.raises(ProfileError, match='records'):
        load_profile(profile_path)
    profile = load_profile(profile_path, allow_record_drift=True)
    assert profile.record_drift == {'departments': {'declared': 1, 'actual': 3}}


def test_hash_mismatch_is_rejected(tmp_path):
    bad = MINIMAL.replace('records: 1', 'sha256: deadbeef')
    with pytest.raises(ProfileError, match='sha256'):
        load_profile(write_profile(tmp_path, bad))
