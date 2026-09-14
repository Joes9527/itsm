#!/usr/bin/env python3
"""Generate idempotent SQL for batch B2 (dictionary option landing).

Applies the dispositions the maintainer accepted on 2026-09-14 in
docs/review/2026-09-14-dictionary-option-reconciliation.md:

* "新增选项" rows are appended to the matching seed template field options;
* "归并" and "排除" rows need no target write and are recorded in the evidence
  doc instead.

Only ``field_definitions.options`` for tenant 1 is touched; identity, history and
other config are untouched.
"""

from __future__ import annotations

import argparse
from pathlib import Path

from generate_seed_sql import json_lit, lit

# (template name, field name, option label, option value) - accepted "new option" rows.
ADDITIONS: tuple[tuple[str, str, str, str], ...] = (
    # 系统名称 -> target_system (three templates use that field)
    ("账号申请", "target_system", "BMS系统", "bms"),
    ("账号申请", "target_system", "KAMS", "kams"),
    ("账号申请", "target_system", "Yonyou", "yonyou"),
    ("账号申请", "target_system", "FLUX", "flux"),
    ("账号申请", "target_system", "HR休假系统", "hr_leave"),
    ("账号申请", "target_system", "K3.5系统", "k3_5"),
    ("账号申请", "target_system", "SQR系统", "sqr"),
    ("业务系统服务申请", "target_system", "BMS系统", "bms"),
    ("业务系统服务申请", "target_system", "KAMS", "kams"),
    ("业务系统服务申请", "target_system", "Yonyou", "yonyou"),
    ("业务系统服务申请", "target_system", "FLUX", "flux"),
    ("业务系统服务申请", "target_system", "HR休假系统", "hr_leave"),
    ("业务系统服务申请", "target_system", "K3.5系统", "k3_5"),
    ("业务系统服务申请", "target_system", "SQR系统", "sqr"),
    ("通用服务申请", "target_system", "BMS系统", "bms"),
    ("通用服务申请", "target_system", "KAMS", "kams"),
    ("通用服务申请", "target_system", "Yonyou", "yonyou"),
    ("通用服务申请", "target_system", "FLUX", "flux"),
    ("通用服务申请", "target_system", "HR休假系统", "hr_leave"),
    ("通用服务申请", "target_system", "K3.5系统", "k3_5"),
    ("通用服务申请", "target_system", "SQR系统", "sqr"),
    # 请求类型 -> 通用服务申请/service_type
    ("通用服务申请", "service_type", "数据导出", "data_export"),
    ("通用服务申请", "service_type", "资产采购", "asset_purchase"),
    # 邮箱申请类别 -> 邮箱服务申请/operation
    ("邮箱服务申请", "operation", "群组邮箱", "group_mailbox"),
    ("邮箱服务申请", "operation", "公共邮箱", "public_mailbox"),
)


def build_sql(additions, tenant_id: int) -> str:
    out: list[str] = ["-- B2 dictionary option landing (idempotent).", "BEGIN;", ""]
    for template, field, label, value in additions:
        option = json_lit([{"label": label, "value": value}])
        guard_present = f"f.options @> {option}"
        out.append(
            "UPDATE field_definitions f SET options = f.options || "
            f"{option}, updated_at = now() "
            f"WHERE f.tenant_id = {lit(tenant_id)} AND f.name = {lit(field)} "
            f"AND f.entity_id = (SELECT id FROM ticket_templates WHERE tenant_id = {lit(tenant_id)} "
            f"AND name = {lit(template)}) AND NOT ({guard_present});"
        )
    out += ["", "COMMIT;", ""]
    return "\n".join(out)


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent B2 option-add SQL")
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--out", required=True)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    sql = build_sql(ADDITIONS, args.tenant_id)
    Path(args.out).write_text(sql, encoding="utf-8")
    print(f"wrote {args.out} ({len(sql.splitlines())} lines, {len(ADDITIONS)} additions)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
