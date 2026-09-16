"""Backfill planning; application is blocked until target binding is verified."""
from __future__ import annotations

from .checks.base import WriteIntent
from .report import tokenise
from .profile import EntitySpec


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
    if apply:
        print('backfill --apply blocked: API/database target binding and tenant admission are not verified')
        if evidence:
            evidence.add('backfill_admission', {'status': 'blocked', 'reason': 'target binding unverified'})
        return 2
    spec = spec or profile.entities[entity_name]
    if not spec.write or not spec.write.enabled:
        print('write is not enabled for %s in the profile; nothing to do' % entity_name)
        return 0

    intents = check.render_write_plan(src, tgt, spec)
    allowed, blocked = preflight(intents, spec, tgt)
    if blocked:
        print('preflight blocked %d record(s); refusing to write' % len(blocked))
        for item in blocked[:20]:
            print('  %s: preflight rejected' % tokenise(item['key']))
        if evidence:
            evidence.add('backfill_blocked', [{'key': tokenise(i['key']), 'blocked': True} for i in blocked])
        return 2
    if not allowed:
        print('nothing to create for %s' % entity_name)
        if evidence:
            evidence.add('backfill_intents', [])
        return 0
    if not apply:
        print('dry run for %s: %d record(s) would be created' % (entity_name, len(allowed)))
        for intent in allowed[:20]:
            print('  %s: fields %s' % (tokenise(intent.key), ', '.join(sorted(intent.payload))))
        if evidence:
            evidence.add('backfill_intents', [{'key': tokenise(i.key), 'fields': sorted(i.payload)} for i in allowed])
        return 0
