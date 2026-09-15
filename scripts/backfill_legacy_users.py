#!/usr/bin/env python3
"""Backfill the legacy users the 2026-08-19 migration dropped.

Creates the 18 HR-linked active users that exist in the SOP export but not in the database, through
the product's own endpoint (POST /api/v1/users) so validation, password hashing and audit all come
from the product rather than from a hand-written insert.

Field mapping was derived from the 7,816 users the original migration did create, not guessed:
  * role      -> end_user (7,812 of 7,816)
  * departmentId -> the department whose code equals the export's `departmentUnit` (7,470 matches)
  * the batch left gender, is_leader, function_line, manager and phone empty, so those are not sent
    (the export's leaderId is an HR identifier that does not resolve to a product user)
  * email follows the intentional development rewrite to `<local>@keas.kln.comm`
  * password follows the SOP default for migrated users (never written to the evidence file)

Dry run is the default; `--apply` performs the writes. Idempotent: an existing username is skipped.

Usage:
    python3 scripts/backfill_legacy_users.py                      # dry run
    python3 scripts/backfill_legacy_users.py --apply --evidence-out backfill.json
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

DATA = Path('/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data')
PROFILE = Path('/home/administrator/.local/state/itsm-backend-switch-20260915/profiles/ga/config.yaml')
BASE = 'http://localhost:3010'          # the running environment's web entry
TENANT_CODE, ADMIN_USER, ADMIN_PASS = 'default', 'admin', 'admin123'
MIGRATED_PASSWORD = 'P@ssw0rd2026!'     # SOP default for migrated users
TARGET_USERNAMES = [
    'D78089', 'D83223', 'D83360', 'D83364', 'D83473', 'H83436', 'H83510', 'D84725', 'D84735',
    'H82255', 'H84813', 'H84410', 'H84946', 'H84947', 'D33743', 'D44967', 'D50002', 'D52745',
]


def db_password() -> str:
    block = re.search(r'(?ms)^database:\n(.*?)(?=^\S)', PROFILE.read_text()).group(1)
    return re.search(r'password:\s*(\S+)', block).group(1).strip("'\"")


def query(sql: str) -> list[list[str]]:
    a = subprocess.run(['docker', 'exec', '-i', '-e', 'PGPASSWORD=' + db_password(),
                        'ga-itsm-20260914', 'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-A', '-t',
                        '-F', '|', '-U', 'ga_owner', '-d', 'itsm_ga_ready'],
                       input='BEGIN READ ONLY;\n' + sql + ';\nCOMMIT;', text=True, capture_output=True)
    if a.returncode != 0:
        raise RuntimeError(a.stderr.strip()[:200])
    return [l.split('|') for l in a.stdout.splitlines() if '|' in l]


def rewrite_email(exported: str) -> str:
    """Apply the migration's intentional development rewrite (real addresses must not be mailed)."""
    local = (exported or '').split('@')[0].strip() or 'noreply'
    return '%s@keas.kln.comm' % local


class Api:
    def __init__(self):
        self.cookies: dict[str, str] = {}

    def request(self, method, path, body=None, headers=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(BASE + path, data=data, method=method)
        req.add_header('Content-Type', 'application/json')
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        if self.cookies:
            req.add_header('Cookie', '; '.join('%s=%s' % kv for kv in self.cookies.items()))
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                status, text = r.status, r.read().decode()
                cookies = r.headers.get_all('Set-Cookie') or []
        except urllib.error.HTTPError as e:
            status, text = e.code, e.read().decode()
            cookies = e.headers.get_all('Set-Cookie') or []
        for cookie in cookies:
            name, _, rest = cookie.partition('=')
            self.cookies[name.strip()] = rest.split(';')[0]
        return status, text

    def login(self):
        status, text = self.request('POST', '/api/v1/auth/login',
                                    {'tenantCode': TENANT_CODE, 'username': ADMIN_USER,
                                     'password': ADMIN_PASS})
        if status != 200:
            raise RuntimeError('admin login failed: %s' % text[:120])
        return json.loads(self.request('GET', '/api/v1/csrf-token')[1])['data']['csrf_token']


def build_payloads() -> list[dict]:
    users = json.loads((DATA / 'itsm_users.json').read_text(encoding='utf-8'))
    by_name = {}
    for u in users:
        if u.get('userName') and u['userName'] not in by_name:
            by_name[u['userName']] = u
    dept_by_code = {r[1]: r[0] for r in query('SELECT id::text, code FROM departments') if len(r) == 2}
    existing = {r[0] for r in query('SELECT username FROM users')}
    existing_emails = {r[0].strip().lower() for r in query('SELECT email FROM users') if r[0].strip()}

    payloads = []
    for name in TARGET_USERNAMES:
        user = by_name.get(name)
        if not user:
            payloads.append({'username': name, 'status': 'not-in-export'})
            continue
        unit = (user.get('departmentUnit') or '').strip()
        payload = {
            'username': name,
            'name': user.get('realName') or name,
            'email': rewrite_email(user.get('email')),
            'departmentId': int(dept_by_code.get(unit, 0) or 0),
            'role': 'end_user',
            'tenantId': 1,
        }
        problems = []
        if name in existing:
            problems.append('username already exists')
        if payload['email'].lower() in existing_emails:
            problems.append('email already exists')
        if not payload['departmentId']:
            problems.append('departmentUnit %r does not resolve to a department' % unit)
        payloads.append({
            'username': name, 'payload': payload, 'export_department_unit': unit,
            'export_email': user.get('email'), 'export_status': user.get('status'),
            'preflight': 'ok' if not problems else '; '.join(problems),
        })
    return payloads


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--apply', action='store_true', help='perform the writes (default: dry run)')
    parser.add_argument('--evidence-out')
    args = parser.parse_args()

    payloads = build_payloads()
    blocked = [p for p in payloads if p.get('preflight') != 'ok']
    print('users to create: %d ; preflight ok: %d ; blocked: %d'
          % (len(payloads), len(payloads) - len(blocked), len(blocked)))
    for p in payloads:
        if 'payload' in p:
            print('  %-8s deptId=%-6s unit=%-14s email=%-32s %s'
                  % (p['username'], p['payload']['departmentId'], p['export_department_unit'],
                     p['payload']['email'], p['preflight']))
        else:
            print('  %-8s %s' % (p['username'], p.get('status')))

    evidence = {
        'mode': 'apply' if args.apply else 'dry-run',
        'mapping_rules': {
            'role': 'end_user (derived from 7812/7816 existing migrated users)',
            'department': 'departmentId resolved from the export departmentUnit',
            'email': 'local part preserved, domain intentionally rewritten to keas.kln.comm',
            'password': 'SOP default for migrated users (not recorded here)',
            'not_sent': 'gender, isLeader, functionLine, managerId, phone: the original batch left them empty',
        },
        'payloads': payloads,
        'results': [],
    }
    if blocked:
        print('\npreflight blocked %d user(s); refusing to write' % len(blocked))
        if args.evidence_out:
            Path(args.evidence_out).write_text(json.dumps(evidence, indent=2, ensure_ascii=False))
        return 2
    if not args.apply:
        print('\ndry run only; no user was created (re-run with --apply)')
        if args.evidence_out:
            Path(args.evidence_out).write_text(json.dumps(evidence, indent=2, ensure_ascii=False))
        return 0

    api = Api()
    csrf = api.login()
    for p in payloads:
        body = dict(p['payload'], password=MIGRATED_PASSWORD)
        status, text = api.request('POST', '/api/v1/users', body, {'X-CSRF-Token': csrf})
        try:
            code = json.loads(text).get('code')
            message = json.loads(text).get('message')
        except Exception:
            code, message = None, text[:80]
        created_id = None
        if status == 200 and code == 0:
            created_id = json.loads(text)['data'].get('id')
        p['result'] = {'http': status, 'code': code, 'message': message, 'created_id': created_id}
        evidence['results'].append({'username': p['username'], 'http': status, 'code': code,
                                    'message': message, 'created_id': created_id})
        # a successful mutation rotates the CSRF token
        csrf = json.loads(api.request('GET', '/api/v1/csrf-token')[1])['data']['csrf_token']

    created = [r for r in evidence['results'] if r['created_id']]
    evidence['created'] = created
    evidence['rollback'] = {
        'disable_via_api': ['PUT /api/v1/users/%s/status {"active": false}' % r['created_id'] for r in created],
        'sql_delete': "DELETE FROM users WHERE id IN (%s);" % ', '.join(str(r['created_id']) for r in created),
    }
    print('\ncreated %d/%d' % (len(created), len(payloads)))
    for r in evidence['results']:
        print('  %-8s http=%s code=%s id=%s %s' % (r['username'], r['http'], r['code'],
                                                   r['created_id'], (r['message'] or '')[:40]))
    if args.evidence_out:
        Path(args.evidence_out).write_text(json.dumps(evidence, indent=2, ensure_ascii=False))
    return 0 if len(created) == len(payloads) else 1


if __name__ == '__main__':
    sys.exit(main())
