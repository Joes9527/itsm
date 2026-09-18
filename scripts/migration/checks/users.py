"""Users: reconciled by username with the field rules derived from the 2026-08 batch."""
from __future__ import annotations

from .base import ReconcileResult, WriteIntent
from ..profile import EntitySpec
from ..report import tokenise
from ..sources import SourceIndex


def rewrite_email(value: str, domain: str) -> str:
    """Keep the local part verbatim (the batch preserved case) and replace the domain."""
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
            "SELECT u.username, coalesce(u.name, ''), coalesce(u.email, ''), u.active::text, "
            "coalesce(u.role, ''), u.tenant_id::text, left(coalesce(u.password_hash, ''), 4) FROM %s u WHERE %s"
            % (spec.target_table, target.scope_predicate('u')),
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
        result.only_source_by_rule = {'filtered-in-and-absent': len(result.only_source)}
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
            username = row['username']
            usernames[username] = usernames.get(username, 0) + 1
            if spec.expected_tenant is not None and str(row.get('tenant_id')) != str(spec.expected_tenant):
                foreign += 1
            stored = str(row.get('password_hash') or row.get('hash_prefix') or '')
            if not stored.startswith('$2'):
                non_bcrypt += 1
        return {
            'duplicate_usernames': sum(1 for count in usernames.values() if count > 1),
            'foreign_tenant_rows': foreign if spec.expected_tenant is not None else None,
            'non_bcrypt_rows': non_bcrypt,
            'total_rows': len(tgt),
        }

    def render_write_plan(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> list[WriteIntent]:
        if not spec.write or not spec.write.enabled:
            return []
        present = {row['username'] for row in tgt}
        department_ids = getattr(src, 'department_ids', {})
        intents: list[WriteIntent] = []
        for key in sorted(self._source_keys(src, spec) - present):
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
