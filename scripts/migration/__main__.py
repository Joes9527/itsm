"""Command line entry point: python3 -m scripts.migration <command> --profile ..."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from .analyze import derive_map, discriminate, drifted, tree_analysis
from .backfill import run as run_backfill
from .checks import REGISTRY
from .profile import load_profile
from .report import Evidence
from .sources import load_source

EXIT_OK, EXIT_ERROR, EXIT_PREFLIGHT, EXIT_UNATTRIBUTED, EXIT_DRIFT = 0, 1, 2, 3, 4
PRECEDENCE = [EXIT_ERROR, EXIT_PREFLIGHT, EXIT_DRIFT, EXIT_UNATTRIBUTED, EXIT_OK]
COMMANDS = ('verify', 'derive-map', 'tree', 'discriminate', 'verify-profile', 'backfill', 'self-test')
FIXTURES = Path(__file__).parent / 'fixtures'


def combine_exit_codes(codes) -> int:
    for candidate in PRECEDENCE:
        if candidate in codes:
            return candidate
    return EXIT_OK


def _evidence_path(args) -> Path | None:
    return Path(args.evidence_out) if getattr(args, 'evidence_out', None) else None


def _load(args):
    profile = load_profile(Path(args.profile),
                           allow_record_drift=getattr(args, 'allow_record_drift', False))
    indexes = {name: load_source(profile.source_dir, spec) for name, spec in profile.files.items()}
    return profile, indexes


def _offline_target(fixture_name='target_rows.json'):
    """Serve target rows from a fixture so the CLI is runnable with no database."""
    rows = json.loads((FIXTURES / fixture_name).read_text(encoding='utf-8'))

    class Offline:
        def scope_predicate(self, alias):
            return "true"

        def query_rows(self, sql, columns, parent_lookup=None):
            marker = 'departments' if 'departments' in sql else 'users'
            return rows[marker]

    return Offline()


def _target(profile, args):
    if getattr(args, 'offline_fixture', False):
        return _offline_target()
    from .target import Target
    return Target(profile.target)


def _lineage_targets(profile):
    """Build a read-only target for each declared lineage database (spec section 5)."""
    from .profile import TargetSpec
    from .target import Target
    targets = []
    for entry in profile.lineage:
        spec = TargetSpec(access=profile.target.access, user=entry.user, database=entry.database,
                          credential=entry.credential, container=entry.container,
                          scope='full-database')
        targets.append((entry.label, Target(spec)))
    return targets


def _lineage_comparison(profile, check, source, spec, names) -> dict:
    """Count how the same entity reconciles in every lineage database; never fail the run."""
    comparison: dict = {}
    for label, target in _lineage_targets(profile):
        try:
            rows = check.fetch_target(target, spec)
            result = check.reconcile(source, rows, spec)
            comparison[label] = {'matched': result.matched,
                                 'only_source': len(result.only_source),
                                 'only_target': len(result.only_target)}
        except Exception as exc:                       # unavailable database must not hide the result
            comparison[label] = {'unavailable': str(exc)[:120]}
    return comparison


def _entities(evidence) -> dict:
    return evidence.payload().setdefault('entities', {})


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog='python3 -m scripts.migration',
                                     description='migration validation toolkit')
    parser.add_argument('command')
    parser.add_argument('--profile')
    parser.add_argument('--entity')
    parser.add_argument('--evidence-out')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--offline-fixture', action='store_true')
    parser.add_argument('--allow-record-drift', action='store_true')
    parser.add_argument('--allow-unattributed', action='store_true')
    parser.add_argument('--no-lineage', action='store_true')
    args = parser.parse_args(argv)

    if args.command not in COMMANDS:
        print('unknown command %r; available commands: %s' % (args.command, ', '.join(COMMANDS)))
        return EXIT_ERROR
    if args.command == 'self-test':
        from .self_test import run_self_test
        return run_self_test()
    if not args.profile:
        print('--profile is required for %s; available commands: %s'
              % (args.command, ', '.join(COMMANDS)))
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
            _entities(evidence)[name] = {
                'matched': result.matched,
                'only_source': len(result.only_source),
                'only_target': len(result.only_target),
                'unattributed': len(result.unattributed),
                'field_checks': check.check_fields(source, tgt, spec),
                'structure': check.check_structure(tgt, spec),
            }
            if profile.lineage and not args.no_lineage and not getattr(args, 'offline_fixture', False):
                evidence.payload().setdefault('lineage', {})[name] = _lineage_comparison(
                    profile, check, source, spec, names)
            codes.append(EXIT_UNATTRIBUTED if result.unattributed else EXIT_OK)
        elif args.command == 'derive-map':
            _entities(evidence)[name] = {'fields': derive_map(source, tgt, spec, check)}
        elif args.command == 'tree':
            _entities(evidence)[name] = tree_analysis(tgt, spec, check)
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
            codes.append(run_backfill(profile, name, args.apply, target, check, source, tgt,
                                      evidence, spec))

    code = combine_exit_codes(codes)
    if args.allow_unattributed and code == EXIT_UNATTRIBUTED:
        code = EXIT_OK
    evidence.add('exit_code', code)
    evidence.write(_evidence_path(args))
    print(evidence.summary())
    return code


if __name__ == '__main__':
    sys.exit(main())
