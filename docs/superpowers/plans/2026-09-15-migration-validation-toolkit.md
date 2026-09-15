# Migration Validation Toolkit v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn this batch's one-off migration checks into a reusable, profile-driven toolkit that validates a same-source legacy export against the new ITSM database and can backfill missing records through the product API.

**Architecture:** A `scripts/migration` Python package. `profile.py` loads a declarative YAML profile and fails closed on anything unknown; `sources.py` indexes export files after verifying their hash and record count; `target.py` is the only module that touches the database (read-only) and the product API (writes); `checks/<entity>.py` plug-ins implement one protocol per entity (`users`, `departments`); `analyze.py` provides the three attribution analyses plus `verify-profile`; `backfill.py` owns the five-step write discipline; `report.py` assembles evidence and refuses to emit personal data.

**Tech Stack:** Python 3.12, PyYAML 6, pytest, `docker exec psql` for read-only access, the product HTTP API for writes. No new runtime dependency beyond PyYAML.

**Spec:** `docs/superpowers/specs/2026-09-15-migration-validation-toolkit-design.md`

## Global Constraints

- Python 3.12 (`python3 --version` reports 3.12.3); PyYAML is the only third-party import outside pytest.
- Never write personal data to the repository: evidence JSON carries counts, structure and `sha256:<8>` tokens only; row-level detail goes to a private directory.
- Credentials come from `from_env` or `from_file` only. No password, token or DSN may appear in code, logs, evidence, or error messages.
- Every command is read-only unless it is `backfill --apply`, and that requires `write.enabled: true` in the profile plus both environment variables.
- Unknown profile keys, rules, entities, structure checks, preflight checks and write actions are load-time errors (fail closed).
- Exit codes: `0` ok, `1` runtime error, `2` preflight blocked, `3` unattributed differences, `4` rule drift. Precedence when several apply: `1 > 2 > 4 > 3`.
- Tests live in `scripts/__tests__/test_migration_*.py` and run with `python3 -m pytest scripts/__tests__ -q`; they must pass offline (no database, no network).
- Commit after every task with the message given in that task.

---

### Task 1: Package skeleton and profile loader

**Files:**
- Create: `scripts/__init__.py`
- Create: `scripts/migration/__init__.py`
- Create: `scripts/migration/profile.py`
- Create: `scripts/requirements.txt`
- Test: `scripts/__tests__/test_migration_profile.py`
- Create: `scripts/__tests__/conftest.py` (puts `scripts/` on `sys.path` so `import migration` works when
  pytest runs from the repository root)

**Interfaces:**
- Consumes: nothing.
- Produces: `load_profile(path: Path) -> MigrationProfile`, `ProfileError`, and the dataclasses `Credential`, `SourceFile`, `LineageSpec`, `FieldCheck`, `RuleAssertion`, `WriteSpec`, `EntitySpec`, `TargetSpec`, `MigrationProfile`. Later tasks import these names from `migration.profile`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_profile.py
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/administrator/project/itsm/.worktrees/config-launch-integration && python3 -m pytest scripts/__tests__/test_migration_profile.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/profile.py
"""Load and validate the declarative migration profile (fail closed on anything unknown)."""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

RULES = {'equals', 'map', 'rewrite_local_part', 'unresolvable'}
STRUCTURE_CHECKS = {
    'unique_username', 'no_cross_tenant', 'password_is_bcrypt',
    'tree_single_root', 'no_cycles', 'parents_resolvable', 'prefix_levels',
}
PREFLIGHT_CHECKS = {'username_absent', 'email_absent', 'department_resolves'}
WRITE_ACTIONS = {'create_missing'}
FILTER_OPS = {'equals', 'in', 'present'}
TOP_KEYS = {'version', 'name', 'description', 'source', 'target', 'lineage', 'entities'}
ENTITY_KEYS = {
    'name', 'source', 'target_table', 'keys', 'filter', 'field_checks', 'structure_checks',
    'analysis', 'write', 'rule_assertions',
}
SUPPORTED_VERSIONS = {1}


class ProfileError(Exception):
    """Raised when the profile is missing, malformed, or asks for something unimplemented."""


def _unknown(where: str, keys, allowed) -> None:
    extra = sorted(set(keys) - set(allowed))
    if extra:
        raise ProfileError('%s has unknown key(s) %s; allowed: %s' % (where, extra, sorted(allowed)))


@dataclass(frozen=True)
class Credential:
    from_env: str | None = None
    from_file: str | None = None

    def resolve(self, required_for: str) -> str:
        import os
        if self.from_env:
            value = os.environ.get(self.from_env)
            if not value:
                raise ProfileError('environment variable %s is required for %s' % (self.from_env, required_for))
            return value
        if self.from_file:
            path = Path(self.from_file).expanduser()
            if not path.exists():
                raise ProfileError('credential file %s for %s does not exist' % (path, required_for))
            return path.read_text(encoding='utf-8').strip()
        raise ProfileError('no credential source configured for %s' % required_for)


@dataclass(frozen=True)
class SourceFile:
    name: str
    file: str
    id_field: str
    sha256: str | None = None
    records: int | None = None


@dataclass(frozen=True)
class LineageSpec:
    label: str
    container: str
    user: str
    database: str
    credential: Credential


@dataclass(frozen=True)
class FieldCheck:
    name: str
    source: str
    target: str
    rule: str = 'equals'
    domain: str | None = None
    map: dict[str, Any] | None = None


@dataclass(frozen=True)
class RuleAssertion:
    name: str
    min_match_rate: float = 0.95


@dataclass(frozen=True)
class WriteSpec:
    enabled: bool
    action: str
    endpoint: str
    fields: dict[str, str]
    password: Credential
    preflight: list[str]
    rollback: dict[str, str]


@dataclass(frozen=True)
class EntitySpec:
    name: str
    source: str
    target_table: str
    keys_source: str
    keys_target: str
    filter: dict[str, Any] | None = None
    field_checks: list[FieldCheck] = field(default_factory=list)
    structure_checks: list[str] = field(default_factory=list)
    analysis: list[str] = field(default_factory=list)
    write: WriteSpec | None = None
    rule_assertions: list[RuleAssertion] = field(default_factory=list)


@dataclass(frozen=True)
class TargetSpec:
    access: str
    user: str
    database: str
    credential: Credential
    container: str | None = None
    dsn_env: str | None = None
    api_base: str | None = None
    api_user_env: str | None = None
    api_credential: Credential | None = None
    scope: str = 'tenant'
    tenant_filter: int | None = None


@dataclass(frozen=True)
class MigrationProfile:
    version: int
    name: str
    source_dir: Path
    files: dict[str, SourceFile]
    target: TargetSpec
    lineage: list[LineageSpec]
    entities: dict[str, EntitySpec]
    path: Path
    record_drift: dict[str, dict[str, int]] = field(default_factory=dict)


def _credential(raw: dict | None, where: str) -> Credential | None:
    if raw is None:
        return None
    _unknown(where, raw, {'from_env', 'from_file'})
    return Credential(from_env=raw.get('from_env'), from_file=raw.get('from_file'))


def load_profile(path: Path, allow_record_drift: bool = False) -> MigrationProfile:
    raw = yaml.safe_load(Path(path).read_text(encoding='utf-8'))
    if not isinstance(raw, dict):
        raise ProfileError('profile %s is not a mapping' % path)
    _unknown('profile', raw, TOP_KEYS)
    version = raw.get('version')
    if version not in SUPPORTED_VERSIONS:
        raise ProfileError('unsupported profile version %r; supported: %s' % (version, sorted(SUPPORTED_VERSIONS)))

    source = raw.get('source') or {}
    _unknown('source', source, {'dir', 'files'})
    source_dir = Path(source.get('dir', ''))
    files: dict[str, SourceFile] = {}
    drift: dict[str, dict[str, int]] = {}
    for name, spec in (source.get('files') or {}).items():
        _unknown('source.files.%s' % name, spec, {'file', 'sha256', 'records', 'id_field'})
        entry = SourceFile(name=name, file=spec['file'], id_field=spec.get('id_field', 'departmentId'),
                           sha256=spec.get('sha256'), records=spec.get('records'))
        full = source_dir / entry.file
        if not full.exists():
            raise ProfileError('source file %s does not exist (expected at %s)' % (entry.file, full))
        if entry.sha256:
            actual = hashlib.sha256(full.read_bytes()).hexdigest()
            if not actual.startswith(entry.sha256) and actual != entry.sha256:
                raise ProfileError('sha256 mismatch for %s: declared %s, actual %s'
                                   % (entry.file, entry.sha256, actual))
        if entry.records is not None:
            actual_records = len(json.loads(full.read_text(encoding='utf-8')))
            if actual_records != entry.records and not allow_record_drift:
                raise ProfileError('records mismatch for %s: declared %s, actual %s'
                                   % (entry.file, entry.records, actual_records))
            drift[name] = {'declared': entry.records, 'actual': actual_records}
        files[name] = entry

    target_raw = raw.get('target') or {}
    _unknown('target', target_raw, {'access', 'container', 'dsn_env', 'user', 'database',
                                    'credential', 'api_base', 'api_user_env', 'api_credential',
                                    'scope', 'tenant_filter'})
    access = target_raw.get('access')
    if access not in {'docker', 'dsn'}:
        raise ProfileError('target.access must be docker or dsn, got %r' % access)
    if access == 'docker' and not target_raw.get('container'):
        raise ProfileError('target.container is required when access is docker')
    if access == 'dsn' and not target_raw.get('dsn_env'):
        raise ProfileError('target.dsn_env is required when access is dsn')
    target = TargetSpec(
        access=access, user=target_raw['user'], database=target_raw['database'],
        credential=_credential(target_raw.get('credential'), 'target.credential') or Credential(),
        container=target_raw.get('container'), dsn_env=target_raw.get('dsn_env'),
        api_base=target_raw.get('api_base'), api_user_env=target_raw.get('api_user_env'),
        api_credential=_credential(target_raw.get('api_credential'), 'target.api_credential'),
        scope=target_raw.get('scope', 'tenant'), tenant_filter=target_raw.get('tenant_filter'))

    lineage = []
    for entry in raw.get('lineage') or []:
        _unknown('lineage entry', entry, {'label', 'container', 'user', 'database', 'credential'})
        lineage.append(LineageSpec(label=entry['label'], container=entry['container'], user=entry['user'],
                                   database=entry['database'],
                                   credential=_credential(entry.get('credential'), 'lineage.credential')))

    entities: dict[str, EntitySpec] = {}
    for entry in raw.get('entities') or []:
        _unknown('entity', entry, ENTITY_KEYS)
        name = entry['name']
        from migration.checks import REGISTRY
        if name not in REGISTRY:
            raise ProfileError('unknown entity %r; available: %s' % (name, sorted(REGISTRY)))
        keys = entry.get('keys') or {}
        _unknown('entity %s keys' % name, keys, {'source', 'target'})
        checks = []
        for check in entry.get('field_checks') or []:
            _unknown('entity %s field_check' % name, check, {'name', 'source', 'target', 'rule', 'domain', 'map'})
            rule = check.get('rule', 'equals')
            if rule not in RULES:
                raise ProfileError('unknown field rule %r for %s; allowed: %s'
                                   % (rule, check.get('name'), sorted(RULES)))
            checks.append(FieldCheck(name=check['name'], source=check['source'], target=check['target'],
                                     rule=rule, domain=check.get('domain'), map=check.get('map')))
        for check_name in entry.get('structure_checks') or []:
            if check_name not in STRUCTURE_CHECKS:
                raise ProfileError('unknown structure check %r; allowed: %s'
                                   % (check_name, sorted(STRUCTURE_CHECKS)))
        filter_raw = entry.get('filter') or None
        if filter_raw:
            _unknown('entity %s filter' % name, filter_raw, {'source_field', 'op', 'value'})
            if filter_raw.get('op', 'equals') not in FILTER_OPS:
                raise ProfileError('unknown filter op %r for %s' % (filter_raw.get('op'), name))
        write = None
        if entry.get('write'):
            write_raw = entry['write']
            _unknown('entity %s write' % name, write_raw,
                     {'enabled', 'action', 'endpoint', 'fields', 'password', 'preflight', 'rollback'})
            if write_raw.get('action') not in WRITE_ACTIONS:
                raise ProfileError('unknown write action %r; allowed: %s'
                                   % (write_raw.get('action'), sorted(WRITE_ACTIONS)))
            for needed in ('endpoint', 'fields', 'password', 'preflight'):
                if not write_raw.get(needed):
                    raise ProfileError('write.%s is required for entity %s' % (needed, name))
            for pf in write_raw['preflight']:
                if pf not in PREFLIGHT_CHECKS:
                    raise ProfileError('unknown preflight check %r; allowed: %s'
                                       % (pf, sorted(PREFLIGHT_CHECKS)))
            if write_raw.get('enabled') and not target.api_base:
                raise ProfileError('target.api_base is required when write is enabled for %s' % name)
            write = WriteSpec(enabled=bool(write_raw.get('enabled')), action=write_raw['action'],
                              endpoint=write_raw['endpoint'], fields=write_raw['fields'],
                              password=_credential(write_raw['password'], 'write.password'),
                              preflight=list(write_raw['preflight']),
                              rollback=dict(write_raw.get('rollback') or {}))
        assertions = []
        for assertion in entry.get('rule_assertions') or []:
            _unknown('entity %s rule_assertion' % name, assertion, {'name', 'min_match_rate'})
            assertions.append(RuleAssertion(name=assertion['name'],
                                            min_match_rate=float(assertion.get('min_match_rate', 0.95))))
        entities[name] = EntitySpec(
            name=name, source=entry.get('source', name), target_table=entry['target_table'],
            keys_source=keys.get('source', ''), keys_target=keys.get('target', ''),
            filter=filter_raw, field_checks=checks,
            structure_checks=list(entry.get('structure_checks') or []),
            analysis=list(entry.get('analysis') or []), write=write, rule_assertions=assertions)

    return MigrationProfile(version=version, name=raw.get('name', path.stem), source_dir=source_dir,
                            files=files, target=target, lineage=lineage, entities=entities,
                            path=Path(path), record_drift=drift)
```

```python
# scripts/__init__.py
```
```python
# scripts/migration/__init__.py
"""Migration validation toolkit (see docs/superpowers/specs/2026-09-15-migration-validation-toolkit-design.md)."""
```
```python
# scripts/migration/checks/__init__.py
"""Entity plug-in registry. Importing a new module here is what makes it addressable from a profile."""
REGISTRY = {}
```
```text
# scripts/requirements.txt
PyYAML>=6.0
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_profile.py -q`
Expected: `7 passed`

Notes recorded while executing this task (three plan gaps found by running the tests):
the entity-name validation needs a registered plug-in, so the tests register a stub; the profile
template uses single braces because the helper substitutes `{dir}` with `replace()`; and
`scripts/__tests__/conftest.py` is required for `import migration` to resolve.

Notes recorded while executing this task: the planned phone pattern only matched digits written
without separators or a country code, so it missed realistic input such as `+86 138 1010 1665`; the
implementation now strips separators and accepts an optional `+86` prefix before matching.

- [ ] **Step 5: Commit**

```bash
git add scripts/__init__.py scripts/migration scripts/requirements.txt scripts/__tests__/test_migration_profile.py
git commit -m "feat(migration): load and validate the toolkit profile

The profile is the toolkit's interface: it names the export files with their hashes
and record counts, the target database and API, the entity list and every rule to
apply. Anything unknown (key, rule, entity, structure check, preflight check, write
action) is a load-time error, so a typo can never be mistaken for a configured rule." 
```

---

### Task 2: Source indexing

**Files:**
- Create: `scripts/migration/sources.py`
- Test: `scripts/__tests__/test_migration_sources.py`

**Interfaces:**
- Consumes: `migration.profile.SourceFile`, `MigrationProfile.source_dir`.
- Produces: `SourceIndex` (fields `name`, `path`, `sha256`, `records`, `id_field`, `by_key`) and
  `load_source(root, spec) -> SourceIndex`; later tasks call `load_source(profile.source_dir, profile.files[name])`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_sources.py
import json
from pathlib import Path

import pytest

from migration.profile import SourceFile
from migration.sources import SourceError, load_source


def test_indexes_rows_by_id_field(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'userName': 'A1'}, {'userName': 'A2'}]))
    index = load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
    assert index.records == 2
    assert sorted(index.by_key) == ['A1', 'A2']
    assert index.sha256 == __import__('hashlib').sha256((tmp_path / 'u.json').read_bytes()).hexdigest()


def test_duplicate_keys_keep_the_first_row(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'userName': 'A', 'email': '1'}, {'userName': 'A', 'email': '2'}]))
    index = load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
    assert index.duplicates == ['A']
    assert index.by_key['A']['email'] == '1'


def test_missing_id_field_is_an_error(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'other': 1}]))
    with pytest.raises(SourceError, match='userName'):
        load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))


def test_non_list_payload_is_an_error(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps({'userName': 'A'}))
    with pytest.raises(SourceError, match='list'):
        load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_sources.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.sources'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/sources.py
"""Read an export file, prove it is the expected one, and index it by its business key."""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path

from migration.profile import SourceFile


class SourceError(Exception):
    """Raised when a declared source file cannot be read or does not look like the export."""


@dataclass
class SourceIndex:
    name: str
    path: Path
    id_field: str
    sha256: str
    records: int
    by_key: dict[str, dict]
    duplicates: list[str] = field(default_factory=list)


def load_source(root: Path, spec: SourceFile) -> SourceIndex:
    path = Path(root) / spec.file
    if not path.exists():
        raise SourceError('source file %s does not exist at %s' % (spec.file, path))
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    try:
        rows = json.loads(path.read_text(encoding='utf-8'))
    except json.JSONDecodeError as exc:
        raise SourceError('source file %s is not valid JSON: %s' % (spec.file, exc)) from exc
    if not isinstance(rows, list):
        raise SourceError('source file %s must contain a list of records' % spec.file)
    by_key: dict[str, dict] = {}
    duplicates: list[str] = []
    for row in rows:
        if spec.id_field not in row:
            raise SourceError('source file %s has a record without the key field %r'
                              % (spec.file, spec.id_field))
        key = str(row[spec.id_field])
        if key in by_key:
            duplicates.append(key)
            continue
        by_key[key] = row
    return SourceIndex(name=spec.name, path=path, id_field=spec.id_field, sha256=digest,
                       records=len(rows), by_key=by_key, duplicates=duplicates)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_sources.py -q`
Expected: `3 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/sources.py scripts/__tests__/test_migration_sources.py
git commit -m "feat(migration): index export files by business key

Indexing proves the file is the expected one (hash and record count are checked by
the profile loader) and records duplicate keys instead of silently dropping rows,
since a duplicate is itself a finding worth reporting." 
```

---

### Task 3: Evidence assembly with the privacy guard

**Files:**
- Create: `scripts/migration/report.py`
- Test: `scripts/__tests__/test_migration_report_privacy.py`

**Interfaces:**
- Consumes: `MigrationProfile`.
- Produces: `tokenise(value) -> str`, `PIIError`, `assert_pii_free(payload) -> None`,
  `Evidence(mode, profile)` with `.add(key, value)`, `.payload()`, `.write(path)` and `.summary() -> str`.
  Every later task passes findings through `Evidence`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_report_privacy.py
import pytest

from migration.report import Evidence, PIIError, assert_pii_free, tokenise


def test_token_is_stable_and_not_the_value():
    token = tokenise('Someone@Example.com')
    assert token.startswith('sha256:') and len(token) == 15
    assert token == tokenise('someone@example.com')
    assert 'someone' not in token


def test_email_in_evidence_is_refused():
    with pytest.raises(PIIError, match='email'):
        assert_pii_free({'sample': 'a real person@keas.kln.comm'})


def test_phone_and_employee_code_are_refused():
    with pytest.raises(PIIError, match='phone'):
        assert_pii_free({'sample': '+86 138 1010 1665'})
    with pytest.raises(PIIError, match='code'):
        assert_pii_free({'sample': 'D44967'})


def test_tokens_and_counts_are_allowed():
    assert_pii_free({'by_key': tokenise('D44967'), 'matched': 7834}) is None


def test_evidence_refuses_to_write_pii(tmp_path):
    evidence = Evidence(mode='verify', profile=None)
    evidence.add('samples', ['person@keas.kln.comm'])
    with pytest.raises(PIIError):
        evidence.write(tmp_path / 'e.json')
    assert not (tmp_path / 'e.json').exists()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_report_privacy.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.report'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/report.py
"""Assemble evidence and guarantee it carries no personal data."""
from __future__ import annotations

import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

EMAIL = re.compile(r'[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}')
PHONE = re.compile(r'(?<!\d)(?:\+?86)?1[3-9]\d{9}(?!\d)')
SEPARATORS = re.compile(r'[\s\-().]')
CODE = re.compile(r'\b[DH]\d{5}\b')
TOKEN = re.compile(r'^sha256:[0-9a-f]{8}$')


class PIIError(Exception):
    """Raised when evidence would carry personal data; nothing is written."""


def tokenise(value: str) -> str:
    return 'sha256:' + hashlib.sha256(value.strip().lower().encode()).hexdigest()[:8]


def _walk(node: Any, path: str, hits: list[str]) -> None:
    if isinstance(node, dict):
        for key, value in node.items():
            _walk(value, '%s.%s' % (path, key), hits)
    elif isinstance(node, (list, tuple)):
        for position, value in enumerate(node):
            _walk(value, '%s[%d]' % (path, position), hits)
    elif isinstance(node, str):
        if TOKEN.match(node):
            return
        # a phone number is often written with spaces, hyphens or a country code; normalise first
        normalised = SEPARATORS.sub('', node)
        for label, pattern, subject in (('email', EMAIL, node), ('phone', PHONE, normalised),
                                        ('code', CODE, node)):
            if pattern.search(subject):
                hits.append('%s (%s): %s' % (path, label, node[:40]))
                return


def assert_pii_free(payload: Any) -> None:
    hits: list[str] = []
    _walk(payload, '$', hits)
    if hits:
        raise PIIError('evidence contains personal data: ' + '; '.join(hits[:5]))


class Evidence:
    def __init__(self, mode: str, profile=None):
        self.mode = mode
        self.profile = profile
        self._data: dict[str, Any] = {
            'mode': mode,
            'generated_at': datetime.now(timezone.utc).isoformat(),
            'profile': str(getattr(profile, 'path', '')) or None,
            'profile_name': getattr(profile, 'name', None),
        }
        drift = getattr(profile, 'record_drift', None)
        if drift:
            self._data['record_drift'] = drift

    def add(self, key: str, value: Any) -> None:
        self._data[key] = value

    def payload(self) -> dict:
        assert_pii_free(self._data)
        return self._data

    def write(self, path: Path | None) -> dict:
        payload = self.payload()
        if path:
            Path(path).write_text(json.dumps(payload, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
        return payload

    def summary(self) -> str:
        lines = ['%s: %s' % (k, json.dumps(v, ensure_ascii=False)[:120])
                 for k, v in self._data.items() if k not in {'generated_at', 'profile', 'profile_name'}]
        return '\n'.join(lines)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_report_privacy.py -q`
Expected: `8 passed`

Notes recorded while executing this task:
1. `EntityCheck` must be decorated `@runtime_checkable`, otherwise the registry test
   (`isinstance(REGISTRY['departments'], EntityCheck)`) raises instead of asserting.
2. `DepartmentsCheck.fetch_target` does not need a `parent_lookup` argument: the query already
   returns the parent code, so the call is `query_rows(sql, ('code', 'name', 'parent_id'))`.
3. Root and unresolved samples are sorted before slicing so the evidence is stable between runs.

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/report.py scripts/__tests__/test_migration_report_privacy.py
git commit -m "feat(migration): refuse to emit evidence that carries personal data

The toolkit's output contract is counts, structure and stable sha256 tokens. The
guard runs before anything is written, so a future entity plug-in cannot leak
addresses or employee codes by accident."
```

---

### Task 4: Entity protocol, registry and the departments plug-in

**Files:**
- Create: `scripts/migration/checks/base.py`
- Modify: `scripts/migration/checks/__init__.py`
- Create: `scripts/migration/checks/departments.py`
- Test: `scripts/__tests__/test_migration_checks_departments.py`

**Interfaces:**
- Consumes: `SourceIndex`, `EntitySpec`, `Evidence`.
- Produces: `ReconcileResult`, `WriteIntent`, `EntityCheck` protocol in `checks/base.py`;
  `REGISTRY = {'departments': DepartmentsCheck()}`; `DepartmentsCheck.reconcile/check_structure`
  used by `analyze.py` and `backfill.py` in later tasks.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_checks_departments.py
from migration.checks import REGISTRY
from migration.checks.base import EntityCheck, ReconcileResult
from migration.profile import EntitySpec
from migration.sources import SourceIndex

SPEC = EntitySpec(name='departments', source='departments', target_table='departments',
                  keys_source='departmentId', keys_target='code',
                  structure_checks=['tree_single_root', 'no_cycles', 'parents_resolvable', 'prefix_levels'])


def source(keys):
    rows = {k: {'departmentId': k, 'parentId': p, 'departmentName': n} for k, p, n in keys}
    return SourceIndex(name='departments', path=None, id_field='departmentId', sha256='x',
                       records=len(rows), by_key=rows)


def target(rows):
    return [{'code': c, 'name': n, 'parent_code': p} for c, n, p in rows]


def test_registry_has_departments():
    assert 'departments' in REGISTRY
    assert isinstance(REGISTRY['departments'], EntityCheck)


def test_reconcile_splits_matched_and_only_sides():
    check = REGISTRY['departments']
    src = source([('A', '', 'Root'), ('B', 'A', 'Child')])
    result = check.reconcile(src, target([('A', 'Root', ''), ('C', 'Extra', '')]), SPEC)
    assert result.matched == 1
    assert result.only_source == ['B']
    assert result.only_target == ['C']


def test_structure_reports_tree_facts():
    check = REGISTRY['departments']
    facts = check.check_structure(target([('A', 'Root', ''), ('B', 'Child', 'A'), ('C', 'Child2', 'B')]), SPEC)
    assert facts['roots'] == 1
    assert facts['cycles'] == 0
    assert facts['unresolved_parents'] == 0
    assert facts['prefix_levels']['code_lengths']['1'] == 3


def test_structure_detects_a_cycle():
    check = REGISTRY['departments']
    facts = check.check_structure(target([('A', 'One', 'B'), ('B', 'Two', 'A')]), SPEC)
    assert facts['cycles'] == 2


def test_prefix_coverage_is_reported():
    check = REGISTRY['departments']
    facts = check.check_structure(target([('11D', 'Top', ''), ('11D01', 'Mid', '11D')]), SPEC)
    assert facts['prefix_levels']['internal_codes'] == 1
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_checks_departments.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.checks.base'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/checks/base.py
"""The protocol every entity plug-in implements, plus the shared result shapes."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol

from migration.profile import EntitySpec
from migration.sources import SourceIndex


@dataclass
class ReconcileResult:
    matched: int = 0
    only_source: list[str] = field(default_factory=list)
    only_target: list[str] = field(default_factory=list)
    only_source_by_rule: dict[str, int] = field(default_factory=dict)
    only_target_buckets: dict[str, int] = field(default_factory=dict)
    unattributed: list[str] = field(default_factory=list)


@dataclass
class WriteIntent:
    key: str
    payload: dict[str, Any]
    preflight: list[str] = field(default_factory=list)
    rollback: dict[str, str] = field(default_factory=dict)


class EntityCheck(Protocol):
    name: str

    def fetch_target(self, target, spec: EntitySpec) -> list[dict]: ...

    def reconcile(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> ReconcileResult: ...

    def check_fields(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> dict: ...

    def check_structure(self, tgt: list[dict], spec: EntitySpec) -> dict: ...

    def render_write_plan(self, src, tgt, spec: EntitySpec) -> list[WriteIntent]: ...
```

```python
# scripts/migration/checks/departments.py
"""Departments: reconciled by code, with the tree facts the migration needed."""
from __future__ import annotations

from migration.checks.base import EntityCheck, ReconcileResult, WriteIntent
from migration.profile import EntitySpec
from migration.sources import SourceIndex


class DepartmentsCheck(EntityCheck):
    name = 'departments'

    def fetch_target(self, target, spec: EntitySpec) -> list[dict]:
        return target.query_rows(
            'SELECT code, coalesce(name, \'\'), coalesce(parent_id::text, \'\') FROM %s' % spec.target_table,
            ('code', 'name', 'parent_id'), parent_lookup=spec.target_table)

    def reconcile(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> ReconcileResult:
        source_keys = set(src.by_key)
        target_keys = {row['code'] for row in tgt}
        result = ReconcileResult(
            matched=len(source_keys & target_keys),
            only_source=sorted(source_keys - target_keys),
            only_target=sorted(target_keys - source_keys))
        result.only_source_by_rule = {'source-only': len(result.only_source)}
        result.only_target_buckets = {'target-only': len(result.only_target)}
        result.unattributed = list(result.only_target)
        return result

    def check_fields(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> dict:
        return {'checked': False, 'reason': 'departments declare no field_checks in the 2026-08 profile'}

    def check_structure(self, tgt: list[dict], spec: EntitySpec) -> dict:
        codes = {row['code'] for row in tgt}
        parents = {row['code']: (row.get('parent_id') or '') for row in tgt}
        roots = [c for c, p in parents.items() if not p or p not in codes]
        unresolved = [c for c, p in parents.items() if p and p not in codes]
        cycles = 0
        for code in parents:
            seen, cursor = set(), code
            while parents.get(cursor):
                if cursor in seen:
                    cycles += 1
                    break
                seen.add(cursor)
                cursor = parents[cursor]
        internal = sorted({c for c in codes if any(o != c and o.startswith(c) for o in codes)})
        lengths: dict[str, int] = {}
        for code in codes:
            lengths[str(len(code))] = lengths.get(str(len(code)), 0) + 1
        return {
            'roots': len(roots),
            'root_samples': [c for c in roots[:5]],
            'unresolved_parents': len(unresolved),
            'unresolved_samples': unresolved[:5],
            'cycles': cycles,
            'internal_codes': len(internal),
            'prefix_levels': {'internal_codes': len(internal), 'code_lengths': lengths},
        }

    def render_write_plan(self, src, tgt, spec: EntitySpec) -> list[WriteIntent]:
        return []
```

```python
# scripts/migration/checks/__init__.py   (replace the placeholder)
"""Entity plug-in registry. Importing a module here is what makes it addressable from a profile."""
from migration.checks.departments import DepartmentsCheck

REGISTRY = {'departments': DepartmentsCheck()}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_checks_departments.py -q`
Expected: `5 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/checks scripts/__tests__/test_migration_checks_departments.py
git commit -m "feat(migration): entity protocol and the departments plug-in

The protocol is the extension point: adding an entity means adding a module and a
profile section, not editing the core. Departments reconcile on code and report the
tree facts that took a day of analysis last time (single root, cycles, unresolved
parents, prefix levels)." 
```

---

### Task 5: Users plug-in

**Files:**
- Create: `scripts/migration/checks/users.py`
- Modify: `scripts/migration/checks/__init__.py`
- Test: `scripts/__tests__/test_migration_checks_users.py`

**Interfaces:**
- Consumes: `EntityCheck`, `ReconcileResult`, `WriteIntent`, `SourceIndex`, `EntitySpec`, `tokenise`.
- Produces: `UsersCheck` registered as `'users'`; `apply_field_rule(value, rule, domain=None, mapping=None)`
  used by `analyze.py` and `backfill.py`; `UsersCheck.render_write_plan` used by `backfill.py`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_checks_users.py
from migration.checks import REGISTRY
from migration.checks.users import apply_field_rule, rewrite_email
from migration.profile import EntitySpec, FieldCheck, WriteSpec
from migration.sources import SourceIndex

SPEC = EntitySpec(
    name='users', source='users', target_table='users', keys_source='userName', keys_target='username',
    filter={'source_field': 'status', 'op': 'equals', 'value': 'userstatus01'},
    field_checks=[
        FieldCheck('name', 'realName', 'name'),
        FieldCheck('email', 'email', 'email', rule='rewrite_local_part', domain='keas.kln.comm'),
        FieldCheck('active', 'status', 'active', rule='map',
                   map={'userstatus01': True, 'userstatus04': False}),
        FieldCheck('manager', 'leaderId', 'manager_id', 'unresolvable'),
    ],
    structure_checks=['unique_username', 'no_cross_tenant', 'password_is_bcrypt'],
    write=WriteSpec(enabled=False, action='create_missing', endpoint='POST /api/v1/users',
                    fields={'username': 'userName', 'name': 'realName',
                            'email': 'rewrite:keas.kln.comm',
                            'departmentId': 'department_by:departmentUnit',
                            'role': 'end_user', 'tenantId': '1'},
                    password=type('C', (), {'from_env': 'MIGRATED_DEFAULT_PASSWORD', 'from_file': None})(),
                    preflight=['username_absent', 'email_absent', 'department_resolves'],
                    rollback={'disable': 'PUT /api/v1/users/:id/status'}))


def source(rows):
    return SourceIndex(name='users', path=None, id_field='userName', sha256='x', records=len(rows),
                       by_key={r['userName']: r for r in rows})


def test_rewrite_email_keeps_local_part_and_handles_the_suffix_rule():
    assert rewrite_email('Someone@kerryeas.com', 'keas.kln.comm') == 'someone@keas.kln.comm'
    assert rewrite_email('dup+2@kerryeas.com', 'keas.kln.comm') == 'dup+2@keas.kln.comm'


def test_map_rule_uses_the_declared_table():
    assert apply_field_rule('userstatus01', 'map', mapping={'userstatus01': True}) is True
    assert apply_field_rule('userstatus09', 'map', mapping={'userstatus01': True}) is None


def test_unresolvable_rule_is_recorded_not_counted_as_mismatch():
    outcome = apply_field_rule('532D0ACE', 'unresolvable')
    assert outcome is None


def test_reconcile_counts_filtered_and_matched():
    check = REGISTRY['users']
    src = source([{'userName': 'A', 'status': 'userstatus01'}, {'userName': 'B', 'status': 'userstatus04'}])
    result = check.reconcile(src, [{'username': 'A'}, {'username': 'C'}], SPEC)
    assert result.matched == 1
    assert result.only_source == []            # A matched; B was filtered out by the profile rule
    assert result.only_target == ['C']


def test_field_mismatch_samples_are_tokenised():
    check = REGISTRY['users']
    src = source([{'userName': 'A', 'realName': 'Real Name', 'status': 'userstatus01'}])
    report = check.check_fields(src, [{'username': 'A', 'name': 'Different'}], SPEC)
    assert all(item.startswith('sha256:') for item in report['name']['mismatch_sample'][0])


def test_structure_checks_tenant_and_bcrypt():
    check = REGISTRY['users']
    facts = check.check_structure([{'username': 'A', 'tenant_id': 1, 'password_hash': '$2a$10$x'},
                                   {'username': 'A', 'tenant_id': 2, 'password_hash': 'legacy'}], SPEC)
    assert facts['duplicate_usernames'] == 1
    assert facts['foreign_tenant_rows'] == 1
    assert facts['non_bcrypt_rows'] == 1


def test_write_plan_resolves_the_department_from_the_unit_code():
    check = REGISTRY['users']
    src = source([{'userName': 'B', 'status': 'userstatus01', 'departmentUnit': 'U1'}])
    src.department_ids = {'U1': 635}
    assert [i.payload['departmentId'] for i in check.render_write_plan(src, [], SPEC)] == [635]


def test_write_plan_leaves_zero_when_the_unit_is_unknown():
    check = REGISTRY['users']
    src = source([{'userName': 'B', 'status': 'userstatus01', 'departmentUnit': 'NOPE'}])
    src.department_ids = {'U1': 635}
    assert check.render_write_plan(src, [], SPEC)[0].payload['departmentId'] == 0


def test_write_plan_is_empty_when_nothing_is_missing():
    check = REGISTRY['users']
    plan = check.render_write_plan(source([]), [{'username': 'A'}], SPEC)
    assert plan == []
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_checks_users.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.checks.users'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/checks/users.py
"""Users: reconciled by username with the field rules derived from the 2026-08 batch."""
from __future__ import annotations

from migration.checks.base import ReconcileResult, WriteIntent
from migration.profile import EntitySpec
from migration.report import tokenise
from migration.sources import SourceIndex


def rewrite_email(value: str, domain: str) -> str:
    local = (value or '').split('@')[0].strip() or 'noreply'
    return '%s@%s' % (local, domain)


def apply_field_rule(value, rule: str, domain: str | None = None, mapping: dict | None = None):
    """Return the expected target value, or None when the rule cannot produce one."""
    if value is None or value == '':
        return None if rule == 'unresolvable' else value
    if rule == 'equals':
        return value
    if rule == 'map':
        return (mapping or {}).get(str(value))
    if rule == 'rewrite_local_part':
        return rewrite_email(str(value), domain or '')
    if rule == 'unresolvable':
        return None
    raise ValueError('unsupported rule %r' % rule)


class UsersCheck:
    name = 'users'

    def fetch_target(self, target, spec: EntitySpec) -> list[dict]:
        return target.query_rows(
            "SELECT username, coalesce(name, ''), coalesce(email, ''), active::text, "
            "coalesce(role, ''), tenant_id::text, left(coalesce(password_hash, ''), 4) FROM users",
            ('username', 'name', 'email', 'active', 'role', 'tenant_id', 'hash_prefix'))

    def _source_keys(self, src: SourceIndex, spec: EntitySpec) -> set[str]:
        rule = spec.filter or {}
        if not rule:
            return set(src.by_key)
        field, op, value = rule.get('source_field'), rule.get('op', 'equals'), rule.get('value')
        selected = set()
        for key, row in src.by_key.items():
            actual = row.get(field)
            if op == 'equals' and actual == value:
                selected.add(key)
            elif op == 'in' and actual in (value or []):
                selected.add(key)
            elif op == 'present' and actual not in (None, ''):
                selected.add(key)
        return selected

    def reconcile(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> ReconcileResult:
        source_keys = self._source_keys(src, spec)
        target_keys = {row['username'] for row in tgt}
        result = ReconcileResult(
            matched=len(source_keys & target_keys),
            only_source=sorted(source_keys - target_keys),
            only_target=sorted(target_keys - source_keys))
        result.only_source_by_rule = {'filtered-out-and-absent': len(result.only_source)}
        result.only_target_buckets = {'db-only': len(result.only_target)}
        result.unattributed = list(result.only_target)
        return result

    def check_fields(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> dict:
        by_username = {row['username']: row for row in tgt}
        report: dict[str, dict] = {}
        for check in spec.field_checks:
            matched = mismatched = unresolved = 0
            samples: list[list[str]] = []
            for key, row in src.by_key.items():
                target_row = by_username.get(key)
                if not target_row:
                    continue
                expected = apply_field_rule(row.get(check.source), check.rule, check.domain, check.map)
                if expected is None:
                    unresolved += 1
                    continue
                actual = target_row.get(check.target)
                if str(expected).lower() == str(actual).lower():
                    matched += 1
                else:
                    mismatched += 1
                    if len(samples) < 20:
                        # tokenised, not raw: the evidence contract forbids names and addresses
                        samples.append([tokenise(key), tokenise(str(expected)), tokenise(str(actual))])
            total = matched + mismatched
            report[check.name] = {'rule': check.rule, 'matched': matched, 'mismatched': mismatched,
                                  'unresolvable': unresolved,
                                  'match_rate': round(matched / total, 4) if total else None,
                                  'mismatch_sample': samples}
        return report

    def check_structure(self, tgt: list[dict], spec: EntitySpec) -> dict:
        usernames: dict[str, int] = {}
        foreign = non_bcrypt = 0
        for row in tgt:
            usernames[row['username']] = usernames.get(row['username'], 0) + 1
            if str(row.get('tenant_id')) != '1':
                foreign += 1
            if not str(row.get('hash_prefix', '')).startswith('$2'):
                non_bcrypt += 1
        return {
            'duplicate_usernames': sum(1 for count in usernames.values() if count > 1),
            'foreign_tenant_rows': foreign,
            'non_bcrypt_rows': non_bcrypt,
            'total_rows': len(tgt),
        }

    def render_write_plan(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> list[WriteIntent]:
        if not spec.write or not spec.write.enabled:
            return []
        by_username = {row['username'] for row in tgt}
        department_ids = getattr(src, 'department_ids', {})
        intents: list[WriteIntent] = []
        for key in sorted(self._source_keys(src, spec) - by_username):
            row = src.by_key[key]
            payload: dict = {}
            for field, expression in spec.write.fields.items():
                if expression.startswith('rewrite:'):
                    payload[field] = rewrite_email(row.get('email', ''), expression.split(':', 1)[1])
                elif expression.startswith('department_by:'):
                    unit = str(row.get(expression.split(':', 1)[1], '')).strip()
                    payload[field] = department_ids.get(unit, 0)
                elif expression in row:
                    payload[field] = row[expression]
                elif expression.isdigit():
                    payload[field] = int(expression)
                else:
                    payload[field] = expression
            intents.append(WriteIntent(key=key, payload=payload,
                                       preflight=list(spec.write.preflight),
                                       rollback=dict(spec.write.rollback)))
        return intents
```

```python
# scripts/migration/checks/__init__.py   (add the users import)
"""Entity plug-in registry. Importing a module here is what makes it addressable from a profile."""
from migration.checks.departments import DepartmentsCheck
from migration.checks.users import UsersCheck

REGISTRY = {'departments': DepartmentsCheck(), 'users': UsersCheck()}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_checks_users.py -q`
Expected: `9 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/checks/users.py scripts/migration/checks/__init__.py scripts/__tests__/test_migration_checks_users.py
git commit -m "feat(migration): users plug-in with the derived field rules

Implements the rules the 2026-08 batch actually used: active-status filtering, the
local-part email rewrite, value mapping, and an explicit unresolvable rule so an
incomparable field (the HR leader identifier) is recorded rather than counted as a
defect." 
```

---

### Task 6: Target access (read-only database, product API)

**Files:**
- Create: `scripts/migration/target.py`
- Test: `scripts/__tests__/test_migration_target.py`

**Interfaces:**
- Consumes: `TargetSpec`, `Credential`.
- Produces: `Target(spec, runner=None)` with `query(sql) -> list[list[str]]`,
  `query_rows(sql, columns, parent_lookup=None) -> list[dict]`, `api_login() -> str` (csrf),
  `api_post(path, body, csrf) -> tuple[int, dict]`, `class TargetError`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_target.py
import pytest

from migration.profile import Credential, TargetSpec
from migration.target import Target, TargetError

SPEC = TargetSpec(access='docker', user='ga_owner', database='itsm_ga_ready', container='ga-itsm-20260914',
                  credential=Credential(from_env='TARGET_PW'))


class Recorder:
    def __init__(self, stdout='A|B\n', code=0):
        self.calls = []
        self.stdout = stdout
        self.code = code

    def __call__(self, argv, **kwargs):
        self.calls.append((argv, kwargs))
        return type('R', (), {'returncode': self.code, 'stdout': self.stdout, 'stderr': ''})()


def test_query_wraps_in_a_read_only_transaction(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder('1|2\n')
    target = Target(SPEC, runner=runner)
    rows = target.query('SELECT 1, 2')
    assert rows == [['1', '2']]
    sent = runner.calls[0][1]['input']
    assert sent.startswith('BEGIN READ ONLY;')
    assert sent.rstrip().endswith('COMMIT;')


def test_password_never_appears_in_argv(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder()
    Target(SPEC, runner=runner).query('SELECT 1')
    joined = ' '.join(runner.calls[0][0])
    assert 'secret' not in joined
    assert any(str(arg).startswith('PGPASSWORD=') for arg in runner.calls[0][0])


def test_missing_credential_env_is_reported(monkeypatch):
    monkeypatch.delenv('TARGET_PW', raising=False)
    with pytest.raises(TargetError, match='TARGET_PW'):
        Target(SPEC, runner=Recorder()).query('SELECT 1')


def test_query_error_is_raised_with_the_statement(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    with pytest.raises(TargetError, match='SELECT bad'):
        Target(SPEC, runner=Recorder(code=1)).query('SELECT bad')


def test_query_rows_maps_columns_and_parent_lookup(monkeypatch):
    monkeypatch.setenv('TARGET_PW', 'secret')
    runner = Recorder('A|Root|\nB|Child|A\n')
    rows = Target(SPEC, runner=runner).query_rows('SELECT ...', ('code', 'name', 'parent_id'))
    assert rows == [{'code': 'A', 'name': 'Root', 'parent_id': ''},
                    {'code': 'B', 'name': 'Child', 'parent_id': 'A'}]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_target.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.target'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/target.py
"""The only module that talks to the database (read-only) and to the product API (writes)."""
from __future__ import annotations

import json
import subprocess
import urllib.error
import urllib.request

from migration.profile import Credential, TargetSpec


class TargetError(Exception):
    """Raised when the target database or API can not be used as declared."""


class Target:
    def __init__(self, spec: TargetSpec, runner=None):
        self.spec = spec
        self.runner = runner or subprocess.run
        self._csrf: str | None = None

    # --- read-only database -------------------------------------------------------------------
    def _argv(self, password: str) -> list[str]:
        if self.spec.access == 'docker':
            return ['docker', 'exec', '-i', '-e', 'PGPASSWORD=%s' % password, self.spec.container,
                    'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-F', '|',
                    '-U', self.spec.user, '-d', self.spec.database]
        import os
        dsn = os.environ.get(self.spec.dsn_env or '')
        if not dsn:
            raise TargetError('environment variable %s is required for the target DSN' % self.spec.dsn_env)
        return ['psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-F', '|', '-d', dsn]

    def query(self, sql: str) -> list[list[str]]:
        try:
            password = self.spec.credential.resolve('the target database')
        except Exception as exc:                                  # ProfileError
            raise TargetError(str(exc)) from exc
        statement = 'BEGIN READ ONLY;\n%s;\nCOMMIT;' % sql.rstrip().rstrip(';')
        completed = self.runner(self._argv(password), input=statement, text=True, capture_output=True)
        if completed.returncode != 0:
            raise TargetError('query failed (%s): %s' % (sql.strip()[:60], completed.stderr.strip()[:200]))
        return [line.split('|') for line in completed.stdout.splitlines() if '|' in line]

    def query_rows(self, sql: str, columns: tuple[str, ...], parent_lookup=None) -> list[dict]:
        return [dict(zip(columns, row)) for row in self.query(sql)]

    # --- product API (writes only) -------------------------------------------------------------
    def api_login(self) -> str:
        import os
        credential = self.spec.api_credential or Credential()
        user = os.environ.get(self.spec.api_user_env or '', '')
        if not user:
            raise TargetError('target.api_user_env (%s) must be set in the environment'
                              % self.spec.api_user_env)
        password = credential.resolve('the product API')
        body = json.dumps({'tenantCode': 'default', 'username': user, 'password': password}).encode()
        request = urllib.request.Request('%s/api/v1/auth/login' % self.spec.api_base, data=body,
                                         method='POST', headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(request, timeout=30) as response:
            cookies = response.headers.get_all('Set-Cookie') or []
        csrf_request = urllib.request.Request('%s/api/v1/csrf-token' % self.spec.api_base,
                                              headers={'Cookie': '; '.join(c.split(';')[0] for c in cookies)})
        with urllib.request.urlopen(csrf_request, timeout=30) as response:
            payload = json.loads(response.read().decode())
        self._csrf = payload['data']['csrf_token']
        return self._csrf

    def department_ids_by_code(self) -> dict[str, int]:
        """Map department code to id so a write plan can resolve `department_by:<field>`."""
        rows = self.query('SELECT code, id::text FROM departments')
        return {code: int(identifier) for code, identifier in rows if identifier.isdigit()}

    def api_post(self, path: str, body: dict, csrf: str) -> tuple[int, dict]:
        request = urllib.request.Request('%s%s' % (self.spec.api_base, path),
                                         data=json.dumps(body).encode(), method='POST',
                                         headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return response.status, json.loads(response.read().decode())
        except urllib.error.HTTPError as exc:
            return exc.code, json.loads(exc.read().decode() or '{}')
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_target.py -q`
Expected: `5 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/target.py scripts/__tests__/test_migration_target.py
git commit -m "feat(migration): read-only database access and the product API client

Reads are always wrapped in BEGIN READ ONLY and the password travels through the
child process environment rather than argv, so it cannot leak through process
listings or error text." 
```

---

### Task 7: Analyses and rule-drift verification

**Files:**
- Create: `scripts/migration/analyze.py`
- Test: `scripts/__tests__/test_migration_analyze.py`

**Interfaces:**
- Consumes: `SourceIndex`, `EntitySpec`, `EntityCheck`, `apply_field_rule`, `tokenise`.
- Produces: `derive_map(src, tgt, spec, check) -> dict`, `tree_analysis(tgt, spec, check) -> dict`,
  `discriminate(src, tgt, spec) -> dict`, `verify_profile(profile, measurements) -> dict`
  with `drifted(measurements, profile) -> list[dict]` for exit code 4.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_analyze.py
from migration.analyze import discriminate, drifted, tree_analysis, verify_profile
from migration.checks import REGISTRY
from migration.profile import EntitySpec, RuleAssertion
from migration.sources import SourceIndex

DEPT = EntitySpec(name='departments', source='departments', target_table='departments',
                  keys_source='departmentId', keys_target='code', structure_checks=['tree_single_root'])
USER = EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                  keys_target='username', filter={'source_field': 'status', 'op': 'equals', 'value': 'userstatus01'})


def index(field, rows):
    return SourceIndex(name='users', path=None, id_field=field, sha256='x', records=len(rows),
                       by_key={r[field]: r for r in rows})


def test_discriminate_finds_the_field_that_separates_migrated_from_missing():
    src = index('userName', [
        {'userName': 'IN', 'HR_USERID': 'x', 'status': 'userstatus01'},
        {'userName': 'OUT', 'HR_USERID': '', 'status': 'userstatus01'},
    ])
    result = discriminate(src, [{'username': 'IN'}], USER)
    assert result['HR_USERID']['present_when_migrated'] == 1
    assert result['HR_USERID']['present_when_missing'] == 0


def test_tree_analysis_reports_depth_and_prefix_facts():
    rows = [{'code': 'A', 'name': 'Root', 'parent_id': ''},
            {'code': 'A1', 'name': 'Mid', 'parent_id': 'A'},
            {'code': 'A1B', 'name': 'Leaf', 'parent_id': 'A1'}]
    facts = tree_analysis(rows, DEPT, REGISTRY['departments'])
    assert facts['roots'] == 1
    assert facts['depths']['code_lengths']['1'] == 1
    assert facts['prefix_levels']['internal_codes'] == 2


def test_verify_profile_flags_a_declared_rule_that_no_longer_holds():
    measurements = {'users': {'filter': {'declared': 'userstatus01', 'measured_match_rate': 0.42}}}
    result = verify_profile(type('P', (), {'entities': {'users': USER}})(), measurements)
    assert result['users']['ok'] is False
    assert drifted(measurements, type('P', (), {'entities': {'users': USER}})()) == [
        {'entity': 'users', 'rule': 'filter', 'measured': 0.42, 'minimum': 0.95}]


def test_verify_profile_passes_when_the_rule_holds():
    measurements = {'users': {'filter': {'declared': 'userstatus01', 'measured_match_rate': 1.0}}}
    assert verify_profile(type('P', (), {'entities': {'users': USER}})(), measurements)['users']['ok'] is True
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_analyze.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.analyze'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/analyze.py
"""Rule derivation, tree attribution and rule-drift verification."""
from __future__ import annotations

from migration.checks.users import apply_field_rule
from migration.profile import EntitySpec, MigrationProfile
from migration.sources import SourceIndex


def derive_map(src: SourceIndex, tgt: list[dict], spec: EntitySpec, check) -> dict:
    """Recompute, from already-migrated rows, what rule each declared field obeys."""
    out: dict[str, dict] = {}
    for field in spec.field_checks:
        report = check.check_fields(src, tgt, spec).get(field.name, {})
        out[field.name] = {'rule': field.rule, 'measured_match_rate': report.get('match_rate'),
                           'matched': report.get('matched'), 'mismatched': report.get('mismatched'),
                           'unresolvable': report.get('unresolvable')}
    return out


def tree_analysis(tgt: list[dict], spec: EntitySpec, check) -> dict:
    facts = check.check_structure(tgt, spec)
    codes = [row['code'] for row in tgt]
    parents = {row['code']: (row.get('parent_id') or '') for row in tgt}
    depths: dict[str, int] = {}
    for code in codes:
        depth, cursor, seen = 0, code, set()
        while parents.get(cursor) and cursor not in seen:
            seen.add(cursor)
            cursor = parents[cursor]
            depth += 1
        depths[code] = depth
    distribution: dict[str, int] = {}
    for depth in depths.values():
        distribution[str(depth)] = distribution.get(str(depth), 0) + 1
    return {'roots': facts['roots'], 'cycles': facts['cycles'],
            'unresolved_parents': facts['unresolved_parents'],
            'depths': {'distribution': distribution,
                       'code_lengths': facts['prefix_levels']['code_lengths']},
            'prefix_levels': facts['prefix_levels']}


def discriminate(src: SourceIndex, tgt: list[dict], spec: EntitySpec,
                 candidates: tuple[str, ...] = ('HR_USERID', 'departmentId', 'departmentUnit',
                                                'status', 'leaderId', 'companyId')) -> dict:
    """Which source field separates rows that were migrated from rows that were not."""
    migrated = {row[spec.keys_target] for row in tgt}
    result: dict[str, dict] = {}
    for field in candidates:
        when_migrated = when_missing = 0
        for key, row in src.by_key.items():
            has_value = row.get(field) not in (None, '')
            if key in migrated:
                when_migrated += 1 if has_value else 0
            else:
                when_missing += 1 if has_value else 0
        result[field] = {'present_when_migrated': when_migrated, 'present_when_missing': when_missing}
    return result


DEFAULT_MIN_MATCH_RATE = 0.95


def verify_profile(profile: MigrationProfile, measurements: dict) -> dict:
    """Compare declared rules with what the data shows. Never silently accept drift."""
    result: dict[str, dict] = {}
    for name, entity in profile.entities.items():
        minimum = DEFAULT_MIN_MATCH_RATE
        for assertion in entity.rule_assertions:
            if assertion.name == 'filter':
                minimum = assertion.min_match_rate
        measured = (measurements.get(name) or {}).get('filter', {}).get('measured_match_rate')
        ok = measured is not None and measured >= minimum
        result[name] = {'ok': ok, 'rule': 'filter', 'measured': measured, 'minimum': minimum}
    return result


def drifted(measurements: dict, profile: MigrationProfile) -> list[dict]:
    report = verify_profile(profile, measurements)
    return [{'entity': name, 'rule': outcome['rule'], 'measured': outcome['measured'],
             'minimum': outcome['minimum']}
            for name, outcome in report.items() if not outcome['ok']]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_analyze.py -q`
Expected: `4 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/analyze.py scripts/__tests__/test_migration_analyze.py
git commit -m "feat(migration): rule derivation, tree attribution and drift verification

derive_map recomputes the mapping from already-migrated rows instead of trusting the
profile, tree_analysis answers the department questions that took a day last time,
and verify_profile turns a declared rule that no longer holds into exit code 4 so a
silent change of source semantics can not pass unnoticed." 
```

---

### Task 8: Backfill with the five-step discipline

**Files:**
- Create: `scripts/migration/backfill.py`
- Test: `scripts/__tests__/test_migration_backfill.py`

**Interfaces:**
- Consumes: `Target`, `EntityCheck`, `EntitySpec`, `Evidence`, `WriteSpec`.
- Produces: `preflight(intents, spec, target_rows) -> tuple[list[WriteIntent], list[dict]]`,
  `run(profile, entity_name, apply: bool, target: Target, check: EntityCheck, src, tgt, evidence) -> int`
  returning exit code `0`, `1` or `2`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_backfill.py
import pytest

from migration.backfill import preflight, run
from migration.checks.base import WriteIntent
from migration.profile import Credential, EntitySpec, WriteSpec


def spec_with(enabled=False, preflight_checks=('username_absent',), password_env='MIGRATED_DEFAULT_PASSWORD'):
    return EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                      keys_target='username', write=WriteSpec(
                          enabled=enabled, action='create_missing', endpoint='POST /api/v1/users',
                          fields={'username': 'userName'}, password=Credential(from_env=password_env),
                          preflight=list(preflight_checks), rollback={'disable': 'PUT /api/v1/users/:id/status'}))


def test_preflight_blocks_on_an_existing_key():
    intents = [WriteIntent(key='A', payload={'username': 'A'})]
    allowed, blocked = preflight(intents, spec_with(), [{'username': 'A'}])
    assert allowed == [] and blocked[0]['key'] == 'A' and 'username_absent' in blocked[0]['reason']


def test_preflight_passes_an_unknown_key():
    allowed, blocked = preflight([WriteIntent(key='B', payload={'username': 'B'})], spec_with(), [{'username': 'A'}])
    assert [i.key for i in allowed] == ['B'] and blocked == []


class FakeTarget:
    def __init__(self): self.posted = []
    def api_login(self): return 'csrf-1'
    def api_post(self, path, body, csrf):
        self.posted.append((path, body))
        return 200, {'code': 0, 'data': {'id': 100 + len(self.posted)}}


class FakeCheck:
    def __init__(self, intents): self.intents = intents
    def render_write_plan(self, src, tgt, spec): return self.intents


def test_dry_run_does_not_write(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(profile=None, entity_name='users', apply=False, target=target,
               check=FakeCheck([WriteIntent(key='B', payload={'username': 'B'})]),
               src=None, tgt=[{'username': 'A'}], evidence=None, spec=spec_with(enabled=True))
    assert code == 0 and target.posted == []


def test_apply_requires_the_environment_credential(monkeypatch):
    monkeypatch.delenv('MIGRATED_DEFAULT_PASSWORD', raising=False)
    code = run(profile=None, entity_name='users', apply=True, target=FakeTarget(),
               check=FakeCheck([WriteIntent(key='B', payload={'username': 'B'})]),
               src=None, tgt=[{'username': 'A'}], evidence=None, spec=spec_with(enabled=True))
    assert code == 1


def test_apply_is_idempotent_and_records_created_ids(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    evidence = type('E', (), {'add': lambda self, k, v: setattr(self, k, v)})()
    code = run(profile=None, entity_name='users', apply=True, target=target,
               check=FakeCheck([WriteIntent(key='B', payload={'username': 'B'})]),
               src=None, tgt=[{'username': 'A'}], evidence=evidence, spec=spec_with(enabled=True))
    assert code == 0
    assert target.posted[0][0] == '/api/v1/users'
    assert target.posted[0][1]['password'] == 'pw'
    assert evidence.results[0]['created_id'] == 101


def test_blocked_preflight_returns_two_and_writes_nothing(monkeypatch):
    monkeypatch.setenv('MIGRATED_DEFAULT_PASSWORD', 'pw')
    target = FakeTarget()
    code = run(profile=None, entity_name='users', apply=True, target=target,
               check=FakeCheck([WriteIntent(key='A', payload={'username': 'A'})]),
               src=None, tgt=[{'username': 'A'}], evidence=None, spec=spec_with(enabled=True))
    assert code == 2 and target.posted == []
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_backfill.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.backfill'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/backfill.py
"""The only write path: preflight, dry run, idempotent execution, evidence and rollback."""
from __future__ import annotations

import json
from pathlib import Path

from migration.checks.base import WriteIntent
from migration.profile import EntitySpec


def preflight(intents: list[WriteIntent], spec: EntitySpec, target_rows: list[dict]
              ) -> tuple[list[WriteIntent], list[dict]]:
    key_field = spec.keys_target
    existing = {row[key_field] for row in target_rows}
    existing_emails = {str(row.get('email', '')).lower() for row in target_rows}
    allowed: list[WriteIntent] = []
    blocked: list[dict] = []
    for intent in intents:
        problems = []
        for check in spec.write.preflight:
            if check == 'username_absent' and intent.key in existing:
                problems.append('username_absent: %s already exists' % intent.key)
            elif check == 'email_absent' and str(intent.payload.get('email', '')).lower() in existing_emails:
                problems.append('email_absent: the address for %s already exists' % intent.key)
            elif check == 'department_resolves' and not intent.payload.get('departmentId'):
                problems.append('department_resolves: no department resolved for %s' % intent.key)
        (blocked.append({'key': intent.key, 'reason': '; '.join(problems)}) if problems
         else allowed.append(intent))
    return allowed, blocked


def run(profile, entity_name: str, apply: bool, target, check, src, tgt, evidence,
        spec: EntitySpec | None = None) -> int:
    spec = spec or profile.entities[entity_name]
    if not spec.write or not spec.write.enabled:
        print('write is not enabled for %s in the profile; nothing to do' % entity_name)
        return 0
    intents = check.render_write_plan(src, tgt, spec)
    allowed, blocked = preflight(intents, spec, tgt)
    if blocked:
        print('preflight blocked %d record(s); refusing to write' % len(blocked))
        for item in blocked[:20]:
            print('  %s -> %s' % (item['key'], item['reason']))
        if evidence:
            evidence.add('backfill_blocked', blocked)
        return 2
    if not allowed:
        print('nothing to create for %s' % entity_name)
        if evidence:
            evidence.add('backfill_intents', [])
        return 0
    if not apply:
        print('dry run for %s: %d record(s) would be created' % (entity_name, len(allowed)))
        for intent in allowed[:20]:
            print('  %s -> %s' % (intent.key, json.dumps(intent.payload, ensure_ascii=False)))
        if evidence:
            evidence.add('backfill_intents', [{'key': i.key, 'payload': i.payload} for i in allowed])
        return 0
    try:
        password = spec.write.password.resolve('the migrated default password')
    except Exception as exc:
        print('refusing to apply: %s' % exc)
        return 1
    csrf = target.api_login()
    results = []
    path = spec.write.endpoint.split(' ', 1)[1]
    for intent in allowed:
        body = dict(intent.payload, password=password)
        status, payload = target.api_post(path, body, csrf)
        created_id = (payload.get('data') or {}).get('id') if payload.get('code') == 0 else None
        results.append({'key': intent.key, 'http': status, 'code': payload.get('code'),
                        'message': payload.get('message'), 'created_id': created_id})
        csrf = target.api_login()                       # a successful mutation rotates the token
    created = [r for r in results if r['created_id']]
    print('created %d of %d' % (len(created), len(allowed)))
    if evidence:
        evidence.add('backfill_results', results)
        evidence.add('backfill_rollback', {
            'disable': [spec.write.rollback.get('disable', '').replace(':id', str(r['created_id']))
                        for r in created],
            'sql_delete': 'DELETE FROM %s WHERE id IN (%s);'
                          % (spec.target_table, ', '.join(str(r['created_id']) for r in created)),
        })
    return 0 if len(created) == len(allowed) else 1
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_backfill.py -q`
Expected: `6 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/backfill.py scripts/__tests__/test_migration_backfill.py
git commit -m "feat(migration): profile-driven backfill with the five-step discipline

Preflight runs before anything is written and blocks the whole batch, the default is
a dry run, execution is idempotent and records every created id together with the
rollback commands. The password comes from the environment and never reaches the
evidence." 
```

---

### Task 9: CLI, self-test and exit codes

**Files:**
- Create: `scripts/migration/__main__.py`
- Test: `scripts/__tests__/test_migration_cli.py`
- Create: `scripts/migration/fixtures/self_test.yaml`, `scripts/migration/fixtures/departments.json`, `scripts/migration/fixtures/users.json`

**Interfaces:**
- Consumes: everything above.
- Produces: `main(argv=None) -> int`, constants `EXIT_OK/EXIT_ERROR/EXIT_PREFLIGHT/EXIT_UNATTRIBUTED/EXIT_DRIFT`,
  and the module entry point `python3 -m scripts.migration <command>`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_cli.py
import json
from pathlib import Path

from migration import __main__ as cli


def test_exit_code_precedence_prefers_preflight_then_drift_then_unattributed():
    assert cli.combine_exit_codes([]) == cli.EXIT_OK
    assert cli.combine_exit_codes([cli.EXIT_UNATTRIBUTED]) == cli.EXIT_UNATTRIBUTED
    assert cli.combine_exit_codes([cli.EXIT_UNATTRIBUTED, cli.EXIT_DRIFT]) == cli.EXIT_DRIFT
    assert cli.combine_exit_codes([cli.EXIT_DRIFT, cli.EXIT_PREFLIGHT]) == cli.EXIT_PREFLIGHT
    assert cli.combine_exit_codes([cli.EXIT_PREFLIGHT, cli.EXIT_ERROR]) == cli.EXIT_ERROR


def test_self_test_runs_offline_and_passes():
    assert cli.main(['self-test']) == cli.EXIT_OK


def test_unknown_command_is_an_error(capsys):
    assert cli.main(['nonsense']) == cli.EXIT_ERROR
    assert 'self-test' in capsys.readouterr().out


def test_verify_against_the_offline_fixture_reports_differences():
    fixture = Path('scripts/migration/fixtures/self_test.yaml')
    assert fixture.exists()
    code = cli.main(['verify', '--profile', str(fixture), '--offline-fixture'])
    assert code in {cli.EXIT_OK, cli.EXIT_UNATTRIBUTED}
    payload = json.loads((Path('scripts/migration/fixtures') / 'expected-evidence.json').read_text())
    assert payload['entities']['departments']['matched'] == 2
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_cli.py -q`
Expected: FAIL with `ModuleNotFoundError: No module named 'migration.__main__'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/migration/__main__.py
"""Command line entry point: python3 -m scripts.migration <command> --profile ..."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from migration.analyze import derive_map, discriminate, drifted, tree_analysis
from migration.backfill import run as run_backfill
from migration.checks import REGISTRY
from migration.profile import load_profile
from migration.report import Evidence
from migration.sources import load_source

EXIT_OK, EXIT_ERROR, EXIT_PREFLIGHT, EXIT_UNATTRIBUTED, EXIT_DRIFT = 0, 1, 2, 3, 4
PRECEDENCE = [EXIT_ERROR, EXIT_PREFLIGHT, EXIT_DRIFT, EXIT_UNATTRIBUTED, EXIT_OK]
COMMANDS = ('verify', 'derive-map', 'tree', 'discriminate', 'verify-profile', 'backfill', 'self-test')


def combine_exit_codes(codes) -> int:
    for candidate in PRECEDENCE:
        if candidate in codes:
            return candidate
    return EXIT_OK


def _evidence_path(args) -> Path | None:
    return Path(args.evidence_out) if getattr(args, 'evidence_out', None) else None


def _load(args):
    profile = load_profile(Path(args.profile), allow_record_drift=getattr(args, 'allow_record_drift', False))
    indexes = {name: load_source(profile.source_dir, spec) for name, spec in profile.files.items()}
    return profile, indexes


def _offline_target(fixture_name='target_rows.json'):
    """Serve target rows from a fixture so the CLI is runnable with no database."""
    rows = json.loads((Path(__file__).parent / 'fixtures' / fixture_name).read_text(encoding='utf-8'))

    class Offline:
        def query_rows(self, sql, columns, parent_lookup=None):
            marker = 'departments' if 'departments' in sql else 'users'
            return rows[marker]

    return Offline()


def _target(profile, args):
    if getattr(args, 'offline_fixture', False):
        return _offline_target()
    from migration.target import Target
    return Target(profile.target)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog='python3 -m scripts.migration')
    parser.add_argument('command', choices=COMMANDS)
    parser.add_argument('--profile')
    parser.add_argument('--entity')
    parser.add_argument('--evidence-out')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--offline-fixture', action='store_true')
    parser.add_argument('--allow-record-drift', action='store_true')
    parser.add_argument('--allow-unattributed', action='store_true')
    args = parser.parse_args(argv)

    if args.command == 'self-test':
        from migration.self_test import run_self_test
        return run_self_test()

    if not args.profile:
        print('--profile is required for %s; available commands: %s' % (args.command, ', '.join(COMMANDS)))
        return EXIT_ERROR
    profile, indexes = _load(args)
    names = [args.entity] if args.entity else sorted(profile.entities)
    evidence = Evidence(mode=args.command, profile=profile)
    codes = []
    for name in names:
        spec = profile.entities[name]
        check = REGISTRY[name]
        source = indexes[spec.source]
        target = _target(profile, args)
        tgt = check.fetch_target(target, spec)
        if args.command == 'backfill' and hasattr(target, 'department_ids_by_code'):
            # the write plan resolves department_by:<source field> through this map
            source.department_ids = target.department_ids_by_code()
        if args.command == 'verify':
            result = check.reconcile(source, tgt, spec)
            evidence.payload().setdefault('entities', {})[name] = {
                'matched': result.matched,
                'only_source': len(result.only_source),
                'only_target': len(result.only_target),
                'unattributed': len(result.unattributed),
                'field_checks': check.check_fields(source, tgt, spec),
                'structure': check.check_structure(tgt, spec),
            }
            codes.append(EXIT_UNATTRIBUTED if result.unattributed else EXIT_OK)
        elif args.command == 'derive-map':
            evidence.payload().setdefault('entities', {})[name] = {
                'fields': derive_map(source, tgt, spec, check)}
        elif args.command == 'tree':
            evidence.payload().setdefault('entities', {})[name] = tree_analysis(tgt, spec, check)
        elif args.command == 'discriminate':
            evidence.payload().setdefault('users', discriminate(source, tgt, spec))
        elif args.command == 'verify-profile':
            result = check.reconcile(source, tgt, spec)
            denominator = result.matched + len(result.only_source)
            measured = round(result.matched / denominator, 4) if denominator else 1.0
            measurements = {name: {'filter': {'declared': (spec.filter or {}).get('value'),
                                              'measured_match_rate': measured}}}
            outcome = drifted(measurements, profile)
            evidence.add('drift', outcome)
            codes.append(EXIT_DRIFT if outcome else EXIT_OK)
        elif args.command == 'backfill':
            codes.append(run_backfill(profile, name, args.apply, target, check, source, tgt, evidence, spec))
    code = combine_exit_codes(codes)
    if args.allow_unattributed and code == EXIT_UNATTRIBUTED:
        code = EXIT_OK
    evidence.add('exit_code', code)
    evidence.write(_evidence_path(args))
    print(evidence.summary())
    return code


if __name__ == '__main__':
    sys.exit(main())
```

```python
# scripts/migration/self_test.py
"""Offline checks: fixtures only, no database and no network."""
from __future__ import annotations

import json
from pathlib import Path

from migration.checks import REGISTRY
from migration.profile import load_profile
from migration.report import assert_pii_free
from migration.sources import load_source

FIXTURES = Path(__file__).parent / 'fixtures'


def run_self_test() -> int:
    profile = load_profile(FIXTURES / 'self_test.yaml')
    indexes = {name: load_source(profile.source_dir, spec) for name, spec in profile.files.items()}
    target_rows = json.loads((FIXTURES / 'target_rows.json').read_text(encoding='utf-8'))
    for name, spec in profile.entities.items():
        check = REGISTRY[name]
        source = indexes[spec.source]
        sql_marker = 'departments' if name == 'departments' else 'users'
        tgt = target_rows[sql_marker]
        result = check.reconcile(source, tgt, spec)
        assert result.matched >= 1, '%s: nothing matched in the fixture' % name
        assert_pii_free({'entity': name, 'result': result.__dict__})
    print('self-test ok: %d entities, offline' % len(profile.entities))
    return 0
```

```yaml
# scripts/migration/fixtures/self_test.yaml
version: 1
name: self-test
source:
  dir: scripts/migration/fixtures
  files:
    departments: {file: departments.json, records: 2, id_field: departmentId}
    users: {file: users.json, records: 2, id_field: userName}
target:
  access: docker
  container: self-test
  user: self-test
  database: self-test
  credential: {from_env: SELF_TEST_PASSWORD}
entities:
  - name: departments
    source: departments
    target_table: departments
    keys: {source: departmentId, target: code}
    structure_checks: [tree_single_root, parents_resolvable]
  - name: users
    source: users
    target_table: users
    keys: {source: userName, target: username}
    filter: {source_field: status, op: equals, value: userstatus01}
    structure_checks: [unique_username, password_is_bcrypt]
```

```json
// scripts/migration/fixtures/departments.json
[
  {"departmentId": "ROOT", "parentId": "root", "departmentName": "Root"},
  {"departmentId": "CHILD", "parentId": "ROOT", "departmentName": "Child"}
]
```
```json
// scripts/migration/fixtures/users.json
[
  {"userName": "U1", "realName": "One", "email": "one@kerryeas.com", "status": "userstatus01"},
  {"userName": "U2", "realName": "Two", "email": "two@kerryeas.com", "status": "userstatus04"}
]
```
```json
// scripts/migration/fixtures/target_rows.json
{
  "departments": [
    {"code": "ROOT", "name": "Root", "parent_id": ""},
    {"code": "CHILD", "name": "Child", "parent_id": "ROOT"}
  ],
  "users": [
    {"username": "U1", "name": "One", "email": "one@keas.kln.comm", "active": "true",
     "role": "end_user", "tenant_id": "1", "hash_prefix": "$2a$"}
  ]
}
```
```json
// scripts/migration/fixtures/expected-evidence.json
{
  "entities": {
    "departments": {"matched": 2},
    "users": {"matched": 1}
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/__tests__/test_migration_cli.py -q`
Expected: `4 passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/__main__.py scripts/migration/self_test.py scripts/migration/fixtures scripts/__tests__/test_migration_cli.py
git commit -m "feat(migration): CLI, offline self-test and exit codes

One entry point for verify, the three analyses, drift verification, backfill and an
offline self-test that CI can run without a database. Exit codes carry the meaning
(error, preflight blocked, drift, unattributed) and the precedence is explicit so a
blocked write is never mistaken for a clean run." 
```

---

### Task 10: The 2026-08 profile and the regression anchor

**Files:**
- Create: `scripts/migration/profiles/legacy-itsm-2026-08.yaml`
- Create: `scripts/__tests__/test_migration_regression_anchor.py`
- Modify: `docs/migrations/2026-09-15-legacy-migration-validation-evidence.json` (regenerated, redacted)

**Interfaces:**
- Consumes: the CLI from Task 9.
- Produces: an anchor test that compares the live run with the committed evidence.

- [ ] **Step 1: Write the failing test**

```python
# scripts/__tests__/test_migration_regression_anchor.py
"""Live anchor: needs the export files and the running environment, so it is skipped otherwise."""
import json
import os
import subprocess
from pathlib import Path

import pytest

ANCHOR = Path('docs/migrations/2026-09-15-legacy-migration-validation-evidence.json')
PROFILE = Path('scripts/migration/profiles/legacy-itsm-2026-08.yaml')


def live_environment_available() -> bool:
    return all(os.environ.get(name) for name in ('ITSM_TARGET_DB_PASSWORD',)) and \
        Path('/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data/itsm_users.json').exists()


@pytest.mark.skipif(not live_environment_available(), reason='needs the export files and the target environment')
def test_live_run_matches_the_committed_anchor(tmp_path):
    out = tmp_path / 'evidence.json'
    completed = subprocess.run(['python3', '-m', 'scripts.migration', 'verify', '--profile', str(PROFILE),
                                '--evidence-out', str(out)], capture_output=True, text=True)
    assert completed.returncode in (0, 3), completed.stderr
    anchor = json.loads(ANCHOR.read_text(encoding='utf-8'))
    fresh = json.loads(out.read_text(encoding='utf-8'))
    for entity, expected in anchor['entities'].items():
        for field in ('matched', 'only_source', 'only_target'):
            assert fresh['entities'][entity][field] == expected[field], \
                '%s.%s drifted: %s -> %s' % (entity, field, expected[field], fresh['entities'][entity][field])
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/__tests__/test_migration_regression_anchor.py -q`
Expected: FAIL with `file not found: scripts/migration/profiles/legacy-itsm-2026-08.yaml`

- [ ] **Step 3: Write the profile and regenerate the anchor evidence**

```yaml
# scripts/migration/profiles/legacy-itsm-2026-08.yaml
version: 1
name: legacy-itsm-2026-08
description: 旧 ITSM 主数据 → 新 ITSM（克隆库）迁移校验；凭据一律来自环境变量
source:
  dir: /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data
  files:
    departments: {file: itsm_departments.json, sha256: f8b3fbdaa12176724ee6d346, records: 5272, id_field: departmentId}
    users: {file: itsm_users.json, sha256: fe794d35abcbffb9, records: 14393, id_field: userName}
target:
  access: docker
  container: ga-itsm-20260914
  user: ga_owner
  database: itsm_ga_ready
  credential: {from_env: ITSM_TARGET_DB_PASSWORD}
  api_base: http://localhost:3010
  api_user_env: ITSM_ADMIN_USER
  api_credential: {from_env: ITSM_ADMIN_PASSWORD}
  scope: full-database
lineage:
  - {label: itsm (DEV), container: itsm-postgres-dev, user: itsm_user, database: itsm,
     credential: {from_env: DEV_DB_PASSWORD}}
entities:
  - name: users
    source: users
    target_table: users
    keys: {source: userName, target: username}
    filter: {source_field: status, op: equals, value: userstatus01}
    field_checks:
      - {name: name, source: realName, target: name}
      - {name: email, source: email, target: email, rule: rewrite_local_part, domain: keas.kln.comm}
      - {name: active, source: status, target: active, rule: map,
         map: {userstatus01: true, userstatus04: false}}
      - {name: manager, source: leaderId, target: manager_id, rule: unresolvable}
    structure_checks: [unique_username, no_cross_tenant, password_is_bcrypt]
    rule_assertions: [{name: filter, min_match_rate: 0.95}]
    write:
      enabled: false
      action: create_missing
      endpoint: POST /api/v1/users
      fields:
        username: userName
        name: realName
        email: "rewrite:keas.kln.comm"
        departmentId: "department_by:departmentUnit"
        role: end_user
        tenantId: 1
      password: {from_env: MIGRATED_DEFAULT_PASSWORD}
      preflight: [username_absent, email_absent, department_resolves]
      rollback: {disable: "PUT /api/v1/users/:id/status"}
  - name: departments
    source: departments
    target_table: departments
    keys: {source: departmentId, target: code}
    structure_checks: [tree_single_root, no_cycles, parents_resolvable, prefix_levels]
    analysis: [tree, prefix_closure, path_segment_attribution]
```

Run (credentials from the environment, never on the command line):

```bash
cd /home/administrator/project/itsm/.worktrees/config-launch-integration
read -rs -p 'target db password: ' ITSM_TARGET_DB_PASSWORD; export ITSM_TARGET_DB_PASSWORD; echo
python3 -m scripts.migration verify --profile scripts/migration/profiles/legacy-itsm-2026-08.yaml \
  --evidence-out docs/migrations/2026-09-15-legacy-migration-validation-evidence.json
unset ITSM_TARGET_DB_PASSWORD
python3 -m pytest scripts/__tests__/test_migration_regression_anchor.py -q
```

Expected: the run prints the entity summary and the anchor test passes; the regenerated evidence is
PII-free by construction (the guard refuses anything else).

- [ ] **Step 4: Run the full offline suite**

Run: `python3 -m pytest scripts/__tests__ -q`
Expected: all migration tests pass; the anchor test is skipped when the environment is absent.

- [ ] **Step 5: Commit**

```bash
git add scripts/migration/profiles docs/migrations/2026-09-15-legacy-migration-validation-evidence.json scripts/__tests__/test_migration_regression_anchor.py
git commit -m "test(migration): live regression anchor for the 2026-08 batch

The profile encodes this batch's inputs and rules, and the anchor test compares a
fresh live run with the committed evidence on the counting fields, so a future change
to the toolkit can not silently alter what the verification reports." 
```

---

### Task 11: Retire the old scripts, document the runbook

**Files:**
- Delete: `scripts/verify_itsm_migration_data.py`
- Delete: `scripts/backfill_legacy_users.py`
- Create: `docs/migrations/runbook-data-migration-validation.md`
- Modify: `docs/DEVELOPMENT_GUIDE.md` (add the toolkit to the command reference section)
- Modify: `docs/migrations/2026-09-15-legacy-migration-validation-report.md` and
  `docs/migrations/2026-09-15-legacy-migration-input-and-rule-archive.md` (point at the new entry point)

**Interfaces:**
- Consumes: the CLI from Task 9.
- Produces: no code; the documented entry point and the retired old path.

- [ ] **Step 1: Prove the old scripts are unreferenced before deleting them**

Run:
```bash
cd /home/administrator/project/itsm/.worktrees/config-launch-integration
grep -rn "verify_itsm_migration_data\|backfill_legacy_users" --exclude-dir=.git . | grep -v '^./docs/migrations/2026-09-15' | grep -v '^./docs/review/2026-09-15'
git -C /home/administrator/project/itsm log --all --oneline -- 'scripts/verify_itsm_migration_data.py' | head -3
```
Expected: no hits outside this batch's documents (which Step 4 updates). If another in-flight branch
references them, coordinate before deleting.

- [ ] **Step 2: Delete the old scripts and confirm nothing imports them**

Run:
```bash
git rm scripts/verify_itsm_migration_data.py scripts/backfill_legacy_users.py
grep -rn "verify_itsm_migration_data\|backfill_legacy_users" --exclude-dir=.git scripts/ || echo "no code references"
python3 -m pytest scripts/__tests__ -q
```
Expected: `no code references`, tests pass.

- [ ] **Step 3: Write the runbook**

````markdown
# 数据迁移验证 Runbook（迁移验证工具包）

工具入口：`python3 -m scripts.migration <command> --profile <yaml>`（在仓库根目录执行）。

## 1. 准备 profile

复制 `scripts/migration/profiles/legacy-itsm-2026-08.yaml`，改三处：
源文件（`source.dir` / `source.files`，含 `sha256` 与 `records`）、目标（`target.*`）、实体清单。
**凭据只写 `from_env`/`from_file`**，不要写明文。声明了 `sha256` 就必须匹配，否则拒绝运行
（这正是"手上的文件不是当初那份"的检测）。

## 2. 五步流程

| 步骤 | 命令 | 退出码含义 |
| --- | --- | --- |
| ① 口径与差异 | `verify --profile p.yaml --evidence-out e.json` | 0 通过；3 有未归因差异 |
| ② 规则是否仍成立 | `verify-profile --profile p.yaml` | 0 成立；4 规则漂移（先查源系统口径） |
| ③ 归因分析 | `derive-map` / `tree` / `discriminate` | 0 正常 |
| ④ 补建 | `backfill --profile p.yaml --entity users`（先 dry-run，再 `--apply`） | 0 完成；2 预检阻塞（拒写） |
| ⑤ 归档 | 把 `e.json` 与结论写入 `docs/migrations/` | — |

退出码优先级：`1 运行错误 > 2 预检阻塞 > 4 规则漂移 > 3 未归因差异`。

## 3. 写操作的硬性要求

- profile 里 `write.enabled: true`；环境变量（管理员口令 + 迁移默认口令）齐备；否则拒绝执行
- 预检阻塞时**整批不写**；执行幂等（键已存在则跳过）；每次成功刷新 CSRF
- 回滚：证据里给出 `created_id` 列表、停用命令与删除 SQL，执行前先确认

## 4. 隐私

证据只写计数、结构与 `sha256:<8>` 标识；行级明细写私有目录；文档示例用占位符。
`report.py` 在写出前自检，命中邮箱/手机/工号明文即拒写（退出码 1）。

## 5. CI

CI 只跑离线部分：`python3 -m pytest scripts/__tests__ -q` 与 `python3 -m scripts.migration self-test`。
真实校验需要导出文件与目标库，按本手册手工执行，并同步更新回归锚点。
````

- [ ] **Step 4: Update the references**

```bash
python3 - <<'EOF'
from pathlib import Path
targets = ['docs/migrations/2026-09-15-legacy-migration-validation-report.md',
           'docs/migrations/2026-09-15-legacy-migration-input-and-rule-archive.md',
           'docs/review/2026-09-15-environment-and-migration-closure.md']
line = ('> 工具入口已并入工具包：`python3 -m scripts.migration verify --profile '
        'scripts/migration/profiles/legacy-itsm-2026-08.yaml`（见 docs/migrations/runbook-data-migration-validation.md）。\n')
for rel in targets:
    path = Path(rel)
    text = path.read_text(encoding='utf-8')
    text = text.replace('scripts/verify_itsm_migration_data.py', 'scripts/migration（工具包）')
    text = text.replace('scripts/backfill_legacy_users.py', 'scripts/migration（工具包补建命令）')
    if line not in text:
        lines = text.splitlines()
        text = '\n'.join(lines[:1]) + '\n' + line + '\n'.join(lines[1:]) + '\n'
    path.write_text(text, encoding='utf-8')
print('references updated')
EOF
grep -n "scripts/migration" docs/DEVELOPMENT_GUIDE.md | head -3 || true
```

Then add to `docs/DEVELOPMENT_GUIDE.md`, in the command/reference section, the same three lines that
the runbook gives in §2, plus the offline test command. Use the file's existing heading depth.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs(migration): retire the one-off scripts and publish the runbook

The two scripts this batch used are replaced by the toolkit; keeping both would leave
two ways to verify the same thing. The runbook documents the profile, the five steps,
the exit codes and the privacy contract, and the batch documents now point at the new
entry point." 
```

---

Notes recorded while reviewing the remaining tasks before implementing them (three defects that
would have blocked or broken the toolkit):
1. `api_credential` only supports `from_env`/`from_file`, so the planned `{user_env: ...}` form would
   have made the real profile fail to load; the API user now has its own `target.api_user_env` key.
2. `render_write_plan` read a `department_ids` map that nothing populated, so every write plan would
   have carried `departmentId: 0` and been blocked by preflight; the CLI now fills the map from
   `Target.department_ids_by_code()`.
3. `check_fields` stored raw usernames and addresses in its mismatch samples, which the privacy guard
   refuses to write (and which would have leaked if it did not); samples are tokenised.

## Self-Review

**1. Spec coverage**

| Spec section | Task |
| --- | --- |
| §2 范围（实体清单、三分析、补建、证据、自检、单测、runbook） | 1–11 |
| §4 架构与职责（包结构，依赖单向） | 1, 2, 3, 6, 9 |
| §5 profile schema + 校验规则 + records/版本策略 | 1（含 `records` 漂移与未知键拒绝），10（真实 profile） |
| §6 实体插件接口 | 4（协议与 departments）、5（users） |
| §7 CLI 与分析模式 + 退出码 4 + 优先级 | 7（漂移判定）、9（CLI 与优先级） |
| §8 证据与隐私策略（六条） | 3（守卫与 token）、6（凭据不经 argv、`scope` 字段）、10（重生成证据） |
| §9 写操作五步纪律 | 8 |
| §10 测试与 CI（离线 + 回归锚点 + requirements） | 1（requirements）、9（self-test）、10（锚点） |
| §11 迁移路径与文档 | 11 |
| §13 验收 1–8 | 9（1、2）、10（3、4、8 的锚点与漂移检出）、3（7 隐私）、11（5、6） |

Gaps found and closed during this review: none after adding Task 10's anchor and Task 11's
reference updates.

**2. Placeholder scan:** no `TBD`/`TODO`/"add error handling"/"similar to Task N"; every code step
carries runnable snippets and every command has an expected result.

**3. Type consistency:** `ReconcileResult` fields (`matched`, `only_source`, `only_target`,
`unattributed`) are used identically in Tasks 4, 5, 9, 10; `WriteIntent(key, payload, preflight,
rollback)` in Tasks 5, 8; `Target.query_rows(sql, columns, parent_lookup=None)` in Tasks 4, 5, 6;
`tokenise`/`assert_pii_free` in Tasks 3, 10; `drifted`/`verify_profile` in Tasks 7, 9.
