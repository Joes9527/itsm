"""Rule derivation, tree attribution and rule-drift verification."""
from __future__ import annotations

from migration.profile import EntitySpec, MigrationProfile
from migration.sources import SourceIndex

DEFAULT_MIN_MATCH_RATE = 0.95
DISCRIMINATE_CANDIDATES = ('HR_USERID', 'departmentId', 'departmentUnit', 'status',
                           'leaderId', 'companyId')


def derive_map(src: SourceIndex, tgt: list[dict], spec: EntitySpec, check) -> dict:
    """Recompute, from already-migrated rows, what rule each declared field obeys."""
    fields = check.check_fields(src, tgt, spec)
    out: dict[str, dict] = {}
    for field in spec.field_checks:
        report = fields.get(field.name, {})
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
    return {'roots': facts['roots'],
            'cycles': facts['cycles'],
            'unresolved_parents': facts['unresolved_parents'],
            'depths': {'distribution': distribution,
                       'code_lengths': facts['prefix_levels']['code_lengths']},
            'prefix_levels': facts['prefix_levels']}


def discriminate(src: SourceIndex, tgt: list[dict], spec: EntitySpec,
                 candidates: tuple[str, ...] = DISCRIMINATE_CANDIDATES) -> dict:
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


def verify_profile(profile: MigrationProfile, measurements: dict) -> dict:
    """Compare declared rules with what the data shows. Never silently accept drift."""
    result: dict[str, dict] = {}
    for name, entity in profile.entities.items():
        minimum = DEFAULT_MIN_MATCH_RATE
        for assertion in entity.rule_assertions:
            if assertion.name == 'filter':
                minimum = assertion.min_match_rate
        measured = (measurements.get(name) or {}).get('filter', {}).get('measured_match_rate')
        result[name] = {'ok': measured is not None and measured >= minimum,
                        'rule': 'filter', 'measured': measured, 'minimum': minimum}
    return result


def drifted(measurements: dict, profile: MigrationProfile) -> list[dict]:
    report = verify_profile(profile, measurements)
    return [{'entity': name, 'rule': outcome['rule'], 'measured': outcome['measured'],
             'minimum': outcome['minimum']}
            for name, outcome in report.items() if not outcome['ok']]
