# SSLVPN WS3 KAF Delegation Recovery and Fencing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Plan Status:** Review revisions pending approval; do not execute yet.

**Goal:** Give KAF bounded, durable and independently recoverable execution/completion phases, stable step-effect fencing, truthful readiness, and a fail-closed sandbox for every mutating capability.

**Architecture:** Split the oversized delegation pipeline into orchestration plus a PostgreSQL state repository. Execution and ITSM completion use separate statuses, leases and retry budgets; once a Tool effect is durable, recovery can replay only the stable completion payload. All workflow Tool/connector calls pass through one metadata-driven sandbox boundary, and delegated step keys bind tenant/task/correlation/procedure/version/step.

**Tech Stack:** Python 3.12, FastAPI, Pydantic 2, SQLAlchemy async, PostgreSQL, Alembic, pytest/pytest-asyncio, httpx, Docker Compose

**Spec:** `[ITSM] docs/superpowers/specs/2026-09-04-sslvpn-delegation-reliability-hardening-design.md`

## Global Constraints

- WS1 and both WS2 release gates must have passed before WS3 runtime changes are deployed.
- Follow KAF `AGENTS.md` and `docs/kaf2/AGENTS-REFERENCE.md`.
- KAF never reads or writes the ITSM database; it uses only typed task-scoped APIs.
- `ITSM_KAF_DELEGATION_ENABLED` is the only feature switch; partial configuration fails startup.
- Procedure documents remain the execution source of truth; Python must not invent a second workflow.
- Unknown Tool, missing `read_only`, missing recovery metadata, unknown state, or missing sandbox fixture fails closed.
- After `effect_confirmed`, no recovery path may re-enter Procedure or Tool execution.
- Completion replays use one persisted payload and one stable idempotency key.
- Email alert delivery and recipients remain backlog evidence and are not a WS3 acceptance claim.
- Do not keep the old single-status model, aliases, or dual reads after the migration release.

---

## File Structure

- Modify `src/acp/config.py` and production Compose: explicit enablement and bounded retry/lease settings.
- Create `src/acp/services/kaf_delegation_state.py`: typed states, retry decisions, and safe error classes.
- Create `src/acp/repositories/kaf_delegation_delivery.py`: all lease/claim/transition SQL.
- Modify `src/acp/models/kaf_delegation_delivery.py`; add the next Alembic revision after `036_kaf_completion_replay`.
- Refactor `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`: orchestration only.
- Create `src/acp/jobs/kaf_delegation_recovery.py`: execution/completion schedulers and lifecycle.
- Create `src/acp/readiness.py`; modify `src/acp/main.py` and health routing.
- Modify `src/acp/models/workflow_step_ledger.py`, `src/acp/workflows/shared/step_ledger.py`, and `src/acp/workflows/assembler.py`: delegated execution key fencing.
- Create `src/acp/tools/sandbox.py` and fixture assets; modify `src/acp/tools/governance.py` and registry validation.
- Modify VPN Tool/Procedure files to separate notification into a governed action.

### Task 1: Add explicit delegation configuration and truthful readiness

**Files:**
- Modify: `src/acp/config.py`
- Create: `src/acp/readiness.py`
- Create: `tests/test_kaf_delegation_readiness.py`
- Modify: `src/acp/routers/health.py`
- Modify: `src/acp/main.py`
- Modify: `deploy/prod/backend/docker-compose.yml`

**Interfaces:**
- Adds settings: `itsm_kaf_delegation_enabled`, execution/completion max attempts, base/max backoff, recovery interval, and lease seconds.
- Produces: `DelegationLoopState(state, last_heartbeat_at, last_error_class)`.
- Produces: `check_readiness() -> ReadinessResult` and `/readyz` JSON.

- [ ] **Step 1: Write failing configuration tests**

Test disabled mode, complete enabled mode, missing URL/secret/token, non-positive limits, backoff max below base, and lease not exceeding the longest allowed Tool timeout.

```python
with pytest.raises(ValidationError, match="ITSM_KAF_AUTOMATION_TOKEN"):
    Settings(ITSM_KAF_DELEGATION_ENABLED=True, ITSM_KAF_URL="https://itsm.test")
```

- [ ] **Step 2: Write failing readiness tests**

Assert 503 for stale Alembic head, inaccessible delivery/step tables, enabled-but-invalid config, and stopped/stale recovery loop. Assert 200 when disabled with schema healthy and when enabled with a live loop. Patch external clients to prove readiness never calls ITSM, LDAP, or Graph.

- [ ] **Step 3: Implement one enablement gate**

Replace `if settings.itsm_kaf_url and settings.itsm_kaf_automation_token` with `settings.itsm_kaf_delegation_enabled`. The Pydantic model validator requires the complete configuration atomically. Disabled webhook returns 503 and no recovery task starts.

- [ ] **Step 4: Implement readiness**

Query PostgreSQL and Alembic head, perform bounded SELECT probes on delegation and step-action tables, and inspect the recovery lifecycle/heartbeat. Keep `/health` as liveness and expose `/readyz` separately.

- [ ] **Step 5: Run and commit**

```bash
uv run pytest tests/test_kaf_delegation_readiness.py tests/test_health_503.py tests/test_runtime_governance_config.py -q
uv run ruff check src/acp/config.py src/acp/readiness.py src/acp/routers/health.py src/acp/main.py tests/test_kaf_delegation_readiness.py
git add src/acp/config.py src/acp/readiness.py src/acp/routers/health.py src/acp/main.py tests/test_kaf_delegation_readiness.py deploy/prod/backend/docker-compose.yml
git commit -m "feat(delegation): require explicit enablement and readiness"
```

### Task 2: Migrate to independent execution and completion state

**Files:**
- Create: `alembic/versions/037_kaf_delegation_two_phase_state.py`
- Modify: `src/acp/models/kaf_delegation_delivery.py`
- Create: `src/acp/services/kaf_delegation_state.py`
- Create: `tests/test_kaf_delegation_two_phase_migration.py`
- Modify: `tests/test_kaf_delegation_delivery_migration.py`

**Interfaces:**
- Produces enums `ExecutionStatus` and `CompletionStatus` with exactly the states approved in the design.
- Adds separate attempts, next-attempt times, lease owners/expiries, terminal times, stable completion idempotency key, and sanitized effect result.
- Removes old `status`, `lease_owner`, `lease_expires_at`, and `remote_applied_at` after backfill in the same revision.

- [ ] **Step 1: Write migration mapping tests**

Insert one row for every old-state/data combination and assert:

```text
completed -> effect_confirmed / confirmed
completion_payload + failed_auth -> effect_confirmed / failed_auth
completion_payload + any other noncompleted known state -> effect_confirmed / unknown
received|running|retryable without payload -> same execution phase / not_ready
failed_auth without payload -> failed_auth / not_ready
unknown old state -> migration failure
```

- [ ] **Step 2: Verify tests fail against revision 036**

Run: `uv run pytest tests/test_kaf_delegation_two_phase_migration.py -q`

Expected: FAIL because split columns/revision are absent.

- [ ] **Step 3: Implement the forward migration**

Add non-null split columns with temporary defaults, perform a deterministic SQL `CASE` backfill, validate zero unmapped rows, create due/lease indexes for both phases, and then drop old columns. Set old retryable rows’ next attempt to the upgrade timestamp.

- [ ] **Step 4: Replace model constants with enums**

```python
class ExecutionStatus(StrEnum):
    RECEIVED = "received"
    RUNNING = "running"
    RETRY_WAIT = "retry_wait"
    EFFECT_CONFIRMED = "effect_confirmed"
    FAILED_AUTH = "failed_auth"
    MANUAL_INTERVENTION = "manual_intervention"
    DEAD_LETTER = "dead_letter"
```

Define the corresponding nine completion states from the design; model defaults are `received` and `not_ready`.

- [ ] **Step 5: Run migration tests and commit**

```bash
uv run pytest tests/test_kaf_delegation_two_phase_migration.py tests/test_kaf_delegation_delivery_migration.py -q
uv run alembic heads
git add alembic/versions/037_kaf_delegation_two_phase_state.py src/acp/models/kaf_delegation_delivery.py src/acp/services/kaf_delegation_state.py tests/test_kaf_delegation_two_phase_migration.py tests/test_kaf_delegation_delivery_migration.py
git commit -m "feat(delegation): split execution and completion state"
```

Expected: one Alembic head and no runtime references to the old status field.

### Task 3: Centralize fenced claims and bounded retry decisions

**Files:**
- Create: `src/acp/repositories/kaf_delegation_delivery.py`
- Create: `tests/test_kaf_delegation_repository.py`
- Modify: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`

**Interfaces:**
- Produces `claim_execution(now: datetime, owner: str, lease_until: datetime, limit: int) -> list[KafDelegationDelivery]` and the symmetric typed renew/finalize methods for execution and completion.
- Produces frozen `RetryPolicy(max_attempts, base_seconds, max_seconds)` and `FailureDisposition`.
- Every finalize call matches delivery ID, expected state, owner, and unexpired lease.
- Removes `KafItsmContextClient.list_kaf_delegated_tasks`, `_recovery_event`, legacy delivery adoption, and pull-based task synthesis; missed delivery recovery remains ITSM Outbox’s responsibility.

- [ ] **Step 1: Write failing multi-worker repository tests**

Use PostgreSQL to prove two owners cannot claim the same row, a stale owner cannot renew/finalize, expired leases are reclaimable, attempt counts increment once per claim, exponential backoff is bounded, and retry exhaustion enters dead letter.

- [ ] **Step 2: Add typed error classification tests**

Map network/429/temporary 5xx to retry; 401/403 to failed auth; identity/version/unknown Tool/invalid Procedure to manual intervention; and response uncertainty after callback send to completion unknown. Do not inspect exception message keywords.

- [ ] **Step 3: Implement the repository and classifier**

Use PostgreSQL row selection with `FOR UPDATE SKIP LOCKED` followed by a fenced update, or one atomic update returning claimed rows, ordered by due time and receipt time. Commit claims before calling any external system.

- [ ] **Step 4: Remove state SQL from the pipeline**

Inject the repository into `KafDelegationPipeline`; keep validation and orchestration there, but move every SQLAlchemy claim/transition query into the repository.

Delete pull recovery from the ITSM delegated-task listing. KAF recovery scans only its durable local receipts; it must never create a receipt for an event that the ITSM Worker did not deliver.

- [ ] **Step 5: Run and commit**

```bash
uv run pytest tests/test_kaf_delegation_repository.py tests/test_kaf_delegation_pipeline.py -q
uv run ruff check src/acp/repositories/kaf_delegation_delivery.py src/acp/services/kaf_delegation_state.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py
git add src/acp/repositories src/acp/services/kaf_delegation_state.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_repository.py tests/test_kaf_delegation_pipeline.py
git commit -m "refactor(delegation): centralize fenced delivery state"
```

### Task 4: Add stable delegated step-effect fencing

**Files:**
- Create: `alembic/versions/038_delegated_step_execution_key.py`
- Modify: `src/acp/models/workflow_step_ledger.py`
- Modify: `src/acp/workflows/shared/step_ledger.py`
- Modify: `src/acp/workflows/assembler.py`
- Modify: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Create: `tests/test_kaf_delegation_step_fencing.py`
- Modify: `tests/test_step_ledger.py`

**Interfaces:**
- Produces: `DelegatedExecutionIdentity(tenant_id, task_id, correlation_id, procedure_ref, procedure_version)`.
- Produces: `make_delegated_step_key(identity, step_index, tool_name) -> str` without attempt number.
- Produces: `claim_or_replay_step(identity: DelegatedExecutionIdentity, step_index: int, tool_name: str, recovery: str, input_summary: dict[str, Any]) -> StepClaim`, whose outcomes are `execute`, `replay_success`, `retry_safe`, or `manual`.

- [ ] **Step 1: Write failing replay tests**

Assert identical delegated identity produces one key across process restart; a succeeded row returns stored sanitized result without Tool invocation; an unknown in-flight write follows Procedure recovery metadata; and missing/unknown recovery metadata returns manual intervention.

- [ ] **Step 2: Add durable execution identity**

Migration 038 adds nullable `delegated_execution_key` for legacy nondelegated workflows and a partial unique index over non-null keys. Compute:

```python
raw = ":".join((tenant_id, task_id, correlation_id, procedure_ref, procedure_version, str(step_index), tool_name))
return hashlib.sha256(raw.encode()).hexdigest()
```

- [ ] **Step 3: Thread identity through the Procedure runner**

Add the frozen identity to `KafExecutionContext` and workflow state. The assembler uses delegated fencing only when that identity is present; chat and legacy batch paths retain their existing scope without a compatibility branch for delegation.

- [ ] **Step 4: Make action result durable before downstream progression**

The step transaction persists result status plus sanitized result summary. On replay, `replay_success` returns that result and never calls `call_with_sandbox` or the real Tool.

- [ ] **Step 5: Run and commit**

```bash
uv run pytest tests/test_kaf_delegation_step_fencing.py tests/test_step_ledger.py tests/test_kaf_delegation_pipeline.py -q
uv run alembic heads
git add alembic/versions/038_delegated_step_execution_key.py src/acp/models/workflow_step_ledger.py src/acp/workflows/shared/step_ledger.py src/acp/workflows/assembler.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_step_fencing.py tests/test_step_ledger.py
git commit -m "feat(delegation): fence durable procedure step effects"
```

### Task 5: Persist effect confirmation before independently replaying completion

**Files:**
- Modify: `src/acp/repositories/kaf_delegation_delivery.py`
- Modify: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Create: `src/acp/jobs/kaf_delegation_recovery.py`
- Create: `tests/test_kaf_delegation_completion_recovery.py`
- Modify: `src/acp/main.py`

**Interfaces:**
- Produces: `confirm_effect_and_queue_completion(delivery_id, execution_owner, final_step_ledger_id, result, payload, idempotency_key)` as one DB transaction.
- Produces: independent `recover_execution_once()` and `recover_completion_once()`.

- [ ] **Step 1: Write crash-window tests**

Inject crashes before effect commit, after effect commit/before callback, after callback send/before response, and after `already_applied`. Assert Tool call counts are respectively governed by recovery metadata, then zero for all post-effect crash windows.

- [ ] **Step 2: Persist one stable completion request**

In one transaction finalize the identified step-ledger result and write sanitized effect result, completion payload, stable idempotency key, execution `effect_confirmed`, and completion `pending`. Sending the callback before this commit is forbidden.

- [ ] **Step 3: Implement the independent completion worker**

Claim only pending, unknown, due retry-wait, or expired in-flight completion rows. Send the persisted payload unchanged. `applied` and `already_applied` become confirmed; network/read ambiguity becomes unknown; task/version/tenant/correlation mismatch becomes manual intervention.

- [ ] **Step 4: Separate lifecycle and heartbeat**

The recovery job owns execution and completion scheduler tasks, records heartbeat only after successful DB sweeps, and stops both deterministically in FastAPI lifespan. Webhook acceptance only commits the receipt; it does not call untracked `asyncio.create_task`. Remove unreferenced `_recovery_task` state from the router-global pipeline.

- [ ] **Step 5: Run and commit**

```bash
uv run pytest tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_repository.py -q
uv run ruff check src/acp/jobs/kaf_delegation_recovery.py src/acp/repositories/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py src/acp/main.py
git add src/acp/jobs/kaf_delegation_recovery.py src/acp/repositories/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py src/acp/main.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_pipeline.py
git commit -m "feat(delegation): recover completion independently"
```

### Task 6: Replace op-type sandboxing with a universal fixture policy

**Files:**
- Create: `src/acp/tools/sandbox.py`
- Create: `src/acp/tools/sandbox_fixtures.yaml`
- Modify: `src/acp/tools/metadata.py`
- Modify: `src/acp/tools/governance.py`
- Modify: `src/acp/procedures/manifest.py`
- Create: `tests/test_sandbox_policy.py`
- Modify: `tests/test_recovery_sandbox.py`
- Modify: `tests/test_tool_registry.py`

**Interfaces:**
- Produces: `SandboxPolicy.execute(meta, tool_name, kwargs) -> SandboxDecision`.
- Extends `ToolMetadata` with explicit `read_only: bool` and `sandbox_fixture: str | None`; write capabilities require a fixture.
- Produces low-sensitive audit event `sandbox_effect_simulated` with capability, execution key, fixture version, and result code only.

- [ ] **Step 1: Write the failing all-capability matrix**

Cover Graph, AD write, LDAP, ITSM mutation, email, CLI, internal mutations, and a synthetic future connector. Assert every `read_only=false` capability avoids the real callable; missing metadata/fixture and unknown Tool fail closed. Assert no user, email, DN, payload, URL, or secret enters the audit.

- [ ] **Step 2: Remove `_SANDBOX_MOCKED` and generic `_mock_response`**

Load versioned, schema-validated fixtures by capability. Fixture outputs must match the Tool’s declared success contract. CLI and unsupported actions fail closed rather than return simulated success.

- [ ] **Step 3: Fix the current missing-metadata fail-open**

Replace this behavior in `call_with_sandbox`:

```python
if meta is None:
    return await fn(**kwargs)
```

with a typed `UNKNOWN_TOOL_METADATA` failure before any callable invocation.

- [ ] **Step 4: Route both execution paths through one policy**

`GovernedTool._arun` and `call_with_sandbox` call the same `SandboxPolicy`. Read-only calls require explicit metadata and allowlist; sandbox defaults them to fixtures unless the policy explicitly allows a controlled test dependency.

- [ ] **Step 5: Run and commit**

```bash
uv run pytest tests/test_sandbox_policy.py tests/test_recovery_sandbox.py tests/test_tool_registry.py tests/test_external_action_recovery.py -q
uv run ruff check src/acp/tools/sandbox.py src/acp/tools/metadata.py src/acp/tools/governance.py src/acp/procedures/manifest.py
git add src/acp/tools/sandbox.py src/acp/tools/sandbox_fixtures.yaml src/acp/tools/metadata.py src/acp/tools/governance.py src/acp/procedures/manifest.py tests/test_sandbox_policy.py tests/test_recovery_sandbox.py tests/test_tool_registry.py
git commit -m "fix(governance): sandbox every mutating capability"
```

### Task 7: Separate SSLVPN notification from LDAP mutation

**Files:**
- Modify: `src/acp/mcp/tools/vpn.py`
- Modify: `src/acp/mcp/server.py`
- Modify: `src/acp/tools/metadata.py`
- Modify: `scripts/procedures/vpn_permission_grant.md`
- Modify: `tests/test_vpn_grant_tool.py`
- Modify: `tests/test_vpn_permission_grant_procedure_doc.py`

**Interfaces:**
- `ldap_grant_vpn_access` performs only member check/add and returns a sanitized effect result.
- Adds governed `notify_vpn_access_granted` action with its own stable step key and sandbox fixture.

- [ ] **Step 1: Write failing call-separation tests**

Assert LDAP success never directly calls mail/notification code, and the Procedure contains a subsequent explicit notification step. Replay the LDAP step and notification step separately and assert one call each.

- [ ] **Step 2: Extract the notification action**

Register the new action in MCP server and metadata with `read_only=false`, explicit recovery policy, audit requirement, and fixture. Update Procedure YAML steps; do not create a Python fallback Procedure.

- [ ] **Step 3: Run and commit**

```bash
uv run pytest tests/test_vpn_grant_tool.py tests/test_vpn_permission_grant_procedure_doc.py tests/test_kaf_delegation_step_fencing.py tests/test_sandbox_policy.py -q
uv run ruff check src/acp/mcp/tools/vpn.py src/acp/mcp/server.py src/acp/tools/metadata.py
git add src/acp/mcp/tools/vpn.py src/acp/mcp/server.py src/acp/tools/metadata.py scripts/procedures/vpn_permission_grant.md tests/test_vpn_grant_tool.py tests/test_vpn_permission_grant_procedure_doc.py
git commit -m "refactor(vpn): separate grant and notification effects"
```

### Task 8: Add low-cardinality operational evidence and pass WS3 gate

**Files:**
- Create: `src/acp/services/kaf_delegation_observability.py`
- Create: `tests/test_kaf_delegation_observability.py`
- Modify: `src/acp/routers/health.py`
- Modify: `docs/kaf2/operations/12-prod-cutover-runbook.md`
- Modify: `[ITSM] docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`

**Interfaces:**
- Produces DB-backed aggregate counts/oldest-age by phase/status plus counters for claim/retry/lease-lost/completion-replay.
- Never uses tenant/task/event/correlation as metric labels.

- [ ] **Step 1: Write redaction and cardinality tests**

Assert output contains only phase/status/error-class/count/age fields and excludes identifiers, payload, LDAP DN, emails, errors, lease owners, and credentials.

- [ ] **Step 2: Implement internal operational metrics**

Expose low-cardinality structured metrics on the private backend boundary and log state transitions with the same bounded fields. Keep effect-confirmed distinct from completion-confirmed.

- [ ] **Step 3: Run the full KAF gate**

```bash
uv run pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_delivery_migration.py tests/test_kaf_delegation_two_phase_migration.py tests/test_kaf_delegation_repository.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_step_fencing.py tests/test_kaf_delegation_readiness.py tests/test_sandbox_policy.py tests/test_vpn_grant_tool.py tests/test_vpn_permission_grant_procedure_doc.py tests/test_kaf_delegation_observability.py -q
uv run ruff check src/acp tests/test_kaf_delegation_*.py tests/test_sandbox_policy.py
uv run mypy src/acp/contracts/kaf_delegation.py src/acp/models/kaf_delegation_delivery.py src/acp/repositories/kaf_delegation_delivery.py src/acp/services/kaf_delegation_state.py src/acp/jobs/kaf_delegation_recovery.py
uv run alembic heads
docker compose -f deploy/prod/backend/docker-compose.yml config --no-interpolate
```

Expected: one Alembic head and all checks pass.

- [ ] **Step 4: Run no-side-effect fault proofs**

With sandbox enabled, inject temporary errors, permanent errors, callback timeouts, process termination, and lease theft. Prove no post-effect path reruns a Tool, completion replay retains the same key/payload, and every write is simulated/audited.

- [ ] **Step 5: Record evidence and commit**

Update both runbook and ITSM report with KAF commit, Alembic head, readiness body, state counts, fault matrix, and sandbox audit proof. Do not claim email alerts, recipients, Langfuse governance, TLS, CIDR, or real Graph/LDAP execution.

```bash
git add src/acp/services/kaf_delegation_observability.py src/acp/routers/health.py tests/test_kaf_delegation_observability.py docs/kaf2/operations/12-prod-cutover-runbook.md
git commit -m "feat(delegation): expose bounded recovery evidence"
```

Commit the ITSM report separately in its own repository.
