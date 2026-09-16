"""Load and validate the declarative migration profile (fail closed on anything unknown)."""
from __future__ import annotations

import hashlib
import json
import os
import re
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
        if self.from_env:
            value = os.environ.get(self.from_env)
            if not value:
                raise ProfileError('environment variable %s is required for %s'
                                   % (self.from_env, required_for))
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
    expected_tenant: int | None = None
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
        raise ProfileError('unsupported profile version %r; supported: %s'
                           % (version, sorted(SUPPORTED_VERSIONS)))

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

    if target.scope not in {'tenant', 'full-database'}:
        raise ProfileError('target.scope must be tenant or full-database')
    if target.scope == 'tenant' and (type(target.tenant_filter) is not int or target.tenant_filter <= 0):
        raise ProfileError('tenant scope requires a positive target.tenant_filter')
    if target.scope == 'full-database' and target.tenant_filter is not None:
        raise ProfileError('full-database scope cannot include tenant_filter')

    lineage = []
    for entry in raw.get('lineage') or []:
        _unknown('lineage entry', entry, {'label', 'container', 'user', 'database', 'credential'})
        lineage.append(LineageSpec(label=entry['label'], container=entry['container'], user=entry['user'],
                                   database=entry['database'],
                                   credential=_credential(entry.get('credential'), 'lineage.credential')))

    from .checks import REGISTRY
    entities: dict[str, EntitySpec] = {}
    for entry in raw.get('entities') or []:
        _unknown('entity', entry, ENTITY_KEYS)
        if not re.fullmatch(r'[a-z_][a-z0-9_]*', entry.get('target_table', '')):
            raise ProfileError('target_table must be a plain SQL identifier')
        name = entry['name']
        if name not in REGISTRY:
            raise ProfileError('unknown entity %r; available: %s' % (name, sorted(REGISTRY)))
        keys = entry.get('keys') or {}
        _unknown('entity %s keys' % name, keys, {'source', 'target'})
        checks = []
        for check in entry.get('field_checks') or []:
            _unknown('entity %s field_check' % name, check,
                     {'name', 'source', 'target', 'rule', 'domain', 'map'})
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
            for preflight in write_raw['preflight']:
                if preflight not in PREFLIGHT_CHECKS:
                    raise ProfileError('unknown preflight check %r; allowed: %s'
                                       % (preflight, sorted(PREFLIGHT_CHECKS)))
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
            expected_tenant=target.tenant_filter, filter=filter_raw, field_checks=checks,
            structure_checks=list(entry.get('structure_checks') or []),
            analysis=list(entry.get('analysis') or []), write=write, rule_assertions=assertions)

    return MigrationProfile(version=version, name=raw.get('name', path.stem), source_dir=source_dir,
                            files=files, target=target, lineage=lineage, entities=entities,
                            path=Path(path), record_drift=drift)
