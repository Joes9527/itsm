from migration.checks import REGISTRY
from migration.checks.base import EntityCheck
from migration.profile import EntitySpec
from migration.sources import SourceIndex

SPEC = EntitySpec(name='departments', source='departments', target_table='departments',
                  keys_source='departmentId', keys_target='code',
                  structure_checks=['tree_single_root', 'no_cycles', 'parents_resolvable',
                                    'prefix_levels'])


def source(keys):
    rows = {k: {'departmentId': k, 'parentId': p, 'departmentName': n} for k, p, n in keys}
    return SourceIndex(name='departments', path=None, id_field='departmentId', sha256='x',
                       records=len(rows), by_key=rows)


def target(rows):
    return [{'code': c, 'name': n, 'parent_id': p} for c, n, p in rows]


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


def test_fields_are_reported_as_unchecked_for_departments():
    check = REGISTRY['departments']
    report = check.check_fields(source([('A', '', 'Root')]), target([('A', 'Root', '')]), SPEC)
    assert report['checked'] is False


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


def test_unresolved_parent_is_reported():
    check = REGISTRY['departments']
    facts = check.check_structure(target([('A', 'Orphan', 'GONE')]), SPEC)
    assert facts['unresolved_parents'] == 1
    assert facts['unresolved_samples'] == ['A']


def test_write_plan_is_empty_for_departments():
    check = REGISTRY['departments']
    assert check.render_write_plan(source([]), [], SPEC) == []
