#!/usr/bin/env python3
"""Generate idempotent SQL for batch B1 (category / asset landing).

Two parts, both confirmed with the maintainer on 2026-09-14:

* B1a/B1d - create CMDB configuration items for the 46 legacy CTI leaves
  (43 business systems + 3 infrastructure leaves). Containers are not created as
  CIs; their hierarchy is kept in ``attributes.legacyPath``.
* B1b - create the three new ticket categories requested by the legacy CTI
  service nodes.

B1c (17 org/location nodes) is owned by Phase 1 identity and B1b's four mappings
plus the OA申请 deletion touch no target rows, so neither is emitted here.
Data source: ``data/b1_ci_assets.json`` (reviewable, derived from the CTI
worksheet).
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from generate_seed_sql import json_lit, lit

SOURCE_SYSTEM = "keas-itsm-test"

# B1b: confirmed new categories (parent_code, code, name, itsm_type, priority, sla_tier)
NEW_CATEGORIES: tuple[dict, ...] = (
    {"parent_code": "COL-MAIL", "code": "COL-MAIL-004", "name": "邮箱导出申请",
     "itsm_type": "Request", "default_priority": "P3", "sla_tier": "标准服务"},
    {"parent_code": "ACC-LCM", "code": "ACC-LCM-003", "name": "业务系统账号申请",
     "itsm_type": "Request", "default_priority": "P3", "sla_tier": "标准服务"},
    {"parent_code": "APP-GEN", "code": "APP-GEN-SVC-001", "name": "业务系统服务申请",
     "itsm_type": "Request", "default_priority": "P3", "sla_tier": "应用支持服务"},
)


def build_sql(assets: list[dict], tenant_id: int) -> str:
    out: list[str] = ["-- B1 category/asset landing (idempotent).", "BEGIN;", ""]

    # B1b: new categories (parent resolved by code)
    for c in NEW_CATEGORIES:
        out.append(
            "INSERT INTO ticket_categories (name, description, code, level, sort_order, is_active, "
            "tenant_id, itsm_type, default_priority, sla_tier, is_user_facing, created_at, updated_at, parent_id) "
            f"SELECT {lit(c['name'])}, '', {lit(c['code'])}, 3, 40, true, {lit(tenant_id)}, "
            f"{lit(c['itsm_type'])}, {lit(c['default_priority'])}, {lit(c['sla_tier'])}, true, now(), now(), "
            f"(SELECT id FROM ticket_categories WHERE code={lit(c['parent_code'])}) "
            f"WHERE NOT EXISTS (SELECT 1 FROM ticket_categories WHERE code={lit(c['code'])});"
        )
    out.append("")

    # B1a/B1d: CMDB CIs, one per legacy leaf
    for a in assets:
        attrs = {
            "legacyCtiId": a["legacyCtiId"],
            "legacyPath": a["legacyPath"],
            "sourceSystem": SOURCE_SYSTEM,
            "routeRefs": a.get("routeRefs", 0),
        }
        guard = (
            f"NOT EXISTS (SELECT 1 FROM configuration_items WHERE tenant_id={lit(tenant_id)} "
            f"AND attributes->>'legacyCtiId'={lit(a['legacyCtiId'])})"
        )
        out.append(
            "INSERT INTO configuration_items (name, ci_type, ci_type_id, status, environment, criticality, "
            "source, attributes, tenant_id, created_at, updated_at) "
            f"SELECT {lit(a['name'])}, {lit(a['ci_type'])}, "
            f"(SELECT id FROM ci_types WHERE name={lit(a['ci_type'])} AND tenant_id={lit(tenant_id)} LIMIT 1), "
            f"'active', 'production', 'medium', 'legacy_itsm_cti', {json_lit(attrs)}, {lit(tenant_id)}, now(), now() "
            f"WHERE {guard};"
        )

    out += ["", "COMMIT;", ""]
    return "\n".join(out)


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent B1 category/asset SQL")
    p.add_argument("--assets", required=True)
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--out", required=True)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    assets = json.loads(Path(args.assets).read_text(encoding="utf-8"))
    sql = build_sql(assets, args.tenant_id)
    Path(args.out).write_text(sql, encoding="utf-8")
    print(f"wrote {args.out} ({len(sql.splitlines())} lines)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
