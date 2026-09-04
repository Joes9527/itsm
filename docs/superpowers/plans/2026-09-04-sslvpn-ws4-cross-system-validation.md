# SSLVPN WS4 Cross-System Controlled Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce reproducible production-equivalent evidence that one approved SSLVPN request crosses ITSM Outbox, two Workers, KAF receipt/step/completion, and BPMN exactly once without an uncontrolled external side effect.

**Architecture:** Use a disposable PostgreSQL-backed environment and the versioned cross-system contract. A guarded preflight proves topology, configuration, schema releases, sandbox coverage, and the non-member baseline before any request is created. Automated evidence captures identifiers and state transitions without payloads or secrets; a separate terminal gate prevents real LDAP/Graph execution without renewed user approval.

**Tech Stack:** Docker Compose, Bash, curl/jq, Go E2E tests, pytest/httpx, PostgreSQL, KAF sandbox fixtures

**Spec:** `docs/superpowers/specs/2026-09-04-sslvpn-delegation-reliability-hardening-design.md`

## Global Constraints

- WS1, WS2a, WS2b, and WS3 evidence must all pass before this plan starts.
- The default and required rehearsal is side-effect-free; every mutating Tool/connector action must be fixture-backed and audited as simulated.
- Use only existing controlled users, catalog items, approval actors, and SSLVPN fixtures; do not invent production identities.
- Never print secrets, tokens, raw Outbox payloads, Procedure inputs, LDAP DNs, user email addresses, or raw external error bodies.
- The API process must have zero KAF dispatchers; exactly two Worker replicas are required for concurrency evidence.
- Real Graph/LDAP execution is a separately approved operation with named change and recovery owners; this plan must stop before it by default.
- Report status remains `Conditional No-Go` until every mandatory acceptance criterion has current evidence.

---

## File Structure

- Create `scripts/sslvpn-delegation-preflight.sh`: read-only topology/config/readiness gate.
- Create `scripts/sslvpn-delegation-evidence.sh`: redacted state-count and identifier evidence collector.
- Create `itsm-backend/tests/e2e/sslvpn_preflight_script_test.go`: hermetic script behavior tests using fake command binaries.
- Create `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`: controlled lifecycle and idempotency assertions.
- Create KAF `tests/test_kaf_delegation_fault_matrix.py`: deterministic cross-phase fault injection.
- Modify `docs/e2e-testing-guide.md` and KAF cutover runbook: exact rehearsal commands and stop conditions.
- Modify the production-readiness report only after evidence exists.

### Task 1: Build a fail-closed read-only preflight

**Files:**
- Create: `scripts/sslvpn-delegation-preflight.sh`
- Create: `itsm-backend/tests/e2e/sslvpn_preflight_script_test.go`
- Modify: `docs/e2e-testing-guide.md`

**Interfaces:**
- Consumes environment variables `ITSM_BASE_URL`, `KAF_BASE_URL`, Compose project/file paths, and read-only database inspection DSNs supplied through secret files.
- Produces exit 0 only when all mandatory gates pass; emits redacted JSON with booleans/counts/release IDs/checksums.

- [ ] **Step 1: Write failing script tests**

Cover missing commands/env, health 200 with readiness false, one Worker, host-published Worker port, API dispatcher evidence, stale scheduler heartbeat, contract checksum mismatch, KAF delegation disabled, stale Alembic head, and missing sandbox fixture coverage.

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
zero KAF dispatcher construction in API logs/process config
Worker internal /healthz, /readyz, /metrics reachable only from Compose network
dispatcher lifecycle=running and heartbeat fresh
same contract artifact checksum on both systems
sandbox enabled and every mutating registered capability has a fixture
```

- [ ] **Step 4: Run tests and shell validation**

```bash
cd itsm-backend
go test ./tests/e2e -run '^TestSSLVPNDelegationPreflight' -count=1
cd ..
bash -n scripts/sslvpn-delegation-preflight.sh
scripts/sslvpn-delegation-preflight.sh | jq -e '.ready == true and .workerReplicas == 2 and .apiDispatchers == 0'
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
- Modify: `/home/administrator/actions-runner/_work/kaf/kaf/deploy/prod/backend/docker-compose.yml`
- Create: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces one ITSM API, two ITSM Workers, KAF Backend/Gateway, external PostgreSQL-equivalent databases, role-mounted secrets, and no host Worker ports.
- Consumes only fixture/sandbox identities.

- [ ] **Step 1: Validate resolved Compose before startup**

```bash
docker compose -f docker-compose.prod.yml config --no-interpolate
docker compose -f /home/administrator/actions-runner/_work/kaf/kaf/deploy/prod/backend/docker-compose.yml config --no-interpolate
```

Expected: no literal secret value and explicit KAF delegation enablement.

- [ ] **Step 2: Create fresh disposable databases through reviewed bootstrap jobs**

Run WS1 fresh bootstrap for ITSM and `alembic upgrade head` for KAF using migration principals. Do not reuse the developer database with the historical checksum mismatch as fresh-install evidence.

- [ ] **Step 3: Start exact topology**

Start API and KAF, then scale `itsm-worker=2`. Inspect ports and process commands. Run Task 1 preflight and stop if any gate fails.

- [ ] **Step 4: Record immutable deployment identity**

Capture image digests, Git commits, ITSM release manifest checksum, KAF Alembic head, contract checksum, Compose config hashes, and UTC start time. Do not capture environment values.

- [ ] **Step 5: Commit runbook/config clarification**

```bash
git add docker-compose.prod.yml docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "docs(sslvpn): define production-equivalent rehearsal"
```

Commit any KAF Compose clarification separately in the KAF repository.

### Task 3: Add the controlled SSLVPN lifecycle E2E

**Files:**
- Create: `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`
- Modify: `itsm-backend/tests/e2e/sslvpn_scenario_test.go`
- Create: `scripts/sslvpn-delegation-evidence.sh`

**Interfaces:**
- Consumes existing controlled catalog/requester/approver fixture IDs from test setup.
- Produces `eventId`, `taskId`, `correlationId`, and stable execution key in test-local memory and redacted evidence.
- Asserts one Outbox row, one KAF receipt/effect/completion, and one BPMN advance.

- [ ] **Step 1: Write the failing end-to-end test**

The test creates one Service Request through the public API, performs both existing approval actions through authenticated APIs, waits with a bounded deadline, and asserts:

```go
require.Equal(t, 1, evidence.OutboxCount)
require.Equal(t, 1, evidence.KAFReceiptCount)
require.Equal(t, 1, evidence.StepEffectCount)
require.Equal(t, 1, evidence.CompletionConfirmedCount)
require.Equal(t, 1, evidence.BPMNAdvanceCount)
require.Equal(t, "sandbox_effect_simulated", evidence.EffectAuditAction)
```

Every lookup is tenant-scoped. Test failure output contains identifiers and states but no payload/error/lease/identity content.

- [ ] **Step 2: Verify test refuses unsafe environments**

Run once with sandbox disabled or missing fixtures.

Expected: test skips/fails before request creation with `sandbox_precondition_failed`; it must not attempt to compensate after an unauthorized write.

- [ ] **Step 3: Implement evidence collection**

The script queries counts and allowed identifiers only. It hashes fixture actor identities before emitting them and never selects Outbox payload, last error, lease owner, Tool input, or raw result.

- [ ] **Step 4: Run the happy path**

```bash
cd itsm-backend
go test ./tests/e2e -run '^TestSSLVPNKAFRuntimeE2E$' -count=1 -v
cd ..
scripts/sslvpn-delegation-evidence.sh | jq -e '.outboxCount == 1 and .kafReceiptCount == 1 and .bpmnAdvanceCount == 1'
```

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go itsm-backend/tests/e2e/sslvpn_scenario_test.go scripts/sslvpn-delegation-evidence.sh
git commit -m "test(sslvpn): verify delegated runtime exactly once"
```

### Task 4: Prove replay and cross-phase failure behavior

**Files:**
- Create: `/home/administrator/actions-runner/_work/kaf/kaf/tests/test_kaf_delegation_fault_matrix.py`
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
cd /home/administrator/actions-runner/_work/kaf/kaf
uv run pytest tests/test_kaf_delegation_fault_matrix.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_step_fencing.py -q
cd /home/administrator/project/itsm/.worktrees/sslvpn-runtime-validation/itsm-backend
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

### Task 5: Audit tenant, RBAC, masking, and topology boundaries

**Files:**
- Modify: `itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go`
- Modify: `/home/administrator/actions-runner/_work/kaf/kaf/tests/test_kaf_delegation_fault_matrix.py`
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces negative evidence for cross-tenant access, missing delegated permissions, forged identities, secret leakage, and host-exposed Worker ports.

- [ ] **Step 1: Add negative API assertions**

Non-sysadmin roles receive authorization denial for delegated execution view/reconcile/requeue. A sysadmin from another tenant receives not-found/empty results. Forged tenant/task/correlation/version values fail closed in KAF and completion APIs.

- [ ] **Step 2: Add masking assertions**

Scan readiness, metrics, API bodies, default logs, and report attachments for known canary secrets and fixture PII. Assert payloads, raw errors, leases, tokens, signatures, DN, and emails are absent.

- [ ] **Step 3: Verify topology**

Inspect Compose port bindings and container network access. Worker endpoints must be reachable from a peer container and absent from host published ports. API process/config/logs must show no dispatcher construction.

- [ ] **Step 4: Run security-focused checks**

```bash
cd itsm-backend
go test ./tests/e2e ./handlers/delegated_execution ./internal/workerhealth ./router -run 'SSLVPN|Delegated|Tenant|Permission|Mask|Worker' -count=1
cd /home/administrator/actions-runner/_work/kaf/kaf
uv run pytest tests/test_kaf_delegation_fault_matrix.py tests/test_kaf_delegation_readiness.py tests/test_sandbox_policy.py -q
```

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/tests/e2e/sslvpn_kaf_runtime_e2e_test.go docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "test(sslvpn): verify delegation security boundaries"
```

Commit KAF test changes separately.

### Task 6: Stop at the real-side-effect approval gate

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

### Task 7: Final verification and evidence-based release decision

**Files:**
- Modify: `docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`
- Modify: `docs/runbooks/sslvpn-delegation-rehearsal.md`

**Interfaces:**
- Produces the final `Go` or retained `Conditional No-Go` decision with command output, commit/image identity, and explicit backlog exclusions.

- [ ] **Step 1: Run the full ITSM verification**

```bash
cd itsm-backend
go test ./config ./migration ./internal/initialization ./internal/readiness ./internal/bootstrap ./internal/workerhealth ./service ./handlers/delegated_execution ./pkg/seeder ./router ./tests/e2e -count=1
go vet ./config ./migration ./internal/initialization ./internal/readiness ./internal/bootstrap ./internal/workerhealth ./service ./handlers/delegated_execution ./router
cd ..
docker compose -f docker-compose.prod.yml config --no-interpolate
git diff --check
```

- [ ] **Step 2: Run the full KAF verification**

```bash
cd /home/administrator/actions-runner/_work/kaf/kaf
uv run pytest tests/test_itsm_webhooks.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_delivery_migration.py tests/test_kaf_delegation_two_phase_migration.py tests/test_kaf_delegation_repository.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_completion_recovery.py tests/test_kaf_delegation_step_fencing.py tests/test_kaf_delegation_readiness.py tests/test_kaf_delegation_fault_matrix.py tests/test_sandbox_policy.py tests/test_vpn_grant_tool.py tests/test_vpn_permission_grant_procedure_doc.py -q
uv run ruff check src/acp tests/test_kaf_delegation_*.py tests/test_sandbox_policy.py
uv run alembic heads
docker compose -f deploy/prod/backend/docker-compose.yml config --no-interpolate
git diff --check
```

- [ ] **Step 3: Re-run runtime preflight and evidence collection**

```bash
scripts/sslvpn-delegation-preflight.sh | jq -e '.ready == true'
scripts/sslvpn-delegation-evidence.sh | jq -e '.outboxCount == 1 and .kafReceiptCount == 1 and .stepEffectCount == 1 and .completionConfirmedCount == 1 and .bpmnAdvanceCount == 1'
```

Expected: all mandatory gates true, two Workers, zero API consumers, exact contract/release identities, one simulated effect, one completion, and one BPMN advance.

- [ ] **Step 4: Update the report precisely**

Change to `Go` only if every mandatory design criterion has current evidence. Explicitly list email alert delivery/recipients, Langfuse governance, TLS/mTLS, CIDR policy, and any unexecuted real external rehearsal as backlog or not executed; never present them as delivered.

- [ ] **Step 5: Commit the final evidence**

```bash
git add docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md docs/runbooks/sslvpn-delegation-rehearsal.md
git commit -m "docs: record SSLVPN delegation release decision"
```
