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

from generate_seed_sql import json_lit, lit


def build_sql(business_hours: dict, tenant_id: int) -> str:
    blob = json_lit(business_hours)
    return "\n".join([
        "-- B4 SLA business calendar (idempotent).",
        "BEGIN;",
        "",
        "UPDATE sla_definitions SET business_hours = " + blob + ", updated_at = now() "
        f"WHERE tenant_id = {lit(tenant_id)} AND business_hours IS DISTINCT FROM {blob};",
        "",
        "COMMIT;",
        "",
    ])


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent B4 business-hours SQL")
    p.add_argument("--business-hours", required=True)
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--out", required=True)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    data = json.loads(Path(args.business_hours).read_text(encoding="utf-8"))
    Path(args.out).write_text(build_sql(data, args.tenant_id), encoding="utf-8")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
