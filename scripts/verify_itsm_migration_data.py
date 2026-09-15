#!/usr/bin/env python3
"""Validate the legacy master-data migration that the running ITSM environment serves.

Source of truth: the legacy export files named by the migration SOP
(`itsm_departments.json`, `itsm_users.json`). Target: the database behind the running environment
(`itsm_ga_ready`), with optional read-only lineage comparison against the other clones and the DEV
database, because every one of them carries the same 2026-08-19 migration batch.

Acceptance stance: item-by-item difference attribution. Every difference bucket is either given a
credible, evidenced explanation or listed as an unexplained defect for a human to rule on. The tool
never writes: all database access runs inside `BEGIN READ ONLY`.

Usage:
    python3 scripts/verify_itsm_migration_data.py [--evidence-out out.json] [--no-lineage]
    python3 scripts/verify_itsm_migration_data.py --self-test
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path

DEFAULT_EXPORT_DIR = '/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data'
DEFAULT_CLONE = ('ga-itsm-20260914', 'ga_owner', 'itsm_ga_ready')
LINEAGE = [
    ('itsm_migration_20260914', 'itsm-postgres-dev', 'itsm_user', 'itsm_migration_20260914', 'dev123'),
    ('itsm (DEV)', 'itsm-postgres-dev', 'itsm_user', 'itsm', 'dev123'),
    ('itsm_baseline_20260908', 'itsm-postgres-dev', 'itsm_user', 'itsm_baseline_20260908', 'dev123'),
]
ACTIVE_STATUS = 'userstatus01'
SEP = '\x1f'


# --------------------------------------------------------------------------- infrastructure

def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def clone_credentials() -> str:
    """Read the runtime password from the admitted profile; never print or store it."""
    profile = Path('/home/administrator/.local/state/itsm-backend-switch-20260915/profiles/ga/config.yaml')
    block = re.search(r'(?ms)^database:\n(.*?)(?=^\S)', profile.read_text())
    return re.search(r'password:\s*(\S+)', block.group(1)).group(1).strip("'\"")


def psql(container: str, user: str, db: str, password: str, sql: str) -> list[list[str]]:
    """Run a read-only query and return rows split on a unit separator.

    `-q` matters: when statements arrive on stdin, psql otherwise prints command tags such as
    `BEGIN` as ordinary output, which would be parsed as a row with far too few fields. Without
    ON_ERROR_STOP a failing statement would be reported on stderr while psql still exits 0, which
    would silently turn an error into an empty result.
    """
    a = subprocess.run(['docker', 'exec', '-i', '-e', 'PGPASSWORD=%s' % password, container,
                        'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-F', SEP,
                        '-U', user, '-d', db],
                       input='BEGIN READ ONLY;\n' + sql.rstrip().rstrip(';') + ';\nCOMMIT;',
                       text=True, capture_output=True)
    if a.returncode != 0:
        raise RuntimeError('psql failed for %s/%s: %s' % (container, db, a.stderr.strip()[:200]))
    return [line.split(SEP) for line in a.stdout.splitlines() if SEP in line]


DEPT_SQL = ("SELECT id, coalesce(code,''), coalesce(name,''), coalesce(parent_id::text,''), "
            "coalesce(org_type,''), coalesce(area_name,''), tenant_id::text, created_at::date::text "
            "FROM departments")
USER_SQL = ("SELECT id, coalesce(username,''), coalesce(email,''), coalesce(name,''), "
            "coalesce(department_id::text,''), coalesce(manager_id::text,''), active::text, "
            "tenant_id::text, created_at::date::text, left(coalesce(password_hash,''), 4), "
            "coalesce(department,'') FROM users")


def fetch_db(container: str, user: str, db: str, password: str) -> dict:
    depts = [dict(zip(('id', 'code', 'name', 'parent_id', 'org_type', 'area_name', 'tenant', 'created'), r))
             for r in psql(container, user, db, password, DEPT_SQL)]
    users = [dict(zip(('id', 'username', 'email', 'name', 'department_id', 'manager_id', 'active',
                       'tenant', 'created', 'hash_prefix', 'department_text'), r))
             for r in psql(container, user, db, password, USER_SQL)]
    return {'departments': depts, 'users': users}


# --------------------------------------------------------------------------- export loading

def load_exports(export_dir: Path) -> dict:
    depts = json.loads((export_dir / 'itsm_departments.json').read_text(encoding='utf-8'))
    users = json.loads((export_dir / 'itsm_users.json').read_text(encoding='utf-8'))
    return {'departments': depts, 'users': users}


def export_stats(exports: dict) -> dict:
    depts, users = exports['departments'], exports['users']
    active = [u for u in users if str(u.get('status')) == ACTIVE_STATUS]
    inactive = [u for u in users if str(u.get('status')) != ACTIVE_STATUS]
    return {
        'departments_total': len(depts),
        'departments_by_type': dict(Counter(str(d.get('departmentType')) for d in depts)),
        'departments_duplicate_ids': sum(1 for v in Counter(d['departmentId'] for d in depts).values() if v > 1),
        'departments_duplicate_names': sum(1 for v in Counter(d.get('departmentName') for d in depts).values() if v > 1),
        'users_total': len(users),
        'users_by_status': dict(Counter(str(u.get('status')) for u in users)),
        'users_active': len(active),
        'users_inactive': len(inactive),
        'users_duplicate_usernames': sum(1 for v in Counter(u['userName'] for u in users if u.get('userName')).values() if v > 1),
        'users_duplicate_emails': sum(1 for v in Counter((u.get('email') or '').strip().lower() for u in users).values() if v > 1),
        'users_with_department': sum(1 for u in users if u.get('departmentId')),
        'users_with_leader': sum(1 for u in users if u.get('leaderId')),
    }


# --------------------------------------------------------------------------- reconciliation

def reconcile(exports: dict, db: dict) -> dict:
    """Set-level comparison plus the attribution buckets for every difference."""
    exp_depts = {d['departmentId']: d for d in exports['departments'] if d.get('departmentId')}
    db_by_code = defaultdict(list)
    for d in db['departments']:
        db_by_code[d['code']].append(d)

    matched_dept_codes = set(exp_depts) & set(db_by_code)
    missing_depts = sorted(set(exp_depts) - set(db_by_code))          # in export, not in DB
    extra_dept_rows = [d for code, rows in db_by_code.items() for d in rows if code not in exp_depts]

    # duplicate codes in the DB are themselves a finding
    duplicate_dept_codes = {code: len(rows) for code, rows in db_by_code.items() if len(rows) > 1}

    exp_users = {u['userName']: u for u in exports['users'] if u.get('userName')}
    exp_active = {n for n, u in exp_users.items() if str(u.get('status')) == ACTIVE_STATUS}
    exp_inactive = set(exp_users) - exp_active
    db_by_username = defaultdict(list)
    for u in db['users']:
        db_by_username[u['username']].append(u)

    matched_users = set(exp_users) & set(db_by_username)
    missing_active = sorted(exp_active - set(db_by_username))
    missing_inactive = sorted(exp_inactive - set(db_by_username))
    extra_users = sorted(set(db_by_username) - set(exp_users))

    # attribution of the extra users: who are they?
    extra_class = Counter()
    for name in extra_users:
        row = db_by_username[name][0]
        if row['tenant'] != '1':
            extra_class['other tenant'] += 1
        elif row['created'] > '2026-08-19':
            extra_class['created after the migration batch'] += 1
        elif not re.match(r'^D\d+$', name):
            extra_class['non-legacy username pattern'] += 1
        else:
            extra_class['unexplained legacy-looking'] += 1

    # attribution of the extra departments: is the export a subset of a newer snapshot?
    extra_by_type = Counter(d['org_type'] or '(none)' for d in extra_dept_rows)
    extra_parent_resolves = Counter('parent present in db' if any(
        p['id'] == d['parent_id'] for p in db['departments']) else 'parent missing'
        for d in extra_dept_rows) if extra_dept_rows else Counter()

    return {
        'departments': {
            'export': len(exp_depts), 'db_rows': len(db['departments']),
            'matched': len(matched_dept_codes), 'only_export': len(missing_depts),
            'only_db': len(extra_dept_rows),
            'duplicate_codes_in_db': duplicate_dept_codes,
            'only_export_sample': missing_depts[:10],
            'only_db_by_org_type': dict(extra_by_type),
            'only_db_parent_linkage': dict(extra_parent_resolves),
        },
        'users': {
            'export_total': len(exp_users), 'export_active': len(exp_active),
            'db_rows': len(db['users']),
            'matched': len(matched_users),
            'only_export_active': len(missing_active),
            'only_export_inactive': len(missing_inactive),
            'only_export_active_sample': missing_active[:10],
            'only_db': len(extra_users),
            'only_db_attribution': dict(extra_class),
            'only_db_sample': extra_users[:10],
        },
    }


def alternative_key_checks(exports: dict, db: dict) -> dict:
    """Do the "missing" rows exist under a different key?

    Legacy usernames are not uniform (employee ids, login names, phone numbers), so a username-only
    comparison overstates the shortfall; email is the better identity key. For departments, the name
    is the only candidate key left once the code does not match.
    """
    def norm(v):
        return (v or '').strip().lower()

    exp_users = {u['userName']: u for u in exports['users'] if u.get('userName')}
    exp_active = {n for n, u in exp_users.items() if str(u.get('status')) == ACTIVE_STATUS}
    db_usernames = {u['username'] for u in db['users']}
    missing_active = sorted(exp_active - db_usernames)

    db_email = {norm(u['email']): u['username'] for u in db['users'] if norm(u['email'])}
    pairs = [(n, db_email.get(norm(exp_users[n].get('email')), '')) for n in missing_active]
    found_by_email = [n for n, e in pairs if e]
    different_username = [n for n, e in pairs if e and e != n]

    db_codes = {d['code'] for d in db['departments']}
    db_names = {norm(d['name']) for d in db['departments']}
    missing_depts = [d for d in exports['departments'] if d.get('departmentId') not in db_codes]
    by_name = [d for d in missing_depts if norm(d.get('departmentName')) in db_names]

    return {
        'users_missing_active': len(missing_active),
        'users_missing_active_found_by_email': len(found_by_email),
        'users_missing_active_under_different_username': len(different_username),
        'users_missing_active_no_email_match': len(missing_active) - len(found_by_email),
        'users_missing_active_sample': [
            {'username': n, 'email': exp_users[n].get('email'), 'db_username_for_email': e}
            for n, e in pairs[:8]],
        'departments_missing_total': len(missing_depts),
        'departments_missing_name_present_in_db': len(by_name),
        'departments_missing_uuid_style_ids': sum(1 for d in missing_depts if len(d.get('departmentId') or '') == 32),
        'departments_missing_code_style_ids': sum(1 for d in missing_depts if len(d.get('departmentId') or '') != 32),
        'departments_missing_sample': [
            {'id': d['departmentId'], 'name': d.get('departmentName'),
             'name_in_db': norm(d.get('departmentName')) in db_names} for d in missing_depts[:8]],
    }


# --------------------------------------------------------------------------- field and structure

def email_verdict(exported, stored) -> str:
    """Classify a stored email against the legacy one.

    Duplicate legacy addresses were made unique by inserting a suffix into the local part
    (`dup@x.com` -> `dup+2@x.com`), so comparing the whole address with startswith() is wrong.
    """
    stored = (stored or '').strip().lower()
    exported = (exported or '').strip().lower()
    if stored == exported:
        return 'exact'
    if '@' in exported and '@' in stored:
        exp_local, exp_domain = exported.rsplit('@', 1)
        got_local, got_domain = stored.rsplit('@', 1)
        if got_domain == exp_domain and got_local.startswith(exp_local) and len(got_local) > len(exp_local):
            return 'suffixed'
    return 'mismatch'


def field_checks(exports: dict, db: dict) -> dict:
    exp_users = {u['userName']: u for u in exports['users'] if u.get('userName')}
    db_by_username = defaultdict(list)
    for u in db['users']:
        db_by_username[u['username']].append(u)
    matched = sorted(set(exp_users) & set(db_by_username))

    dept_code_by_id = {d['id']: d['code'] for d in db['departments']}
    name_matches = email_matches = active_matches = department_matches = 0
    department_missing = 0
    department_mismatch_sample = []
    email_repaired = 0
    email_mismatch_sample = []
    bcrypt = 0
    legacy_hash_reused = 0
    for name in matched:
        row, exp = db_by_username[name][0], exp_users[name]
        if row['name'] == (exp.get('realName') or ''):
            name_matches += 1
        verdict = email_verdict(exp.get('email'), row['email'])
        if verdict == 'exact':
            email_matches += 1
        elif verdict == 'suffixed':
            email_repaired += 1          # duplicate email resolved with a suffix in the local part
        else:
            email_mismatch_sample.append((name, exp.get('email'), row['email']))
        want_active = str(exp.get('status')) == ACTIVE_STATUS
        if (row['active'] == 'true') == want_active:
            active_matches += 1
        # The migration bound users through the unit code, not the leaf department id: an existing
        # user's department code equals the export's `departmentUnit` (7,470 of 7,816), while the
        # leaf `departmentId` almost never equals it. Comparing against departmentId scored 0 and
        # was misleading.
        unit = (exp.get('departmentUnit') or '').strip()
        code = dept_code_by_id.get(row['department_id'])
        if not unit:
            continue
        if code == unit:
            department_matches += 1
        elif not row['department_id']:
            department_missing += 1
        else:
            department_mismatch_sample.append((name, unit, code))

    # Password storage is a security invariant of the whole table, not only of matched rows: every
    # hash must be bcrypt and no legacy password value may survive anywhere.
    legacy_prefixes = {(u.get('passWord') or '')[:4] for u in exports['users'] if u.get('passWord')}
    bcrypt = sum(1 for u in db['users'] if (u['hash_prefix'] or '').startswith(('$2a', '$2b', '$2y')))
    legacy_hash_reused = sum(1 for u in db['users']
                             if u['hash_prefix'] and u['hash_prefix'] in legacy_prefixes)
    non_bcrypt_sample = [u['username'] for u in db['users']
                         if not (u['hash_prefix'] or '').startswith(('$2a', '$2b', '$2y'))][:5]

    duplicate_usernames_db = sum(1 for v in Counter(u['username'] for u in db['users']).values() if v > 1)
    duplicate_emails_db = sum(1 for v in Counter((u['email'] or '').lower() for u in db['users']).values() if v > 1)
    return {
        'matched_users_checked': len(matched),
        'name_matches': name_matches,
        'email_exact_matches': email_matches,
        'email_matches_after_suffix_rule': email_repaired,
        'email_mismatch_sample': email_mismatch_sample[:5],
        'active_flag_matches': active_matches,
        'department_binding_matches': department_matches,
        'department_binding_absent': department_missing,
        'department_mismatch_sample': department_mismatch_sample[:5],
        'password_hash_bcrypt': bcrypt,
        'password_hash_legacy_reuse': legacy_hash_reused,
        'password_hash_non_bcrypt_sample': non_bcrypt_sample,
        'duplicate_usernames_in_db': duplicate_usernames_db,
        'duplicate_emails_in_db': duplicate_emails_db,
    }


def structural_checks(db: dict) -> dict:
    ids = {d['id'] for d in db['departments']}
    parent_ok = sum(1 for d in db['departments'] if not d['parent_id'] or d['parent_id'] in ids)
    roots = sum(1 for d in db['departments'] if not d['parent_id'] or d['parent_id'] not in ids)
    by_id = {d['id']: d for d in db['departments']}
    cycles = 0
    for d in db['departments']:
        seen, cur = set(), d
        while cur and cur['parent_id'] and cur['parent_id'] in by_id:
            if cur['id'] in seen:
                cycles += 1
                break
            seen.add(cur['id'])
            cur = by_id[cur['parent_id']]
    tenants = Counter(u['tenant'] for u in db['users'])
    return {
        'departments_with_resolvable_parent': parent_ok,
        'department_roots': roots,
        'department_cycles': cycles,
        'user_tenants': dict(tenants),
        'users_without_department': sum(1 for u in db['users'] if not u['department_id']),
        'users_with_manager': sum(1 for u in db['users'] if u['manager_id']),
    }


def run_lineage(no_lineage: bool) -> dict:
    if no_lineage:
        return {}
    out = {}
    for label, container, user, db, pw in LINEAGE:
        try:
            rows = psql(container, user, db, pw,
                        "SELECT 'departments', count(*)::text FROM departments "
                        "UNION ALL SELECT 'departments_created', min(created_at)::date::text FROM departments "
                        "UNION ALL SELECT 'users', count(*)::text FROM users "
                        "UNION ALL SELECT 'users_active', count(*)::text FROM users WHERE active "
                        "UNION ALL SELECT 'legacy_users', count(*)::text FROM users WHERE username ~ '^D[0-9]+$'")
            out[label] = {k: v for k, v in rows}
        except RuntimeError as failure:
            out[label] = 'unavailable: %s' % str(failure)[:80]
    return out


# --------------------------------------------------------------------------- self test

def self_test() -> int:
    """Synthetic fixture: the buckets must classify duplicates, inactive users and extras."""
    exports = {
        'departments': [
            {'departmentId': 'A', 'departmentName': 'root', 'parentId': '', 'departmentType': '1'},
            {'departmentId': 'B', 'departmentName': 'child', 'parentId': 'A', 'departmentType': '1'},
            {'departmentId': 'C', 'departmentName': 'dup name', 'parentId': 'A', 'departmentType': '1'},
        ],
        'users': [
            {'userName': 'D1', 'email': 'a@x.com', 'realName': 'A', 'status': ACTIVE_STATUS,
             'departmentId': 'B', 'leaderId': 'D2'},
            {'userName': 'D2', 'email': 'dup@x.com', 'realName': 'B', 'status': ACTIVE_STATUS,
             'departmentId': 'B'},
            {'userName': 'D3', 'email': 'dup@x.com', 'realName': 'C', 'status': ACTIVE_STATUS,
             'departmentId': 'C'},
            {'userName': 'D4', 'email': 'gone@x.com', 'realName': 'D', 'status': ACTIVE_STATUS,
             'departmentId': 'C'},
            {'userName': 'D5', 'email': 'inactive@x.com', 'realName': 'E', 'status': 'userstatus04'},
        ],
    }
    def u(**kw):
        base = dict(id='1', username='', email='', name='', department_id='', manager_id='',
                    active='true', tenant='1', created='2026-08-19', hash_prefix='$2a', department_text='')
        base.update(kw)
        return base
    db = {
        'departments': [
            dict(id='1', code='A', name='root', parent_id='', org_type='department', area_name='中国',
                 tenant='1', created='2026-08-19'),
            dict(id='2', code='B', name='child', parent_id='1', org_type='department', area_name='中国',
                 tenant='1', created='2026-08-19'),
            dict(id='3', code='Z', name='extra', parent_id='', org_type='warehouse', area_name='中国',
                 tenant='1', created='2026-08-19'),
        ],
        'users': [
            u(id='1', username='D1', email='a@x.com', name='A', department_id='2', manager_id='2'),
            u(id='2', username='D2', email='dup@x.com', name='B', department_id='2'),
            u(id='3', username='D3', email='dup+2@x.com', name='C', department_id=''),
            u(id='4', username='admin', email='admin@x.com', name='Admin', department_id='',
              hash_prefix='$2a'),
            u(id='5', username='D9', email='x@x.com', name='X', department_id='', hash_prefix='plain'),
        ],
    }
    buckets = reconcile(exports, db)
    fields = field_checks(exports, db)
    struct = structural_checks(db)
    problems = []
    if buckets['departments']['matched'] != 2 or buckets['departments']['only_export'] != 1 \
            or buckets['departments']['only_db'] != 1:
        problems.append('department buckets wrong: %s' % buckets['departments'])
    if buckets['users']['only_export_active'] != 1 or buckets['users']['only_db'] != 2:
        problems.append('user buckets wrong: %s' % buckets['users'])
    if fields['email_matches_after_suffix_rule'] != 1:
        problems.append('suffix-rule detection wrong: %s' % fields['email_matches_after_suffix_rule'])
    if fields['password_hash_bcrypt'] != 4 or fields['password_hash_legacy_reuse'] != 0:
        problems.append('bcrypt accounting wrong: %s/%s' % (fields['password_hash_bcrypt'],
                                                            fields['password_hash_legacy_reuse']))
    if struct['departments_with_resolvable_parent'] != 3 or struct['department_cycles'] != 0:
        problems.append('structure checks wrong: %s' % struct)
    for p in problems:
        print('SELF-TEST FAIL:', p)
    if not problems:
        print('self-test passed (bucketing, suffix rule, bcrypt accounting, structure)')
    return 1 if problems else 0


# --------------------------------------------------------------------------- main

def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--export-dir', default=DEFAULT_EXPORT_DIR)
    parser.add_argument('--evidence-out')
    parser.add_argument('--no-lineage', action='store_true')
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.self_test:
        return self_test()

    export_dir = Path(args.export_dir)
    exports = load_exports(export_dir)
    stats = export_stats(exports)
    container, user, db = DEFAULT_CLONE
    clone = fetch_db(container, user, db, clone_credentials())
    result = {
        'generated_at': __import__('datetime').datetime.now(__import__('datetime').timezone.utc).isoformat(),
        'source_of_truth': {
            'dir': str(export_dir),
            'itsm_departments.json': {'sha256': sha256(export_dir / 'itsm_departments.json'),
                                      'mtime': (export_dir / 'itsm_departments.json').stat().st_mtime},
            'itsm_users.json': {'sha256': sha256(export_dir / 'itsm_users.json'),
                                'mtime': (export_dir / 'itsm_users.json').stat().st_mtime},
        },
        'export_stats': stats,
        'target': {'container': container, 'user': user, 'database': db},
        'reconciliation': reconcile(exports, clone),
        'alternative_key_checks': alternative_key_checks(exports, clone),
        'field_checks': field_checks(exports, clone),
        'structure': structural_checks(clone),
        'lineage': run_lineage(args.no_lineage),
    }
    text = json.dumps(result, indent=2, ensure_ascii=False)
    if args.evidence_out:
        Path(args.evidence_out).write_text(text + '\n', encoding='utf-8')
    print(text)
    return 0


if __name__ == '__main__':
    sys.exit(main())
