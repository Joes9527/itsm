# SSLVPN WS4 Cross-System Controlled Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Plan Status:** Review revisions pending approval; do not execute yet.

**Goal:** Produce reproducible production-equivalent evidence that one approved SSLVPN request crosses ITSM Outbox, two Workers, KAF receipt/step/completion, and BPMN exactly once without an uncontrolled external side effect.

**Architecture:** Use a rehearsal-ID-scoped PostgreSQL-backed environment and the versioned cross-system contract. Preflight proves only deployment topology, immutable identities, readiness, and sandbox coverage. ITSM and KAF each produce a redacted evidence bundle through their own read-only auditor principal; a credential-free cross-system orchestrator validates the shared identities and counts. A separate terminal gate prevents real LDAP/Graph execution without renewed user approval.

**Tech Stack:** Docker Compose, Bash, curl/jq, Go E2E tests, pytest/httpx, PostgreSQL, KAF sandbox fixtures

**Spec:** `docs/superpowers/specs/2026-09-04-sslvpn-delegation-reliability-hardening-design.md`

## Global Constraints

- WS1, WS2a, WS2b, and WS3 evidence must all pass before this plan starts.
- The default and required rehearsal is side-effect-free; every mutating Tool/connector action must be fixture-backed and audited as simulated.
- Use only existing controlled users, catalog items, approval actors, and SSLVPN fixtures; do not invent production identities.
- Never print secrets, tokens, raw Outbox payloads, Procedure inputs, LDAP DNs, user email addresses, or raw external error bodies.
- The API process must have zero KAF dispatchers; exactly two Worker replicas are required for concurrency evidence.
- ITSM tests and product code never read KAF persistence, and KAF tests and product code never read ITSM persistence.
- Evidence is finalized and checksummed in persistent storage before exact rehearsal resources are removed.
- Real Graph/LDAP execution is a separately approved operation with named change and recovery owners; this plan must stop before it by default.
- Report status remains `Conditional No-Go` until every mandatory acceptance criterion has current evidence.

---

## File Structure

- Create `[ITSM] scripts/sslvpn-delegation-preflight.sh`: read-only topology/config/readiness gate.
- Create `[ITSM] scripts/sslvpn-delegation-cleanup.sh`: exact rehearsal-inventory cleanup with dry-run.
- Create `[ITSM] itsm-backend/cmd/sslvpn_evidence/main.go`: ITSM-only read-only evidence exporter.
- Create `[ITSM] validation/sslvpn_delegation_orchestrator.py`: credential-free bundle validator.
- Create `[ITSM] itsm-backend/tests/e2e/sslvpn_preflight_script_test.go`, `sslvpn_cleanup_script_test.go`, and `sslvpn_kaf_runtime_e2e_test.go`.
- Create `[KAF] scripts/validation/export_kaf_delegation_evidence.py`: KAF-only read-only evidence exporter.
- Create `[KAF] tests/test_kaf_delegation_fault_matrix.py` and `tests/test_kaf_delegation_evidence.py`.
- Modify `docs/e2e-testing-guide.md` and KAF cutover runbook: exact rehearsal commands and stop conditions.
- Modify the production-readiness report only after evidence exists.

Paths tagged `[ITSM]` or `[KAF]` are relative to that repository root. Commands use caller-supplied `ITSM_REPO` and `KAF_REPO`; no workstation or CI-runner checkout path is part of the plan.

### Task 1: Build a fail-closed read-only preflight

**Files:**
- Create: `scripts/sslvpn-delegation-preflight.sh`
- Create: `itsm-backend/tests/e2e/sslvpn_preflight_script_test.go`
- Modify: `docs/e2e-testing-guide.md`

**Interfaces:**
- Consumes environment variables `ITSM_BASE_URL`, `KAF_BASE_URL`, `REHEARSAL_ID`, Compose project/file paths, and HTTP credentials supplied through secret files.
- Produces exit 0 only when all mandatory gates pass; emits redacted JSON with booleans/counts/release IDs/checksums.

- [ ] **Step 1: Write failing script tests**

Cover missing commands/env, invalid rehearsal ID, health 200 with readiness false, one Worker, host-published Worker port, stale scheduler heartbeat, contract checksum mismatch, KAF delegation disabled, stale Alembic head, and missing sandbox fixture coverage.

- [ ] **Step 2: Add the safety header and guards**

The script begins with:

```bash
set -euo pipefail
: "${ITSM_BASE_URL:?ITSM_BASE_URL is required}"
: "${KAF_BASE_URL:?KAF_BASE_URL is required}"
case "$ITSM_BASE_URL $KAF_BASE_URL" in
  *Authorization*|*token=*|*secret=*) exit 64 ;;
esac
```

It accepts only GET/read-only SQL and `docker compose ps/config`; it contains no POST, UPDATE, INSERT, DELETE, approval, Tool, or migration command.

- [ ] **Step 3: Implement exact gates**

Parse and validate:

```text
ITSM /health and /api/v1/readyz body
KAF /health and /readyz body
schema_state release/checksum
KAF Alembic head
two itsm-worker replicas
Worker internal /healthz, /readyz, /metrics reachable only from Compose network
dispatcher lifecycle=running and heartbeat fresh
same contract artifact checksum on both systems
sandbox enabled and every mutating registered capability has a fixture
```

The preflight does not search logs or infer API dispatcher ownership from Compose. WS2’s architecture test proves API bootstrap cannot construct the dispatcher; runtime ownership is proven later from the two Workers’ private metric deltas for the controlled event.

- [ ] **Step 4: Run tests and shell validation**

```bash
cd itsm-backend
go test ./tests/e2e -run '^TestSSLVPNDelegationPreflight' -count=1
cd ..
bash -n scripts/sslvpn-delegation-preflight.sh
scripts/sslvpn-delegation-preflight.sh | jq -e '.ready == true and .workerReplicas == 2'
```

Expected: tests pass and runtime preflight is true before any write.

- [ ] **Step 5: Commit**

```bash
git add scripts/sslvpn-delegation-preflight.sh itsm-backend/tests/e2e/sslvpn_preflight_script_test.go docs/e2e-testing-guide.md
git commit -m "test(sslvpn): add fail-closed delegation preflight"
```

### Task 2: Provision a disposable production-equivalent rehearsal

**Files:**
- Modify: `docker-compose.prod.yml`
- Modify: `[KAF] deploy/prod/backend/docker-compose.yml`
- Create: `scripts/sslvpn-delegation-cleanup.sh`
- Create: `itsm-backend/tests/e2e/sslvpn_cleanup_script_test.go`
- Create: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces a unique `REHEARSAL_ID`, dedicated Compose project, exact resource inventory, one ITSM API, two ITSM Workers, KAF Backend/Gateway, dedicated PostgreSQL databases/volumes, role-mounted secrets, and no host Worker ports.
- Produces cleanup command `scripts/sslvpn-delegation-cleanup.sh --rehearsal-id "$REHEARSAL_ID" --inventory "$EVIDENCE_DIR/resource-inventory.json" --dry-run|--execute`.
- Consumes only fixture/sandbox identities.

- [ ] **Step 1: Generate and validate a unique rehearsal identity**

Generate `REHEARSAL_ID` as `sslvpn-` plus UTC timestamp and eight lowercase hexadecimal random characters. Derive normalized dedicated `ITSM_REHEARSAL_PROJECT` and `KAF_REHEARSAL_PROJECT` names plus exact database/volume names from that ID. Reject Compose default project names, any resource lacking the rehearsal label, duplicate IDs, globs, and shared database/volume names. Require `EVIDENCE_DIR` to be an existing persistent directory outside both disposable Compose projects.

Write `resource-inventory.json` before startup with the exact project, database, network, container, and volume names and owners. The inventory contains no DSNs or credentials.

- [ ] **Step 2: Validate resolved Compose before startup**

```bash
cd "$ITSM_REPO"
docker compose --project-name "$ITSM_REHEARSAL_PROJECT" -f docker-compose.prod.yml config --no-interpolate
cd "$KAF_REPO"
docker compose --project-name "$KAF_REHEARSAL_PROJECT" -f deploy/prod/backend/docker-compose.yml config --no-interpolate
```

Expected: no literal secret value and explicit KAF delegation enablement.

- [ ] **Step 3: Create fresh disposable databases through reviewed bootstrap jobs**

Run WS1 fresh bootstrap for ITSM and `alembic upgrade head` for KAF using migration principals. Do not reuse the developer database with the historical checksum mismatch as fresh-install evidence.

- [ ] **Step 4: Start exact topology**

Start API and KAF, then scale `itsm-worker=2`. Inspect ports and process commands. Run Task 1 preflight and stop if any gate fails.

- [ ] **Step 5: Record immutable deployment identity**

Capture image digests, Git commits, ITSM release manifest checksum, KAF Alembic head, contract checksum, Compose config hashes, and UTC start time. Do not capture environment values.

- [ ] **Step 6: Implement interruption-safe, exact cleanup**

Install a shell trap that records `cleanupRequired: true` and the interruption reason in the persistent evidence bundle; it must not issue broad automatic deletion. Cleanup first verifies that the evidence index and its SHA-256 exist, then resolves every target from `resource-inventory.json`, verifies the exact rehearsal label/ID, and prints a dry-run. `--execute` removes only those enumerated containers, networks, and volumes and drops only the two named databases using their respective migration/DB-owner principals.

The cleanup script must reject missing or mismatched IDs, shared/default project names, inventory entries outside the rehearsal namespace, wildcard targets, and execution by a runtime principal. It must never call `docker compose down -v`, wildcard volume removal, or database-wide cleanup. Hermetic Go tests cover success, failure, interrupt marker, evidence-not-finalized, owner mismatch, ID mismatch, and malicious inventory cases.

- [ ] **Step 7: Commit runbook/config clarification**

```bash
git add docker-compose.prod.yml scripts/sslvpn-delegation-cleanup.sh itsm-backend/tests/e2e/sslvpn_cleanup_script_test.go docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "docs(sslvpn): define production-equivalent rehearsal"
```

Commit any KAF Compose clarification separately in the KAF repository.

### Task 3: Add the controlled SSLVPN lifecycle E2E

**Files:**
- Create: `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`
- Modify: `itsm-backend/tests/e2e/sslvpn_scenario_test.go`
- Create: `itsm-backend/cmd/sslvpn_evidence/main.go`
- Create: `itsm-backend/cmd/sslvpn_evidence/main_test.go`
- Create: `scripts/validation/capture_worker_metrics.sh`
- Create: `itsm-backend/tests/e2e/sslvpn_worker_metrics_script_test.go`

**Interfaces:**
- Consumes existing controlled catalog/requester/approver fixture IDs from test setup.
- Consumes `ITSM_EVIDENCE_DB_URL_FILE` only in the exporter, using a tenant-scoped read-only ITSM auditor principal.
- Consumes before/after metric snapshots captured through `docker compose exec` from each exact Worker container; Worker ports remain unpublished.
- Produces `eventId`, `taskId`, `correlationId`, ITSM Outbox count, authorized completion receipt/action counts, and BPMN advance count.
- Does not contain a KAF database driver, KAF DSN, or KAF persistence query.

- [ ] **Step 1: Write the failing end-to-end test**

The test creates one Service Request through the public API, performs both existing approval actions through authenticated APIs, waits with a bounded deadline, and asserts:

```go
require.Equal(t, 1, evidence.OutboxCount)
require.Equal(t, 1, evidence.CompletionReceiptCount)
require.Equal(t, 1, evidence.CompletionActionCount)
require.Equal(t, 1, evidence.BPMNAdvanceCount)
```

These are ITSM-owned facts: the signed completion endpoint’s accepted receipt/action ledger and the resulting BPMN state. Every lookup is tenant-scoped. Test failure output contains identifiers and states but no payload/error/lease/identity content. KAF receipt/effect/completion-delivery counts are deliberately absent from this test.

- [ ] **Step 2: Verify test refuses unsafe environments**

Run once with sandbox disabled or missing fixtures.

Expected: test skips/fails before request creation with `sandbox_precondition_failed`; it must not attempt to compensate after an unauthorized write.

- [ ] **Step 3: Implement the ITSM-only evidence exporter**

The Go exporter queries only ITSM with its read-only auditor principal and emits a versioned JSON bundle containing rehearsal ID, build/image identity, contract/release checksums, allowed correlation IDs, Outbox count/status, completion receipt/action counts, and BPMN advance count. It hashes fixture actor identities and never selects Outbox payload, last error, lease owner, Tool input, or raw result. Tests assert the SQL/query layer cannot address a KAF data source and that permission/tenant failures fail closed.

- [ ] **Step 4: Run the happy path**

```bash
cd "$ITSM_REPO"
scripts/validation/capture_worker_metrics.sh --phase before --project "$ITSM_REHEARSAL_PROJECT" --output "$EVIDENCE_DIR/worker-metrics-before.json"
cd itsm-backend
go test ./tests/e2e -run '^TestSSLVPNKAFRuntimeE2E$' -count=1 -v
cd ..
scripts/validation/capture_worker_metrics.sh --phase after --project "$ITSM_REHEARSAL_PROJECT" --output "$EVIDENCE_DIR/worker-metrics-after.json"
cd itsm-backend
go run ./cmd/sslvpn_evidence --rehearsal-id "$REHEARSAL_ID" --worker-metrics-before "$EVIDENCE_DIR/worker-metrics-before.json" --worker-metrics-after "$EVIDENCE_DIR/worker-metrics-after.json" --output "$EVIDENCE_DIR/itsm-evidence.json"
jq -e '.outboxCount == 1 and .completionReceiptCount == 1 and .completionActionCount == 1 and .bpmnAdvanceCount == 1' "$EVIDENCE_DIR/itsm-evidence.json"
```

The capture script resolves exactly two container IDs from the named rehearsal project, executes read-only metric GETs inside those containers, and emits only worker instance ID plus `claim_total`/`attempt_total`. It rejects any other replica count, project-label mismatch, counter reset, or unexpected metric.

- [ ] **Step 5: Commit**

```bash
cd "$ITSM_REPO"
git add itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go itsm-backend/tests/e2e/sslvpn_scenario_test.go itsm-backend/cmd/sslvpn_evidence/main.go itsm-backend/cmd/sslvpn_evidence/main_test.go scripts/validation/capture_worker_metrics.sh itsm-backend/tests/e2e/sslvpn_worker_metrics_script_test.go
git commit -m "test(sslvpn): verify delegated runtime exactly once"
```

### Task 4: Prove replay and cross-phase failure behavior

**Files:**
- Create: `[KAF] tests/test_kaf_delegation_fault_matrix.py`
- Modify: `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`

**Interfaces:**
- Consumes the same stable event/task/correlation/step/completion identities from Task 3.
- Produces a fault matrix with expected ITSM Outbox, KAF execution, KAF completion, Tool-call, and BPMN counts.

- [ ] **Step 1: Encode the failure matrix**

```text
KAF explicit 400/401/403/409/422 -> ITSM blocked
KAF 429/500 -> bounded ITSM retry then retry/dead-letter
request sent, response timeout/read interruption/malformed 202 -> delivery_unknown, no requeue
KAF process death before effect -> recovery follows step metadata
death after effect commit -> completion only, Tool count unchanged
completion timeout -> completion unknown, same payload/key replay
completion already_applied -> confirmed, BPMN count unchanged
lease expiry/theft -> stale owner cannot effect/finalize
permanent Procedure/Tool error -> manual intervention
retry budget exhausted -> dead-letter
```

- [ ] **Step 2: Write deterministic KAF fault tests**

Use injected clocks, repositories, HTTP transports, and fixture adapters. Do not use timing sleeps as correctness assertions.

- [ ] **Step 3: Extend runtime replay checks**

Replay the signed webhook, trigger recovery after effect confirmation, and replay completion. Assert one external simulated effect and one BPMN advancement after each operation.

- [ ] **Step 4: Run both suites**

```bash
cd "$KAF_REPO"
uv run pytest tests/test_kaf_delegation_fault_matrix.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_step_fencing.py -q
cd "$ITSM_REPO/itsm-backend"
go test ./tests/e2e -run '^TestSSLVPNKAFRuntime(E2E|Replay|Faults)$' -count=1 -v
```

Expected: all rows match the matrix; no Tool-call or BPMN count exceeds one.

- [ ] **Step 5: Commit in each repository**

KAF commit:

```bash
git add tests/test_kaf_delegation_fault_matrix.py
git commit -m "test(delegation): cover cross-phase recovery faults"
```

ITSM commit:

```bash
git add itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go
git commit -m "test(sslvpn): prove replay idempotency"
```

### Task 5: Export KAF evidence and correlate without cross-database access

**Files:**
- Create: `[KAF] scripts/validation/export_kaf_delegation_evidence.py`
- Create: `[KAF] tests/test_kaf_delegation_evidence.py`
- Create: `validation/sslvpn_delegation_orchestrator.py`
- Create: `validation/test_sslvpn_delegation_orchestrator.py`
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- KAF exporter consumes `KAF_EVIDENCE_DB_URL_FILE` using a KAF-local read-only auditor principal and emits only approved KAF facts.
- Orchestrator consumes paths to finalized ITSM and KAF JSON bundles and produces `cross-system-evidence.json`; it accepts no DSN, token, database driver, or network URL.
- Neither exporter can query the other product’s persistence.

- [ ] **Step 1: Write failing exporter and orchestrator tests**

KAF tests cover tenant/correlation scoping, read-only permission failure, duplicate receipt/effect/completion detection, redaction, and prohibited-field rejection. Orchestrator tests cover schema version mismatch, rehearsal/event/task/correlation mismatch, non-one counts, manifest/build mismatch, unexpected fields, tampered bundle hashes, and any credential-shaped input.

- [ ] **Step 2: Implement the KAF-owned evidence exporter**

Emit a versioned bundle containing rehearsal ID, KAF build/image identity, contract checksum, allowed `eventId`/`taskId`/`correlationId`, receipt count, durable step-effect count, completion-pending/unknown/confirmed counts, simulated-effect audit count, and redacted terminal states. Never emit payloads, Tool inputs/results, raw errors, leases, DNs, email addresses, or secrets. KAF’s own integration/fault suite remains the authority for phase-transition semantics.

- [ ] **Step 3: Implement the credential-free cross-system orchestrator**

Validate both JSON schemas and bundle SHA-256 values, then require the same rehearsal ID and correlation identities. Assert exactly one ITSM Outbox event, one KAF receipt, one durable simulated step effect, one KAF completion confirmation, one ITSM authorized completion action, and one ITSM BPMN advance. Produce a redacted merged decision record plus hashes of both source bundles. The orchestrator must not import a PostgreSQL client or make HTTP requests.

- [ ] **Step 4: Validate Worker-owned runtime evidence**

Validate the Task 3 before/after snapshots from each of the two Workers. Require the aggregate `claim_total` delta and `attempt_total` delta to equal one, and retain per-worker deltas in the ITSM bundle. Combine this with WS2’s bootstrap ownership test; do not inspect API logs as proof of absence.

- [ ] **Step 5: Run and commit in each repository**

```bash
cd "$KAF_REPO"
uv run pytest tests/test_kaf_delegation_evidence.py tests/test_kaf_delegation_fault_matrix.py -q
uv run python scripts/validation/export_kaf_delegation_evidence.py --rehearsal-id "$REHEARSAL_ID" --output "$EVIDENCE_DIR/kaf-evidence.json"
git add scripts/validation/export_kaf_delegation_evidence.py tests/test_kaf_delegation_evidence.py
git commit -m "test(delegation): export redacted KAF evidence"

cd "$ITSM_REPO"
python3 -m unittest validation/test_sslvpn_delegation_orchestrator.py
python3 validation/sslvpn_delegation_orchestrator.py --itsm "$EVIDENCE_DIR/itsm-evidence.json" --kaf "$EVIDENCE_DIR/kaf-evidence.json" --output "$EVIDENCE_DIR/cross-system-evidence.json"
git add validation/sslvpn_delegation_orchestrator.py validation/test_sslvpn_delegation_orchestrator.py docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "test(sslvpn): correlate isolated evidence bundles"
```

### Task 6: Audit tenant, RBAC, masking, and topology boundaries

**Files:**
- Modify: `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`
- Modify: `[KAF] tests/test_kaf_delegation_fault_matrix.py`
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces negative evidence for cross-tenant access, missing delegated permissions, forged identities, secret leakage, and host-exposed Worker ports.

- [ ] **Step 1: Add negative API assertions**

Non-sysadmin roles receive authorization denial for delegated execution view/reconcile/requeue. A sysadmin from another tenant receives not-found/empty results. Forged tenant/task/correlation/version values fail closed in KAF and completion APIs.

- [ ] **Step 2: Add masking assertions**

Scan readiness, metrics, API bodies, default logs, and report attachments for known canary secrets and fixture PII. Assert payloads, raw errors, leases, tokens, signatures, DN, and emails are absent.

- [ ] **Step 3: Verify topology**

Inspect Compose port bindings and container network access. Worker endpoints must be reachable from a peer container and absent from host published ports. Cite WS2’s API-bootstrap architecture test and Task 5 Worker metric deltas; do not search API logs or configuration for runtime-construction proof.

- [ ] **Step 4: Run security-focused checks**

```bash
cd itsm-backend
go test ./tests/e2e ./handlers/delegated_execution ./internal/workerhealth ./router -run 'SSLVPN|Delegated|Tenant|Permission|Mask|Worker' -count=1
cd "$KAF_REPO"
uv run pytest tests/test_kaf_delegation_fault_matrix.py tests/test_kaf_delegation_readiness.py tests/test_sandbox_policy.py -q
```

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "test(sslvpn): verify delegation security boundaries"
```

Commit KAF test changes separately.

### Task 7: Stop at the real-side-effect approval gate

**Files:**
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces a manual approval record requirement; produces no automatic external write command.

- [ ] **Step 1: Verify prerequisites read-only**

Record the existing controlled fixture identifier in the restricted change record, confirm non-membership, identify change owner and recovery owner, verify add/remove commands are available, and set a bounded window.

- [ ] **Step 2: Request renewed explicit approval**

Stop execution and ask the user to approve the named real Graph/LDAP rehearsal. The earlier design/plan approval is not approval for this external mutation.

- [ ] **Step 3: If approval is absent, mark the real rehearsal not executed**

Do not describe it as passed or deployed. Continue the release decision using only mandatory no-side-effect evidence if policy allows; otherwise retain `Conditional No-Go`.

- [ ] **Step 4: If separately approved, execute the reviewed change record only**

Perform one add, one idempotent replay, and one remove through the normal governed workflow. On cleanup failure, stop, keep No-Go, and transfer to the named recovery owner. Confirm final non-membership read-only.

### Task 8: Finalize evidence and clean only rehearsal-owned resources

Task 8 is the mandatory `finally` path after any success, failure, or interruption in Tasks 2–7. A failed gate skips later mutation/testing tasks but still finalizes failure evidence and cleans only the inventoried resources.

**Files:**
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`
- Modify: `docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`

**Interfaces:**
- Produces immutable `evidence-index.json` plus SHA-256 after all source bundles, test summaries, image digests, commits, manifest checksums, timestamps, redacted state counts, and cleanup inventory exist.
- Consumes the exact Task 2 rehearsal ID and inventory; removes no unlisted resource.

- [ ] **Step 1: Finalize the persistent evidence bundle on success or failure**

Write test exit states and failure/interruption reason, redact it, enumerate every pre-cleanup evidence artifact and SHA-256 in `evidence-index.json`, then write `evidence-index.sha256` for that index. Mark `evidenceFinalizedAt` before cleanup. An unsuccessful rehearsal remains unsuccessful; finalizing evidence does not turn it into passing evidence.

- [ ] **Step 2: Dry-run exact cleanup**

```bash
cd "$ITSM_REPO"
scripts/sslvpn-delegation-cleanup.sh --rehearsal-id "$REHEARSAL_ID" --inventory "$EVIDENCE_DIR/resource-inventory.json" --dry-run
```

Review that every target carries the exact rehearsal label and appears in the inventory. The command must contain no wildcard, shared project, or unscoped database target.

- [ ] **Step 3: Execute as the authorized owners and verify absence**

After the migration/DB owners authorize their exact database drops, run the same command with `--execute`. Verify every inventoried ephemeral resource is absent and the persistent evidence directory plus its checksums remain readable. Write and checksum a separate `cleanup-result.json` that references the immutable pre-cleanup index hash. Record cleanup result in the report; on interruption or partial cleanup, retain `Conditional No-Go`, keep `cleanupRequired: true`, record the exact recovery command, and assign each residual resource to its named owner.

### Task 9: Post-cleanup verification and evidence-based release decision

**Files:**
- Modify: `docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces the final `Go` or retained `Conditional No-Go` decision with command output, commit/image identity, and explicit backlog exclusions.

- [ ] **Step 1: Run the full ITSM verification**

```bash
cd "$ITSM_REPO/itsm-backend"
go test ./config ./migration ./internal/initialization ./internal/readiness ./internal/bootstrap ./internal/workerhealth ./service ./handlers/delegated_execution ./pkg/seeder ./router ./tests/e2e -count=1
go vet ./config ./migration ./internal/initialization ./internal/readiness ./internal/bootstrap ./internal/workerhealth ./service ./handlers/delegated_execution ./router
cd ..
docker compose -f docker-compose.prod.yml config --no-interpolate
git diff --check
```

- [ ] **Step 2: Run the full KAF verification**

```bash
cd "$KAF_REPO"
uv run pytest tests/test_itsm_webhooks.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_delivery_migration.py tests/test_kaf_delegation_two_phase_migration.py tests/test_kaf_delegation_repository.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_step_fencing.py tests/test_kaf_delegation_readiness.py tests/test_kaf_delegation_fault_matrix.py tests/test_sandbox_policy.py tests/test_vpn_grant_tool.py tests/test_vpn_permission_grant_procedure_doc.py -q
uv run ruff check src/acp tests/test_kaf_delegation_*.py tests/test_sandbox_policy.py
uv run alembic heads
docker compose -f deploy/prod/backend/docker-compose.yml config --no-interpolate
git diff --check
```

- [ ] **Step 3: Revalidate preserved runtime evidence without recreating resources**

```bash
cd "$EVIDENCE_DIR"
sha256sum --check evidence-index.sha256
cd "$ITSM_REPO"
python3 validation/sslvpn_delegation_orchestrator.py --itsm "$EVIDENCE_DIR/itsm-evidence.json" --kaf "$EVIDENCE_DIR/kaf-evidence.json" --output "$EVIDENCE_DIR/cross-system-evidence.recheck.json"
jq -e '.decision == "pass" and .counts.outbox == 1 and .counts.kafReceipt == 1 and .counts.stepEffect == 1 and .counts.completionAction == 1 and .counts.bpmnAdvance == 1' "$EVIDENCE_DIR/cross-system-evidence.recheck.json"
```

Expected: preserved evidence proves all mandatory gates were true before cleanup: two Workers with aggregate claim/attempt delta one, WS2 API-bootstrap ownership proof passing, exact contract/release identities, one simulated effect, one completion action, and one BPMN advance.

- [ ] **Step 4: Update the report precisely**

Change to `Go` only if every mandatory design criterion has current evidence. Explicitly list email alert delivery/recipients, Langfuse governance, TLS/mTLS, CIDR policy, and any unexecuted real external rehearsal as backlog or not executed; never present them as delivered.

- [ ] **Step 5: Commit the final evidence**

```bash
git add docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "docs: record SSLVPN delegation release decision"
```
