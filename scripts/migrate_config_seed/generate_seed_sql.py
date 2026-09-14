#!/usr/bin/env python3
"""Generate idempotent SQL that admits the fixed ITSM seed config into a target DB.

Used by handoff B0 (see docs/migrations/2026-09-14-b0-seed-admission-dry-run.md).
Read-only against the source seed; it only *generates* SQL. Apply the output with
psql in a single transaction (`psql -1 -v ON_ERROR_STOP=1`).

Scope: the non-binding config sections only. departments/teams/roles, history and
process_bindings are deliberately excluded.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any

EXCLUDED = {
    "departments", "teams", "roles", "process_bindings", "sla_policies",
    "incident_categories", "incidents", "problems", "changes", "knowledge_articles",
}
CATEGORY_SECTIONS = {"ticket_categories"}


def lit(value: Any) -> str:
    if value is None:
        return "NULL"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    return "'" + str(value).replace("'", "''") + "'"


def json_lit(value: Any) -> str:
    return "'" + json.dumps(value, ensure_ascii=False).replace("'", "''") + "'::jsonb"


def _cols(pairs: list[tuple[str, str]]) -> tuple[str, str]:
    return ", ".join(name for name, _ in pairs), ", ".join(value for _, value in pairs)


def require_sql(condition: str, message: str) -> str:
    return f"DO $guard$ BEGIN IF NOT ({condition}) THEN RAISE EXCEPTION {lit(message)}; END IF; END $guard$;"


def category_lookup(code: str, tenant_id: int) -> str:
    return f"SELECT id FROM ticket_categories WHERE code={lit(code)} AND tenant_id={lit(tenant_id)}"


def category_insert(cols: list[tuple[str, str]]) -> str:
    values_by_name = dict(cols)
    identity = f"code={values_by_name['code']}"
    same = ' AND '.join(f"{name} IS NOT DISTINCT FROM {value}" for name, value in cols
                        if name not in ('created_at', 'updated_at'))
    names, values = _cols(cols)
    return require_sql(f"NOT EXISTS (SELECT 1 FROM ticket_categories WHERE {identity} AND NOT ({same}))",
                       'category tenant or content conflict') + '\n' + (
        f"INSERT INTO ticket_categories ({names}) VALUES ({values}) ON CONFLICT (code) DO NOTHING;")


def build_sql(seed: dict[str, Any], tenant_id: int, created_by: int) -> str:
    out: list[str] = ["-- B0 seed admission (idempotent). Generated, do not hand-edit.", "BEGIN;", ""]

    def insert(table: str, cols: list[tuple[str, str]], guard: str) -> None:
        names, values = _cols(cols)
        out.append(f"INSERT INTO {table} ({names}) SELECT {values} WHERE {guard};")

    # 1. categories, roots first then children (parent resolved by code)
    cats = sorted(seed["ticket_categories"], key=lambda c: (c.get("level") or 1, c.get("sort_order") or 0))
    for c in cats:
        parent = f"({category_lookup(c['parent_code'], tenant_id)})" if c.get("parent_code") else "NULL"
        if c.get('parent_code'):
            out.append(require_sql(f"(SELECT count(*) FROM ({category_lookup(c['parent_code'], tenant_id)}) p)=1", 'category parent missing in tenant'))
        cols = [
            ("name", lit(c["name"])), ("description", lit(c.get("description"))), ("code", lit(c["code"])),
            ("level", lit(c.get("level") or 1)), ("sort_order", lit(c.get("sort_order") or 0)),
            ("is_active", "true"), ("tenant_id", lit(tenant_id)),
            ("itsm_type", lit(c.get("itsm_type"))), ("default_priority", lit(c.get("default_priority"))),
            ("sla_tier", lit(c.get("sla_tier"))), ("default_resolver", lit(c.get("default_resolver"))),
            ("is_user_facing", lit(c.get("is_user_facing", True))),
            ("created_at", "now()"), ("updated_at", "now()"), ("parent_id", parent),
        ]
        out.append(category_insert(cols))
    out.append("")

    # 2. templates + fields
    for t in seed["ticket_templates"]:
        cat_codes = t.get("category_codes") or ([t["category"]] if t.get("category") else [])
        for code in cat_codes:
            out.append(require_sql(f"(SELECT count(*) FROM ({category_lookup(code, tenant_id)}) c)=1", 'template category missing in tenant'))
        cat_ids = "jsonb_build_array(" + ", ".join(
            f"({category_lookup(code, tenant_id)})" for code in cat_codes
        ) + ")" if cat_codes else "'[]'::jsonb"
        guard = f"NOT EXISTS (SELECT 1 FROM ticket_templates WHERE tenant_id={lit(tenant_id)} AND name={lit(t['name'])})"
        insert("ticket_templates", [
            ("name", lit(t["name"])), ("description", lit(t.get("description"))),
            ("category", lit(t.get("category"))), ("priority", lit(t.get("priority") or "medium")),
            ("category_ids", cat_ids), ("is_active", lit(t.get("is_active", True))),
            ("tenant_id", lit(tenant_id)), ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)
        for f in t.get("fields", []):
            entity = f"(SELECT id FROM ticket_templates WHERE tenant_id={lit(tenant_id)} AND name={lit(t['name'])})"
            fguard = (
                f"NOT EXISTS (SELECT 1 FROM field_definitions WHERE tenant_id={lit(tenant_id)} "
                f"AND entity_type='ticket_template' AND entity_id={entity} AND name={lit(f['name'])})"
            )
            insert("field_definitions", [
                ("tenant_id", lit(tenant_id)), ("entity_type", "'ticket_template'"), ("entity_id", entity),
                ("name", lit(f["name"])), ("label", lit(f.get("label"))), ("field_type", lit(f.get("field_type") or "text")),
                ("required", lit(f.get("required", False))),
                ("options", json_lit(f.get("options") or [])), ("sort_order", lit(f.get("sort_order") or 0)),
                ("is_active", "true"), ("created_at", "now()"), ("updated_at", "now()"),
            ], fguard)
    out.append("")

    # 3. sla definitions
    for s in seed["sla_definitions"]:
        guard = f"NOT EXISTS (SELECT 1 FROM sla_definitions WHERE tenant_id={lit(tenant_id)} AND name={lit(s['name'])})"
        insert("sla_definitions", [
            ("name", lit(s["name"])), ("description", lit(s.get("description"))),
            ("service_type", lit(s.get("service_type"))), ("priority", lit(s.get("priority"))),
            ("response_time", lit(s.get("response_time") or 30)), ("resolution_time", lit(s.get("resolution_time") or 240)),
            ("is_active", "true"), ("tenant_id", lit(tenant_id)), ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 4. service catalog
    for s in seed["service_catalog"]:
        guard = f"NOT EXISTS (SELECT 1 FROM service_catalogs WHERE tenant_id={lit(tenant_id)} AND name={lit(s['name'])})"
        insert("service_catalogs", [
            ("name", lit(s["name"])), ("description", lit(s.get("description"))), ("category", lit(s.get("category"))),
            ("service_type", lit(s.get("service_type") or "custom")), ("target_class", lit(s.get("target_class"))),
            ("delivery_time", lit(s.get("delivery_time"))), ("requires_approval", lit(s.get("requires_approval", True))),
            ("status", "'active'"), ("is_active", "true"), ("tenant_id", lit(tenant_id)),
            ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 5. CI types
    for c in seed["ci_types"]:
        guard = f"NOT EXISTS (SELECT 1 FROM ci_types WHERE tenant_id={lit(tenant_id)} AND name={lit(c['name'])})"
        insert("ci_types", [
            ("name", lit(c["name"])), ("description", lit(c.get("description"))), ("icon", lit(c.get("icon"))),
            ("color", lit(c.get("color"))), ("is_active", lit(c.get("is_active", True))), ("tenant_id", lit(tenant_id)),
            ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 6. standard changes
    for c in seed["standard_changes"]:
        guard = f"NOT EXISTS (SELECT 1 FROM standard_changes WHERE tenant_id={lit(tenant_id)} AND title={lit(c['title'])})"
        insert("standard_changes", [
            ("title", lit(c["title"])), ("description", lit(c.get("description"))),
            ("implementation_plan", lit(c.get("implementation_plan") or "")), ("rollback_plan", lit(c.get("rollback_plan") or "")),
            ("justification", lit(c.get("justification"))), ("category", lit(c.get("category") or "general")),
            ("risk_level", lit(c.get("risk_level") or "low")), ("impact_scope", lit(c.get("impact_scope") or "low")),
            ("expected_duration", lit(c.get("expected_duration") or 30)), ("approval_required", lit(c.get("approval_required", False))),
            ("affected_cis", json_lit(c.get("affected_cis") or [])), ("prerequisites", json_lit(c.get("prerequisites") or [])),
            ("remarks", lit(c.get("remarks"))), ("created_by", lit(created_by)), ("tenant_id", lit(tenant_id)),
            ("is_active", "true"), ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 7. known errors (placeholder, excluded from G-B acceptance evidence)
    for k in seed["known_errors"]:
        guard = f"NOT EXISTS (SELECT 1 FROM known_errors WHERE tenant_id={lit(tenant_id)} AND title={lit(k['title'])})"
        insert("known_errors", [
            ("title", lit(k["title"])), ("description", lit(k.get("description"))), ("symptoms", lit(k.get("symptoms"))),
            ("root_cause", lit(k.get("root_cause"))), ("workaround", lit(k.get("workaround"))), ("resolution", lit(k.get("resolution"))),
            ("status", lit(k.get("status") or "active")), ("category", lit(k.get("category"))),
            ("severity", lit(k.get("severity") or "medium")), ("affected_products", json_lit(k.get("affected_products") or [])),
            ("affected_cis", json_lit(k.get("affected_cis") or [])), ("keywords", json_lit(k.get("keywords") or [])),
            ("created_by", lit(created_by)), ("tenant_id", lit(tenant_id)), ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 8. ticket tags
    for g in seed["ticket_tags"]:
        guard = f"NOT EXISTS (SELECT 1 FROM ticket_tags WHERE tenant_id={lit(tenant_id)} AND name={lit(g['name'])})"
        insert("ticket_tags", [
            ("name", lit(g["name"])), ("color", lit(g.get("color") or "#1890ff")), ("description", lit(g.get("description"))),
            ("is_active", "true"), ("tenant_id", lit(tenant_id)), ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    # 9. ticket views
    for v in seed["ticket_views"]:
        guard = f"NOT EXISTS (SELECT 1 FROM ticket_views WHERE tenant_id={lit(tenant_id)} AND name={lit(v['name'])})"
        insert("ticket_views", [
            ("name", lit(v["name"])), ("description", lit(v.get("description"))), ("columns", json_lit(v.get("columns") or [])),
            ("is_shared", lit(v.get("is_shared", False))), ("created_by", lit(created_by)), ("tenant_id", lit(tenant_id)),
            ("created_at", "now()"), ("updated_at", "now()"),
        ], guard)

    out += ["", "COMMIT;", ""]
    return "\n".join(out)


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent B0 seed-admission SQL")
    p.add_argument("--seed", required=True)
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--created-by", type=int, default=1)
    p.add_argument("--out", required=True)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    seed = json.loads(Path(args.seed).read_text(encoding="utf-8"))
    sql = build_sql(seed, args.tenant_id, args.created_by)
    Path(args.out).write_text(sql, encoding="utf-8")
    print(f"wrote {args.out} ({len(sql.splitlines())} lines)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
