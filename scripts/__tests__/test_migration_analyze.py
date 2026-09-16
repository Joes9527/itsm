from migration.analyze import discriminate, drifted, tree_analysis, verify_profile
from migration.checks import REGISTRY
from migration.profile import EntitySpec
from migration.sources import SourceIndex

DEPT = EntitySpec(name='departments', source='departments', target_table='departments',
                  keys_source='departmentId', keys_target='code',
                  structure_checks=['tree_single_root'])
USER = EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                  keys_target='username',
                  filter={'source_field': 'status', 'op': 'equals', 'value': 'userstatus01'})


def index(field, rows):
    return SourceIndex(name='users', path=None, id_field=field, sha256='x', records=len(rows),
                       by_key={row[field]: row for row in rows})


def profile_of(entities):
    return type('P', (), {'entities': entities})()


def test_discriminate_finds_the_field_that_separates_migrated_from_missing():
    src = index('userName', [
        {'userName': 'IN', 'HR_USERID': 'x', 'status': 'userstatus01'},
        {'userName': 'OUT', 'HR_USERID': '', 'status': 'userstatus01'},
    ])
    result = discriminate(src, [{'username': 'IN'}], USER)
    assert result['HR_USERID']['present_when_migrated'] == 1
    assert result['HR_USERID']['present_when_missing'] == 0


def test_discriminate_counts_both_directions_for_a_weak_field():
    src = index('userName', [
        {'userName': 'IN', 'status': 'userstatus01'},
        {'userName': 'OUT', 'status': 'userstatus01'},
    ])
    result = discriminate(src, [{'username': 'IN'}], USER)
    assert result['status']['present_when_migrated'] == 1
    assert result['status']['present_when_missing'] == 1


def test_tree_analysis_reports_depth_and_prefix_facts():
    rows = [{'code': 'A', 'name': 'Root', 'parent_id': ''},
            {'code': 'A1', 'name': 'Mid', 'parent_id': 'A'},
            {'code': 'A1B', 'name': 'Leaf', 'parent_id': 'A1'}]
    facts = tree_analysis(rows, DEPT, REGISTRY['departments'])
    assert facts['roots'] == 1
    assert facts['cycles'] == 0
    assert facts['depths']['code_lengths']['1'] == 1
    assert facts['prefix_levels']['internal_codes'] == 2
    assert facts['depths']['distribution'] == {'0': 1, '1': 1, '2': 1}


def test_derive_map_reports_the_measured_rate_per_field():
    from migration.analyze import derive_map
    from migration.profile import FieldCheck

    spec = EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                      keys_target='username',
                      field_checks=[FieldCheck('name', 'realName', 'name')])
    src = index('userName', [{'userName': 'A', 'realName': 'Same'}])
    tgt = [{'username': 'A', 'name': 'Same'}]
    result = derive_map(src, tgt, spec, REGISTRY['users'])
    assert result['name']['measured_match_rate'] == 1.0


def test_verify_profile_flags_a_declared_rule_that_no_longer_holds():
    measurements = {'users': {'filter': {'declared': 'userstatus01', 'measured_match_rate': 0.42}}}
    assert verify_profile(profile_of({'users': USER}), measurements)['users']['ok'] is False
    assert drifted(measurements, profile_of({'users': USER})) == [
        {'entity': 'users', 'rule': 'filter', 'measured': 0.42, 'minimum': 0.95}]


def test_verify_profile_passes_when_the_rule_holds():
    measurements = {'users': {'filter': {'declared': 'userstatus01', 'measured_match_rate': 1.0}}}
    assert verify_profile(profile_of({'users': USER}), measurements)['users']['ok'] is True
    assert drifted(measurements, profile_of({'users': USER})) == []


def test_verify_profile_treats_a_missing_measurement_as_drift():
    assert drifted({'users': {}}, profile_of({'users': USER}))[0]['measured'] is None


def test_drift_threshold_can_be_raised_by_the_profile():
    from migration.profile import RuleAssertion
    strict = EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                        keys_target='username', rule_assertions=[RuleAssertion('filter', 0.99)])
    measurements = {'users': {'filter': {'measured_match_rate': 0.98}}}
    assert drifted(measurements, profile_of({'users': strict}))[0]['minimum'] == 0.99
