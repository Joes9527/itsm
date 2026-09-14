#!/usr/bin/env python3
"""Generate idempotent SQL for batch B4 (SLA business calendar).

Writes ``sla_definitions.business_hours`` for tenant 1 from the reviewable data
file ``data/b4_business_hours.json`` (work days Mon-Fri, 09:00-18:00 as chosen by
the maintainer, Asia/Shanghai, 89 national holiday dates for 2024-2026).

Known capability gaps (registered in the evidence doc, not worked around here):
the runtime parser ignores ``time_zone``, cannot express a lunch break (single
continuous period) and cannot express weekend make-up workdays.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from generate_seed_sql import json_lit, lit, require_sql
from batch_receipt import add_context_argument, write_batch


# Exact SLA identities admitted from the fixed 0788 seed, not all tenant SLAs.
SLA_IDENTITIES = (
    ('Incident-P0-紧急', 'incident', 'urgent'), ('Incident-P1-高', 'incident', 'high'),
    ('Incident-P2-中', 'incident', 'medium'), ('Incident-P3-低', 'incident', 'low'),
    ('ServiceRequest-标准', 'service_request', 'medium'), ('Change-普通', 'change', 'medium'),
    ('Change-紧急', 'change', 'high'),
)


def batch_targets(tenant_id):
    return [('sla_definitions', f'tenant_id={tenant_id} AND name={lit(name)}')
            for name, _, _ in SLA_IDENTITIES]


def build_dml(business_hours: dict, tenant_id: int) -> str:
    blob = json_lit(business_hours)
    guards = []
    for name, service_type, priority in SLA_IDENTITIES:
        guards.append(require_sql(f"(SELECT count(*) FROM sla_definitions WHERE tenant_id={tenant_id} AND name={lit(name)})=1 "
                                  f"AND EXISTS (SELECT 1 FROM sla_definitions WHERE tenant_id={tenant_id} AND name={lit(name)} "
                                  f"AND service_type={lit(service_type)} AND priority={lit(priority)})", 'admitted SLA identity missing or changed'))
    names = ','.join(lit(name) for name, _, _ in SLA_IDENTITIES)
    return "\n".join([
        "-- B4 SLA business calendar (idempotent).",
        "BEGIN;",
        *guards,
        "",
        "UPDATE sla_definitions SET business_hours = " + blob + ", updated_at = now() "
        f"WHERE tenant_id = {lit(tenant_id)} AND name IN ({names}) AND business_hours IS DISTINCT FROM {blob};",
        "",
        "COMMIT;",
        "",
    ])


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent B4 business-hours SQL")
    p.add_argument("--business-hours", required=True)
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--out", required=True)
    add_context_argument(p)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    data = json.loads(Path(args.business_hours).read_text(encoding="utf-8"))
    write_batch(args, build_dml(data, args.tenant_id), 'B4-calendar-20260914', data,
                batch_targets(args.tenant_id), {'business_hours': args.business_hours})
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
