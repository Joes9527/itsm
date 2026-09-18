from migration.checks import REGISTRY
from migration.checks.users import apply_field_rule, rewrite_email
from migration.profile import EntitySpec, FieldCheck, WriteSpec
from migration.sources import SourceIndex

SPEC = EntitySpec(
    name='users', source='users', target_table='users', keys_source='userName', keys_target='username', expected_tenant=1,
    filter={'source_field': 'status', 'op': 'equals', 'value': 'userstatus01'},
    field_checks=[
        FieldCheck('name', 'realName', 'name'),
        FieldCheck('email', 'email', 'email', rule='rewrite_local_part', domain='keas.kln.comm'),
        FieldCheck('active', 'status', 'active', rule='map',
                   map={'userstatus01': True, 'userstatus04': False}),
        FieldCheck('manager', 'leaderId', 'manager_id', 'unresolvable'),
    ],
    structure_checks=['unique_username', 'no_cross_tenant', 'password_is_bcrypt'],
    write=WriteSpec(enabled=True, action='create_missing', endpoint='POST /api/v1/users',
                    fields={'username': 'userName', 'name': 'realName',
                            'email': 'rewrite:keas.kln.comm',
                            'departmentId': 'department_by:departmentUnit',
                            'role': 'end_user', 'tenantId': '1'},
                    password=type('C', (), {'from_env': 'MIGRATED_DEFAULT_PASSWORD',
                                            'from_file': None})(),
                    preflight=['username_absent', 'email_absent', 'department_resolves'],
                    rollback={'disable': 'PUT /api/v1/users/:id/status'}))


def source(rows):
    return SourceIndex(name='users', path=None, id_field='userName', sha256='x', records=len(rows),
                       by_key={row['userName']: row for row in rows})


def test_rewrite_email_keeps_the_local_part_verbatim():
    # the 2026-08 batch preserved case (a migrated account reads WangQing@keas.kln.comm)
    assert rewrite_email('Someone@kerryeas.com', 'keas.kln.comm') == 'Someone@keas.kln.comm'
    assert rewrite_email('dup+2@kerryeas.com', 'keas.kln.comm') == 'dup+2@kerryeas.com.keas.kln.comm'.replace(
        'kerryeas.com.', '')


def test_map_rule_uses_the_declared_table():
    assert apply_field_rule('userstatus01', 'map', mapping={'userstatus01': True}) is True
    assert apply_field_rule('userstatus09', 'map', mapping={'userstatus01': True}) is None


def test_unresolvable_rule_is_recorded_not_counted_as_mismatch():
    assert apply_field_rule('532D0ACE', 'unresolvable') is None


def test_reconcile_counts_filtered_and_matched():
    check = REGISTRY['users']
    src = source([{'userName': 'A', 'status': 'userstatus01'},
                  {'userName': 'B', 'status': 'userstatus04'}])
    result = check.reconcile(src, [{'username': 'A'}, {'username': 'C'}], SPEC)
    assert result.matched == 1
    assert result.only_source == []            # A matched; B was filtered out by the profile rule
    assert result.only_target == ['C']


def test_filter_present_op_selects_rows_with_a_value():
    check = REGISTRY['users']
    spec = EntitySpec(name='users', source='users', target_table='users', keys_source='userName',
                      keys_target='username', filter={'source_field': 'HR_USERID', 'op': 'present'})
    src = source([{'userName': 'A', 'HR_USERID': 'x'}, {'userName': 'B', 'HR_USERID': ''}])
    assert check.reconcile(src, [], spec).only_source == ['A']


def test_field_checks_report_match_rate_and_unresolvable_counts():
    check = REGISTRY['users']
    src = source([{'userName': 'A', 'status': 'userstatus01', 'realName': 'Same',
                   'email': 'same@kerryeas.com', 'leaderId': 'HR1'}])
    tgt = [{'username': 'A', 'name': 'Same', 'email': 'same@keas.kln.comm', 'active': 'true',
            'tenant_id': '1', 'hash_prefix': '$2a$', 'manager_id': ''}]
    report = check.check_fields(src, tgt, SPEC)
    assert report['name']['match_rate'] == 1.0
    assert report['email']['matched'] == 1
    assert report['active']['matched'] == 1
    assert report['manager']['unresolvable'] == 1


def test_field_mismatch_samples_are_tokenised():
    check = REGISTRY['users']
    src = source([{'userName': 'A', 'realName': 'Real Name', 'status': 'userstatus01'}])
    report = check.check_fields(src, [{'username': 'A', 'name': 'Different'}], SPEC)
    assert all(item.startswith('sha256:') for item in report['name']['mismatch_sample'][0])


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
    assert check.render_write_plan(source([]), [{'username': 'A'}], SPEC) == []


def test_write_plan_carries_no_password():
    check = REGISTRY['users']
    src = source([{'userName': 'B', 'status': 'userstatus01', 'realName': 'Bee',
                   'departmentUnit': 'U1'}])
    src.department_ids = {'U1': 635}
    intent = check.render_write_plan(src, [], SPEC)[0]
    assert 'password' not in intent.payload
    assert intent.payload['email'].endswith('@keas.kln.comm')
    assert intent.preflight == ['username_absent', 'email_absent', 'department_resolves']


def test_structure_checks_tenant_and_bcrypt():
    check = REGISTRY['users']
    facts = check.check_structure([{'username': 'A', 'tenant_id': 1, 'password_hash': '$2a$10$x'},
                                   {'username': 'A', 'tenant_id': 2, 'password_hash': 'legacy'}], SPEC)
    assert facts['duplicate_usernames'] == 1
    assert facts['foreign_tenant_rows'] == 1
    assert facts['non_bcrypt_rows'] == 1
