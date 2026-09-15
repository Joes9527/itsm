"""The only write path: preflight, dry run, idempotent execution, evidence and rollback."""
from __future__ import annotations

import json

from migration.checks.base import WriteIntent
from migration.profile import EntitySpec


def preflight(intents: list[WriteIntent], spec: EntitySpec, target_rows: list[dict]
              ) -> tuple[list[WriteIntent], list[dict]]:
    """Resolve every declared check before anything is written; a single block stops the batch."""
    key_field = spec.keys_target
    existing = {row[key_field] for row in target_rows}
    existing_emails = {str(row.get('email', '')).lower() for row in target_rows}
    allowed: list[WriteIntent] = []
    blocked: list[dict] = []
    for intent in intents:
        problems = []
        for check in spec.write.preflight:
            if check == 'username_absent' and intent.key in existing:
                problems.append('username_absent: %s already exists' % intent.key)
            elif check == 'email_absent' and str(intent.payload.get('email', '')).lower() in existing_emails:
                problems.append('email_absent: the address for %s already exists' % intent.key)
            elif check == 'department_resolves' and not intent.payload.get('departmentId'):
                problems.append('department_resolves: no department resolved for %s' % intent.key)
        (blocked.append({'key': intent.key, 'reason': '; '.join(problems)}) if problems
         else allowed.append(intent))
    return allowed, blocked


def run(profile, entity_name: str, apply: bool, target, check, src, tgt, evidence,
        spec: EntitySpec | None = None) -> int:
    spec = spec or profile.entities[entity_name]
    if not spec.write or not spec.write.enabled:
        print('write is not enabled for %s in the profile; nothing to do' % entity_name)
        return 0

    intents = check.render_write_plan(src, tgt, spec)
    allowed, blocked = preflight(intents, spec, tgt)
    if blocked:
        print('preflight blocked %d record(s); refusing to write' % len(blocked))
        for item in blocked[:20]:
            print('  %s -> %s' % (item['key'], item['reason']))
        if evidence:
            evidence.add('backfill_blocked', blocked)
        return 2
    if not allowed:
        print('nothing to create for %s' % entity_name)
        if evidence:
            evidence.add('backfill_intents', [])
        return 0
    if not apply:
        print('dry run for %s: %d record(s) would be created' % (entity_name, len(allowed)))
        for intent in allowed[:20]:
            print('  %s -> %s' % (intent.key, json.dumps(intent.payload, ensure_ascii=False)))
        if evidence:
            evidence.add('backfill_intents', [{'key': i.key, 'payload': i.payload} for i in allowed])
        return 0

    try:
        password = spec.write.password.resolve('the migrated default password')
    except Exception as exc:                                  # ProfileError
        print('refusing to apply: %s' % exc)
        return 1

    csrf = target.api_login()
    results = []
    path = spec.write.endpoint.split(' ', 1)[1]
    for intent in allowed:
        body = dict(intent.payload, password=password)
        status, payload = target.api_post(path, body, csrf)
        created_id = (payload.get('data') or {}).get('id') if payload.get('code') == 0 else None
        results.append({'key': intent.key, 'http': status, 'code': payload.get('code'),
                        'message': payload.get('message'), 'created_id': created_id})
        csrf = target.api_login()                 # a successful mutation rotates the token
    created = [result for result in results if result['created_id']]
    print('created %d of %d' % (len(created), len(allowed)))
    if evidence:
        evidence.add('backfill_results', results)
        evidence.add('backfill_rollback', {
            'disable': [spec.write.rollback.get('disable', '').replace(':id', str(result['created_id']))
                        for result in created],
            'sql_delete': 'DELETE FROM %s WHERE id IN (%s);'
                          % (spec.target_table, ', '.join(str(result['created_id'])
                                                          for result in created)),
        })
    return 0 if len(created) == len(allowed) else 1
