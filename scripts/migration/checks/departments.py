"""Departments: reconciled by code, with the tree facts the migration needed."""
from __future__ import annotations

from .base import ReconcileResult, WriteIntent
from ..profile import EntitySpec
from ..sources import SourceIndex


class DepartmentsCheck:
    name = 'departments'

    def fetch_target(self, target, spec: EntitySpec) -> list[dict]:
        return target.query_rows(
            "SELECT code, coalesce(name, ''), coalesce(parent_id::text, '') FROM %s"
            % spec.target_table,
            ('code', 'name', 'parent_id'))

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
        return {'checked': False,
                'reason': 'departments declare no field_checks in the 2026-08 profile'}

    def check_structure(self, tgt: list[dict], spec: EntitySpec) -> dict:
        codes = {row['code'] for row in tgt}
        parents = {row['code']: (row.get('parent_id') or '') for row in tgt}
        roots = [code for code, parent in parents.items() if not parent or parent not in codes]
        unresolved = [code for code, parent in parents.items() if parent and parent not in codes]
        cycles = 0
        for code in parents:
            seen, cursor = set(), code
            while parents.get(cursor):
                if cursor in seen:
                    cycles += 1
                    break
                seen.add(cursor)
                cursor = parents[cursor]
        internal = sorted({code for code in codes
                           if any(other != code and other.startswith(code) for other in codes)})
        lengths: dict[str, int] = {}
        for code in codes:
            lengths[str(len(code))] = lengths.get(str(len(code)), 0) + 1
        return {
            'roots': len(roots),
            'root_samples': sorted(roots)[:5],
            'unresolved_parents': len(unresolved),
            'unresolved_samples': sorted(unresolved)[:5],
            'cycles': cycles,
            'internal_codes': len(internal),
            'prefix_levels': {'internal_codes': len(internal), 'code_lengths': lengths},
        }

    def render_write_plan(self, src, tgt, spec: EntitySpec) -> list[WriteIntent]:
        return []
