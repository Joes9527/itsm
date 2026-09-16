"""Offline checks: fixtures only, no database and no network. This is what CI runs."""
from __future__ import annotations

import json
from pathlib import Path

from .analyze import derive_map, discriminate, tree_analysis
from .checks import REGISTRY
from .profile import load_profile
from .report import assert_pii_free
from .sources import load_source

FIXTURES = Path(__file__).parent / 'fixtures'


def _rows_for(name: str, rows: dict) -> list[dict]:
    return rows['departments' if name == 'departments' else 'users']


def run_self_test() -> int:
    """Run every read-only path against the fixtures and assert the invariants hold."""
    profile = load_profile(FIXTURES / 'self_test.yaml')
    indexes = {name: load_source(profile.source_dir, spec) for name, spec in profile.files.items()}
    target_rows = json.loads((FIXTURES / 'target_rows.json').read_text(encoding='utf-8'))
    checked = 0
    for name, spec in profile.entities.items():
        check = REGISTRY[name]
        source = indexes[spec.source]
        tgt = _rows_for(name, target_rows)
        result = check.reconcile(source, tgt, spec)
        assert result.matched >= 1, '%s: nothing matched in the fixture' % name
        structure = check.check_structure(tgt, spec)
        assert 'roots' in structure or 'total_rows' in structure, '%s: no structure facts' % name
        payload = {'entity': name, 'reconcile': result.__dict__, 'structure': structure,
                   'fields': check.check_fields(source, tgt, spec),
                   'derived': derive_map(source, tgt, spec, check)}
        if name == 'departments':
            payload['tree'] = tree_analysis(tgt, spec, check)
        else:
            payload['discriminate'] = discriminate(source, tgt, spec)
        assert_pii_free(payload)          # the evidence contract must hold for every entity
        checked += 1
    print('self-test ok: %d entities, offline' % checked)
    return 0
