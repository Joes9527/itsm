#!/usr/bin/env python3
"""Generate idempotent SQL that initializes the canonical BPMN process config.

Companion to generate_seed_sql.py for the B0 follow-up "canonical process
initialization" batch (see docs/migrations/2026-09-14-b0-seed-admission-dry-run.md
and docs/review/2026-09-14-b0-seed-admission-evidence.md).

It mirrors the application's ``BPMNTemplateService.deployTemplate``: every
embedded ``service/bpmn/*.bpmn`` becomes one ``process_deployments`` row
(``<key>-v1``) and one ``process_definitions`` row (version 1.0.0, is_latest,
is_active) whose ``bpmn_xml`` jsonb holds the base64 of the exact file bytes
(the column is ``field.JSON([]byte)``). Then the seed's ``process_bindings`` are
inserted. No legacy BPMN is imported.
"""

from __future__ import annotations

import argparse
import base64
import json
from pathlib import Path

from generate_seed_sql import json_lit, lit  # same package directory

# Mirrors the switch in service/bpmn_template_service.go listTemplates().
TEMPLATE_META: dict[str, tuple[str, str, str]] = {
    "ticket_general_flow": ("通用工单流程", "ticket", "通用工单处理流程"),
    "ticket_urgent_flow": ("紧急工单流程", "ticket", "高/紧急优先级工单处理流程（结构与通用工单流程等价，暂无独立超时/升级差异）"),
    "ticket_assignment_flow": ("工单分配流程", "ticket", "工单自动分配处理流程"),
    "change_normal_flow": ("普通变更流程", "change", "普通变更管理流程"),
    "change_emergency_flow": ("紧急变更流程", "change", "紧急变更快速处理流程"),
    "incident_emergency_flow": ("紧急事件流程", "incident", "紧急事件快速响应流程"),
    "service_request_flow": ("服务请求流程", "service_request", "标准服务请求处理流程"),
    "service_request_urgent_flow": ("紧急服务请求流程", "service_request", "高优先级服务请求处理流程（结构与标准服务请求流程等价，暂无独立超时/升级差异）"),
    "problem_management_flow": ("问题管理流程", "problem", "问题管理全流程"),
    "release_approval_flow": ("发布审批流程", "release", "软件发布审批管理流程"),
    "sslvpn_approval_flow": ("SSL-VPN 申请与双级审批流", "service_request", "SSL-VPN 远程办公访问权限申请与双级审批流"),
}


def template_meta(key: str) -> tuple[str, str, str]:
    return TEMPLATE_META.get(key, (key, "default", ""))


def build_sql(bpmn_dir: Path, bindings: list[dict], tenant_id: int) -> str:
    out: list[str] = ["-- Canonical process initialization (idempotent).", "BEGIN;", ""]
    files = sorted(bpmn_dir.glob("*.bpmn"))
    if not files:
        raise SystemExit(f"no .bpmn files under {bpmn_dir}")

    for path in files:
        key = path.stem
        name, category, description = template_meta(key)
        data = path.read_bytes()
        b64 = base64.b64encode(data).decode("ascii")
        deployment_id = f"{key}-v1"
        out.append(
            "INSERT INTO process_deployments (deployment_id, deployment_name, deployment_time, "
            "deployed_by, is_active, deployment_category, tenant_id, created_at, updated_at) "
            f"SELECT {lit(deployment_id)}, {lit(name + ' v1')}, now(), 'system', true, 'default', "
            f"{lit(tenant_id)}, now(), now() "
            f"WHERE NOT EXISTS (SELECT 1 FROM process_deployments WHERE deployment_id={lit(deployment_id)});"
        )
        out.append(
            "INSERT INTO process_definitions (key, name, description, version, category, bpmn_xml, "
            "is_active, is_latest, deployed_at, tenant_id, created_at, updated_at, deployment_id) "
            f"SELECT {lit(key)}, {lit(name)}, {lit(description)}, '1.0.0', {lit(category)}, "
            f"to_jsonb({lit(b64)}::text), true, true, now(), {lit(tenant_id)}, now(), now(), "
            f"(SELECT id FROM process_deployments WHERE deployment_id={lit(deployment_id)}) "
            f"WHERE NOT EXISTS (SELECT 1 FROM process_definitions WHERE key={lit(key)} AND tenant_id={lit(tenant_id)});"
        )
    out.append("")

    for b in bindings:
        sub = b.get("business_sub_type")
        guard = (
            f"NOT EXISTS (SELECT 1 FROM process_bindings WHERE tenant_id={lit(tenant_id)} "
            f"AND business_type={lit(b['business_type'])} "
            f"AND business_sub_type IS NOT DISTINCT FROM {lit(sub)} "
            f"AND process_definition_key={lit(b['process_definition_key'])})"
        )
        cols = [
            ("business_type", lit(b["business_type"])), ("business_sub_type", lit(sub)),
            ("process_definition_key", lit(b["process_definition_key"])),
            ("process_version", lit(b.get("process_version", 1))), ("is_default", lit(b.get("is_default", False))),
            ("priority", lit(b.get("priority", 0))), ("is_active", lit(b.get("is_active", True))),
            ("department_id", "0"), ("team_id", "0"), ("scenario", "''"), ("category", "''"),
            ("approval_chain_id", "''"), ("sla_policy_id", "''"), ("tenant_id", lit(tenant_id)),
            ("created_at", "now()"), ("updated_at", "now()"),
        ]
        names = ", ".join(c for c, _ in cols)
        values = ", ".join(v for _, v in cols)
        out.append(f"INSERT INTO process_bindings ({names}) SELECT {values} WHERE {guard};")

    out += ["", "COMMIT;", ""]
    return "\n".join(out)


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Generate idempotent canonical process-init SQL")
    p.add_argument("--bpmn-dir", required=True)
    p.add_argument("--seed", required=True, help="fixed seed JSON providing process_bindings")
    p.add_argument("--tenant-id", type=int, default=1)
    p.add_argument("--out", required=True)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    seed = json.loads(Path(args.seed).read_text(encoding="utf-8"))
    sql = build_sql(Path(args.bpmn_dir), seed.get("process_bindings", []), args.tenant_id)
    Path(args.out).write_text(sql, encoding="utf-8")
    print(f"wrote {args.out} ({len(sql.splitlines())} lines)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
