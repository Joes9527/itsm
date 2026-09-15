# SSLVPN KAF Worker Production Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the SSLVPN `kaf_delegate_requested` delivery loop from `itsm-api` to at least two dedicated `itsm-worker` replicas without dual consumers, while making cross-process delivery, recovery, observability, reconciliation and the controlled real-change rehearsal release-ready.

**Architecture:** Keep ITSM authoritative for WorkItem, BPMN, tenant, RBAC and audit. The new worker is a second executable from the same Go module: it constructs only the dependencies required to claim and deliver KAF Outbox events, and exposes an internal-only health/metrics listener. `itsm-api` retains its existing non-KAF background jobs in this phase but removes every KAF dispatcher construction and startup path. KAF remains a separate Docker deployment that receives the signed event through its internal gateway and calls ITSM only through task-scoped APIs.

**Tech Stack:** Go/Gin/Ent/PostgreSQL, Docker Compose and Docker secrets, Prometheus client, FastAPI/KAF, Nginx internal gateway, Microsoft Graph controlled fixture.

## Global Constraints

- Follow `AGENTS.md`: ITSM is the source of truth; KAF never writes the ITSM database directly.
- Move only `kaf_delegate_requested` in this release. API retains callback, notification, SLA, Embedding and generic Outbox jobs until separately migrated.
- The API and Worker must never concurrently consume KAF Outbox events; no API fallback or compatibility path is permitted.
- Preserve existing event ID, HMAC signature, task scope, tenant validation, KAF delivery dedupe and completion replay semantics.
- Unknown handler/configuration/contract errors fail closed into visible `blocked`; an external-send result that cannot be known becomes `delivery_unknown`, never a blind retry.
- Configuration varies by deployment. Do not hardcode URLs, addresses, tenant data, recipient addresses, thresholds or credentials.
- Credentials are mounted as read-only Docker secrets; log fields and alert payloads are allowlisted and must never contain payloads, trace content, prompts, passwords or tokens.
- No database data migration is part of this plan. ITSM and KAF use separate logical databases and least-privilege runtime/migration users on the shared PostgreSQL instance.
- Preserve the dirty workspace: do not reformat, revert, stage or commit unrelated changes. This plan intentionally has no `git commit` steps.

---

## Phase 0 — Lock the delivery contract before changing runtime wiring

### Task 1: Correct the implementation-facing design and establish the KAF worker contract

**Files:**
- Modify: `docs/superpowers/specs/2026-09-03-sslvpn-worker-production-readiness-design.md`
- Modify: `itsm-backend/main.go`
- Create: `itsm-backend/internal/bootstrap/kaf_worker_test.go`
- Modify: `itsm-backend/internal/bootstrap/kaf_outbox_lifecycle_test.go`

**Interfaces:**
- Produces `NewKAFWorkerApplication() (*KAFWorkerApplication, error)` and `(*KAFWorkerApplication).Run(context.Context) error` for later tasks.
- Produces `(*Application).RunAPI(context.Context) error` and `(*Application).startAPIRuntime(context.Context) func()`; these replace the current `(*Application).Run()` production path.

- [ ] **Step 1: Add failing bootstrap lifecycle tests.**

```go
func TestApplication_StartAPIRuntimeDoesNotStartKAFDispatcher(t *testing.T) {
    runner := &blockingKafOutboxRunner{started: make(chan struct{})}
    app := newLifecycleTestApplication(t, runner)
    wait := app.startAPIRuntime(context.Background())
    defer wait()
    require.Zero(t, runner.runs.Load())
}

func TestKAFWorker_RunStartsDispatcherAndHealth(t *testing.T) {
    dispatcher := &fakeKAFRunner{}
    health := &fakeHealthRunner{}
    worker := newKAFWorkerTestApplication(t, dispatcher, health)
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() { done <- worker.Run(ctx) }()
    require.Eventually(t, func() bool {
        return dispatcher.StartCalls() == 1 && health.StartCalls() == 1
    }, time.Second, 10*time.Millisecond)
    cancel()
    require.NoError(t, <-done)
    require.Equal(t, 1, dispatcher.StartCalls())
    require.Equal(t, 1, health.StartCalls())
}
```

- [ ] **Step 2: Run the focused tests and confirm they fail because the role-specific methods do not exist.**

Run: `cd itsm-backend && go test ./internal/bootstrap -run 'Test(Application_StartAPIRuntimeDoesNotStartKAFDispatcher|KAFWorker_RunStartsDispatcherAndHealth)$' -count=1`

Expected: compilation failure for `startAPIRuntime`, `KAFWorkerApplication`, or Worker lifecycle dependency injection hooks.

- [ ] **Step 3: Record the exact runtime split in the production-readiness design.**

Keep the already accepted rule: API no longer starts the KAF dispatcher, but it deliberately retains callback, notification, SLA and Embedding jobs. Add a short implementation note that the Worker owns only the KAF event type and cannot register business HTTP routes. Do not alter Service Request, BPMN or KAF Procedure semantics.

- [ ] **Step 4: Add testable role constructors instead of a boolean role switch.**

In `internal/bootstrap/app.go`, extract API background startup into `startAPIRuntime` and API serving into `RunAPI`; they start existing non-KAF workers and the HTTP server but never call `startKafOutboxDispatcher`. Create `internal/bootstrap/kaf_worker.go` with the focused application type and constructor:

```go
type workerHealthRunner interface {
    Run(context.Context) error
}

type KAFWorkerApplication struct {
    cfg          *config.Config
    logger       *zap.SugaredLogger
    dbClient     *ent.Client
    dispatcher   kafOutboxRunner
    healthRunner workerHealthRunner
}

func NewKAFWorkerApplication() (*KAFWorkerApplication, error)
func (app *KAFWorkerApplication) Run(ctx context.Context) error
```

`NewKAFWorkerApplication` loads configuration, validates only worker-required settings, opens the ITSM database using the Worker runtime role, constructs the KAF dispatcher and health server, and returns errors rather than calling `log.Fatal`. It must not create a Gin router, controllers, connector manager, embedding pipeline or API background jobs.

In `main.go`, create the signal context once and pass it to `app.RunAPI(ctx)`. In tests, inject dispatcher and health runners through narrow interfaces so lifecycle calls can be asserted without a database listener.

- [ ] **Step 5: Re-run bootstrap tests and the existing KAF lifecycle tests.**

Run: `cd itsm-backend && go test ./internal/bootstrap -run 'KAFOutbox|RunAPI|KAFWorker' -count=1`

Expected: API test proves zero KAF dispatcher starts; Worker test proves exactly one dispatcher starts; existing lifecycle behaviour remains covered under the new Worker owner.

---

## Phase 1 — Make KAF delivery a recoverable, fail-closed Worker responsibility

### Task 2: Add worker-only KAF configuration validation and secret-file loading

**Files:**
- Modify: `itsm-backend/config/config.go`
- Modify: `itsm-backend/config/kaf_outbox_config_test.go`
- Create: `itsm-backend/config/worker_startup_config_test.go`

**Interfaces:**
- Consumes `KAF_WEBHOOK_URL`, `KAF_WEBHOOK_SECRET`, `KAF_OUTBOX_BATCH_SIZE`, `KAF_OUTBOX_POLL_INTERVAL`.
- Produces `ValidateKAFWorkerStartupConfig(cfg *Config) error` and a secret-aware environment loader usable by API and Worker configuration.

- [ ] **Step 1: Write failing validation tests.**

```go
func TestValidateKAFWorkerStartupConfigRejectsMissingWebhookURL(t *testing.T) {
    cfg := &Config{KAFOutbox: KAFOutboxConfig{WebhookSecret: "secret"}}
    require.ErrorContains(t, ValidateKAFWorkerStartupConfig(cfg), "KAF_WEBHOOK_URL")
}

func TestValidateKAFWorkerStartupConfigRejectsMissingSecret(t *testing.T) {
    cfg := &Config{KAFOutbox: KAFOutboxConfig{WebhookURL: "https://kaf.internal/webhooks/itsm"}}
    require.ErrorContains(t, ValidateKAFWorkerStartupConfig(cfg), "KAF_WEBHOOK_SECRET")
}
```

- [ ] **Step 2: Run the config tests and confirm they fail.**

Run: `cd itsm-backend && go test ./config -run 'TestValidateKAFWorkerStartupConfig' -count=1`

Expected: missing symbol compilation failure.

- [ ] **Step 3: Implement required-vs-optional role semantics.**

Keep `loadKAFOutboxConfig` API-safe: an API can load without a webhook URL because it no longer dispatches. Add `ValidateKAFWorkerStartupConfig` and call it only from `NewKAFWorkerApplication`; it requires an absolute HTTP(S) URL, non-empty HMAC secret, positive batch/poll values and a configured internal health port. A missing or unreadable required secret returns an error and causes Worker startup/readiness failure.

Add `readEnvironmentOrSecret(name string) (string, error)`: accept `NAME` or `NAME_FILE`; reject both being non-empty; read a trimmed regular file from `NAME_FILE`; reject empty content. Use it for `DB_PASSWORD`, `KAF_WEBHOOK_SECRET`, `JWT_SECRET` only where that process requires the value. Do not log the resolved value or file path.

- [ ] **Step 4: Extend existing config test coverage.**

Add tests for `_FILE` success, both variables set, unreadable file and empty secret. Assert only error classes/key names, never a secret value.

- [ ] **Step 5: Run the complete configuration package.**

Run: `cd itsm-backend && go test ./config -count=1`

Expected: all existing config tests and the new worker/secret cases pass.

### Task 3: Harden the existing KAF dispatcher without introducing a second delivery route

**Files:**
- Modify: `itsm-backend/service/kaf_outbox_dispatcher.go`
- Modify: `itsm-backend/service/kaf_outbox_dispatcher_test.go`
- Modify: `itsm-backend/service/outbox_event_repository_test.go`

**Interfaces:**
- Consumes `OutboxEventRepository.ClaimDueByEventType`, `MarkDeliveryAttemptStarted`, `MarkRetry`, `MarkBlocked`, `MarkDeliveryUnknown`, `MarkDeadLetter`, and `MarkPublished`.
- Produces a single `KafOutboxDispatcher.Run(context.Context)` consumer used only by `itsm-worker`.

- [ ] **Step 1: Add failing delivery-state tests.**

```go
func TestKafOutboxDispatcherMarksAttemptBeforeHTTPPost(t *testing.T) { /* assert durable attempt marker */ }
func TestKafOutboxDispatcherBlocksPermanentKAFRejection(t *testing.T) { /* 401/403/404/422 -> blocked */ }
func TestKafOutboxDispatcherMarksTransportOutcomeUnknown(t *testing.T) { /* request started, timeout -> delivery_unknown */ }
func TestKafOutboxDispatcherDeadLettersAfterConfiguredMaxAttempts(t *testing.T) { /* retry ceiling -> dead_letter */ }
func TestKafOutboxDispatcherExpiredAttemptBecomesDeliveryUnknown(t *testing.T) { /* lease recovery is manual */ }
```

- [ ] **Step 2: Run the new tests and confirm current dispatcher behaviour fails them.**

Run: `cd itsm-backend && go test ./service -run 'TestKafOutboxDispatcher(MarksAttemptBeforeHTTPPost|BlocksPermanentKAFRejection|MarksTransportOutcomeUnknown|DeadLettersAfterConfiguredMaxAttempts|ExpiredAttemptBecomesDeliveryUnknown)$' -count=1`

Expected: failures because the current dispatcher retries all non-2xx responses and does not write a delivery-attempt marker.

- [ ] **Step 3: Add explicit KAF delivery classification.**

Before each webhook call, call `MarkDeliveryAttemptStarted` with the stable event ID. Map malformed local request, invalid payload/signature contract, HTTP 401/403/404/422 and handler/configuration errors to `MarkBlocked`. Map HTTP 429 and 5xx to retry with the existing capped exponential backoff. After the HTTP request was sent, any timeout, reset, unreadable response, or process recovery of an unfinalized attempt becomes `MarkDeliveryUnknown`; it must create the existing audit action and never trigger automatic redelivery. Add `MaxAttempts` to `KAFOutboxConfig`, validate it from `KAF_OUTBOX_MAX_ATTEMPTS` in the same `1..20` range as the shared worker, and call `MarkDeadLetter` on a retryable failure at the ceiling.

Treat only a successful KAF acceptance response as `MarkPublished`. Do not add an API consumer, compatibility retry path or a second table.

- [ ] **Step 4: Cover concurrent Worker ownership using the existing conditional-claim repository.**

Add a two-dispatcher test sharing one repository and one due KAF event. Assert one HTTP delivery, one final state transition and no stale-claim completion. Retain the existing lease recovery tests.

- [ ] **Step 5: Run focused reliability regressions.**

Run: `cd itsm-backend && go test ./service -run 'KafOutboxDispatcher|OutboxEventRepository_(ClaimDueAllowsOnlyOneConcurrentClaimer|ClaimDueRecoversExpiredLease|RejectsStaleLeaseCompletion)' -count=1`

Expected: delivery state classification, dedupe and lease fencing pass together.

### Task 4: Build the Worker executable, private health listener and Prometheus metrics

**Files:**
- Create: `itsm-backend/cmd/kaf_worker/main.go`
- Create: `itsm-backend/internal/workerhealth/server.go`
- Create: `itsm-backend/internal/workerhealth/server_test.go`
- Create: `itsm-backend/service/kaf_outbox_metrics.go`
- Modify: `itsm-backend/internal/bootstrap/kaf_worker.go`
- Modify: `itsm-backend/service/kaf_outbox_dispatcher.go`

**Interfaces:**
- Produces executable `kaf-worker`.
- Produces `GET /healthz`, `GET /readyz`, and internal `GET /metrics` from the Worker listener.
- Exposes only allowlisted metrics labels: `event_type`, `status`, `error_class`; never tenant ID, task ID, event ID, URL or payload.

- [ ] **Step 1: Write failing health and metric tests.**

```go
func TestWorkerReadinessFailsWithoutSuccessfulConfigAndDatabaseCheck(t *testing.T) { /* expect 503 */ }
func TestWorkerReadinessIsHealthyWhenDispatcherDependenciesAreReady(t *testing.T) { /* expect 200 */ }
func TestKafOutboxMetricsDoNotExposeEventOrTenantIDs(t *testing.T) { /* gather output and assert absent */ }
```

- [ ] **Step 2: Run the tests and confirm missing packages fail.**

Run: `cd itsm-backend && go test ./internal/workerhealth ./service -run 'TestWorkerReadiness|TestKafOutboxMetrics' -count=1`

Expected: package/symbol failures.

- [ ] **Step 3: Implement an internal-only worker listener.**

`cmd/kaf_worker/main.go` must call `bootstrap.NewKAFWorkerApplication()` and exit non-zero on construction or run failure. The Worker server may expose only `/healthz`, `/readyz` and `/metrics`; it must not import Gin router registration. `readyz` returns 200 only after required configuration validation, a DB ping and successful dispatcher construction; otherwise it returns 503 with a reason class but no secret or endpoint value. The listener port is a Worker-only deployment setting and is never published to the host in Compose.

Add counters for delivery attempts/final states and gauges for ready state, due backlog and oldest due-event age. Obtain backlog/age through a repository query scoped to `kaf_delegate_requested`; aggregate without per-tenant labels. Measure accepted-delivery latency from event creation to `MarkPublished`.

- [ ] **Step 4: Wire dispatcher results to metrics without changing persistence semantics.**

Increment metrics only after repository transitions succeed. Ensure `blocked`, `delivery_unknown`, `dead_letter`, retry and published transitions are individually observable. Keep structured logs to allowlisted event type/status/error class.

- [ ] **Step 5: Run Worker and service tests.**

Run: `cd itsm-backend && go test ./cmd/kaf_worker ./internal/workerhealth ./internal/bootstrap ./service -run 'KAF|Outbox|Worker|Readiness|Metrics' -count=1`

Expected: Worker starts only the KAF loop, readiness reflects dependencies, and metrics expose no sensitive identifiers.

---

## Phase 2 — Hard-cut deployment and cross-system boundary

### Task 5: Produce one image with API and Worker binaries and deploy two Worker replicas

**Files:**
- Modify: `itsm-backend/Dockerfile.prod`
- Modify: `itsm-backend/Dockerfile`
- Modify: `docker-compose.prod.yml`
- Modify: `docker-compose.dev.yml`
- Modify: `docs/DEVELOPMENT_GUIDE.md`
- Modify: `docs/dev-commands-reference.md`

**Interfaces:**
- API image command remains `./main`.
- Worker Compose service command is `./kaf-worker`.
- Worker consumes `KAF_WEBHOOK_URL`, `KAF_WEBHOOK_SECRET_FILE`, ITSM database secret files and Worker health port configuration.

- [ ] **Step 1: Add a Compose rendering test/checklist before modifying deployment.**

Create a shell-verifiable deployment check in the command reference that renders production Compose and asserts: one `itsm-api` service, one `itsm-worker` service, no fixed Worker container name, no host port for the Worker, and no `KAF_WEBHOOK_SECRET` literal in rendered output.

Run: `docker compose -f docker-compose.prod.yml config`

Expected before implementation: no `itsm-worker` service is present.

- [ ] **Step 2: Build both binaries in each backend Dockerfile.**

Build `./main.go` to `/out/main` and `./cmd/kaf_worker` to `/out/kaf-worker`; copy both into the runtime image as the non-root `app` user. Keep API as the default `CMD ["./main"]`; the Worker is selected only by Compose `command: ["./kaf-worker"]`.

- [ ] **Step 3: Add a separate Worker Compose service and remove fixed-name scaling blockers.**

Create `itsm-worker` from the same image with no `container_name`, no published port, an internal healthcheck against its local Worker readiness endpoint, and `--scale itsm-worker=2` as the standard Compose invocation. Remove the fixed `container_name` from the API service too if it prevents rolling deployment naming, but do not change frontend service discovery: use the Compose service name `itsm-backend` for API-to-API links.

Use Compose `secrets` mounts for ITSM API/Worker database passwords and KAF webhook secret. Pass only `*_FILE=/run/secrets/<role-secret>` paths into containers. The API must not receive `KAF_WEBHOOK_SECRET_FILE`; the Worker must not receive the JWT signing secret unless a demonstrated dependency requires it.

- [ ] **Step 4: Align production database topology.**

Replace the production-only in-stack PostgreSQL assumption with external shared-instance connection configuration for the ITSM logical database. Keep local/dev PostgreSQL Compose behaviour for developer use. Define separate runtime and migration secret names and ensure `itsm-init`/migration uses migration credentials while API/Worker use runtime credentials. Do not add KAF credentials to ITSM services.

- [ ] **Step 5: Render and run deployment checks.**

Run: `docker compose -f docker-compose.prod.yml config`

Run: `docker compose -f docker-compose.prod.yml up -d itsm-backend && docker compose -f docker-compose.prod.yml up -d --scale itsm-worker=2`

Expected: API is healthy without KAF dispatcher; two Worker containers become ready; Worker has no host-published port; missing secret prevents the affected role from becoming ready.

### Task 6: Close KAF Gateway and callback deployment configuration

> **Deployment Backlog by product decision (2026-09-04):** The private HTTP
> Gateway ingress, Worker source-IP/CIDR policy and TLS/mTLS rollout are owned
> by the deployment team. This code plan does not add a mandatory Gateway
> configuration or assume an address range.

**Files:**
- Modify: `/Users/julian/development/agent-control-plane/deploy/prod/gateway/nginx.conf`
- Modify: `/Users/julian/development/agent-control-plane/deploy/prod/backend/docker-compose.yml`
- Modify: `/Users/julian/development/agent-control-plane/deploy/prod/.env.example`
- Modify: `/Users/julian/development/agent-control-plane/deploy/prod/README.md`
- Test: `/Users/julian/development/agent-control-plane/tests/test_kaf_delegation_contract.py`
- Test: `/Users/julian/development/agent-control-plane/tests/test_kaf_delegation_pipeline.py`

**Interfaces:**
- ITSM Worker posts only to internal `POST /webhooks/itsm` through KAF Gateway/LB with `X-Event-ID` and `X-Webhook-Signature`.
- KAF Backend receives `ITSM_KAF_URL`, `ITSM_KAF_WEBHOOK_SECRET_FILE`, `ITSM_KAF_AUTOMATION_TOKEN_FILE` as required delegation configuration.

- [ ] **Step 1: Add failing deployment-contract tests.**

```python
def test_kaf_delegation_prod_config_requires_dedicated_itsm_settings():
    assert "ITSM_KAF_URL" in backend_compose
    assert "ITSM_KAF_AUTOMATION_TOKEN_FILE" in backend_compose
    assert "ITSM_KAF_WEBHOOK_SECRET_FILE" in backend_compose

def test_gateway_has_exact_internal_itsm_webhook_route():
    assert "location = /webhooks/itsm" in nginx_config
```

- [ ] **Step 2: Run the contract tests and confirm current deployment definitions fail.**

Run: `cd /Users/julian/development/agent-control-plane && ENV_FILE=/dev/null PYTHONPATH=src pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q`

Expected: deployment-specific assertions fail before the dedicated variables and exact route are added; unrelated full-suite failures are not used as delegation evidence.

- [ ] **Step 3: Add the exact route and least-privilege configuration.**

Add an exact Nginx `location = /webhooks/itsm` route to the backend upstream, accepting only the ITSM Worker private source range and the required POST method. Do not rely on a catch-all public route. Add the dedicated KAF-to-ITSM URL, webhook secret and automation token secret-file variables to the KAF backend deployment. Keep legacy lifecycle settings isolated; do not reuse their secret as a KAF delegation credential.

- [ ] **Step 4: Test both authorization directions.**

Verify an allowed Worker request with correct signature yields KAF acceptance; a missing/invalid signature and a disallowed source are rejected. Verify KAF completion reaches only the internal ITSM task-scoped endpoint with the automation token and is rejected for wrong tenant, task, action or version.

- [ ] **Step 5: Run KAF targeted regression suite and syntax validation.**

Run: `cd /Users/julian/development/agent-control-plane && ENV_FILE=/dev/null PYTHONPATH=src pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py -q`

Expected: signature, dedupe, lease recovery and completion replay contracts pass.

---

## Phase 3 — Operations and human reconciliation (same release plan, independently shippable)

### Task 7: Add ITSM-owned reconciliation query and audited repair actions

**Files:**
- Create: `itsm-backend/handlers/delegated_execution/service.go`
- Create: `itsm-backend/handlers/delegated_execution/handler.go`
- Create: `itsm-backend/handlers/delegated_execution/*_test.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/pkg/seeder/seeder.go`

No separate entity/repository wrapper is introduced: the existing Ent Outbox
entity is the persistence boundary, so another pass-through layer would not
remove complexity and would violate the repository architecture constraints.

**Interfaces:**
- `GET /api/v1/delegated-executions?tenantId=&taskId=&eventId=&correlationId=` requires `delegated_execution.view`.
- `POST /api/v1/delegated-executions/{eventId}/reconcile` requires `delegated_execution.reconcile`.
- `POST /api/v1/delegated-executions/{eventId}/requeue` requires `delegated_execution.requeue` and can act only on a non-ambiguous blocked event.

- [x] **Step 1: Write authorization and state-machine tests.**

```go
func TestRequeueRejectsDeliveryUnknown(t *testing.T) { /* 409, no state change */ }
func TestRequeueRestoresBlockedEventWithReasonAndAudit(t *testing.T) { /* pending + audit */ }
func TestReconcileRequiresTenantScopedCapability(t *testing.T) { /* 403 for cross-tenant/non-capable actor */ }
```

- [x] **Step 2: Run the new handler tests and confirm missing routes/services fail.**

Run: `cd itsm-backend && go test ./handlers/delegated_execution -run 'Test(Requeue|Reconcile)' -count=1`

Expected: package or route/service failures.

- [x] **Step 3: Implement the vertical slice.**

The repository performs tenant-scoped Outbox reads and status transitions; it never queries KAF databases. The service accepts a required reason and writes ITSM audit records containing actor, tenant, event/task/correlation IDs and conclusion. It exposes a redacted KAF/Langfuse correlation link only; it never proxies trace content. `requeue` is permitted only when the stored conclusion is `not_accepted_not_started`; `delivery_unknown` is reconcile-only. Register capability IDs in the permission seed/configuration rather than hardcoding roles.

- [x] **Step 4: Register routes and verify router authorization.**

Keep controllers thin: bind DTO, require existing RBAC middleware, invoke the delegated-execution service, return DTO. Add router tests proving endpoint ACLs are present and no requester/approver endpoint can obtain operations data.

- [x] **Step 5: Run focused vertical-slice regressions.**

Run: `cd itsm-backend && go test ./handlers/delegated_execution ./router ./service -run 'DelegatedExecution|Requeue|Reconcile|Outbox' -count=1`

Expected: authorized audit-preserving repair works; ambiguous delivery cannot be force resent.

Implementation evidence: `go test ./handlers/delegated_execution ./router ./service -run 'DelegatedExecution|Requeue|Reconcile|Outbox' -count=1` passes. The current read model filters by event ID, task ID and status, and returns the redacted correlation ID when present. Payload, raw delivery error and lease values are never returned.

### Task 8: Wire deployment-configured alert delivery and Langfuse governance prerequisites as a gated Phase 3 dependency

> **Backlog by product decision (2026-09-04):** Email alert delivery, recipient
> selection and Langfuse governance are deferred. Do not implement a partial
> alert path or invent recipient values in this release. Their later release
> must complete this task as a whole before enabling the feature.

**Files:**
- Modify: `itsm-backend/config/config.go`
- Create: `itsm-backend/service/kaf_worker_alert_service.go`
- Create: `itsm-backend/service/kaf_worker_alert_service_test.go`
- Modify: `docker-compose.prod.yml`
- Modify: `/Users/julian/development/agent-control-plane/src/acp/observability.py`
- Modify: `/Users/julian/development/agent-control-plane/deploy/prod/gateway/nginx.conf`
- Modify: `docs/superpowers/specs/2026-09-03-sslvpn-worker-production-readiness-design.md`

**Interfaces:**
- Consumes deployment `KAF_ALERT_EMAIL_RECIPIENTS` and mail-transport secrets.
- Produces deduplicated allowlisted alert emails and Worker alert delivery metrics.

- [ ] **Step 1: Write failing alert privacy and configuration tests.**

```go
func TestKAFWorkerAlertRequiresRecipientsWhenAlertsEnabled(t *testing.T) { /* startup validation error */ }
func TestKAFWorkerAlertDeduplicatesSameFingerprintForThirtyMinutes(t *testing.T) { /* one send */ }
func TestKAFWorkerAlertBodyExcludesPayloadAndSecrets(t *testing.T) { /* redaction assertion */ }
```

- [ ] **Step 2: Run the alert tests and confirm the service does not yet exist.**

Run: `cd itsm-backend && go test ./service -run 'TestKAFWorkerAlert' -count=1`

Expected: missing-symbol failure.

- [ ] **Step 3: Implement only the approved alert boundary.**

Use the established `EmailService` as a transport port; do not repurpose requester or approval notifications. Persist/derive a stable alert fingerprint from environment, error class and event ID; suppress duplicates for 30 minutes and emit a recovery notification once state recovers. The email body contains only environment, severity, error class, event/task/correlation IDs, retry count and the ITSM operation URL. Sender credentials and recipients are never logged.

- [ ] **Step 4: Apply Langfuse access prerequisites without copying traces into ITSM.**

Keep Langfuse trace input/output masking active and add metadata allowlisting at every `record_langfuse_trace` call path. Restrict the Langfuse gateway route to the shared authenticated platform-operations group and remove direct public port publication. Data retention/export/deletion policy and the final value of the recipient group remain the separately owned Backlog; do not invent values in code.

- [ ] **Step 5: Run targeted tests and deployment checks.**

Run: `cd itsm-backend && go test ./service ./config -run 'KAFWorkerAlert|KAF.*Config' -count=1`

Run: `cd /Users/julian/development/agent-control-plane && ENV_FILE=/dev/null PYTHONPATH=src pytest tests -k 'langfuse or kaf_delegation' -q`

Expected: alert content and dedupe are proven; Langfuse trace masking paths remain covered; any unrelated KAF full-suite failure is reported separately.

---

## Phase 4 — Production-equivalent verification and controlled SSLVPN rehearsal

### Task 9: Execute the release evidence matrix and controlled external-change rehearsal

**Files:**
- Modify: `docs/testing/kaf-delegation-release-closeout-fixture.md`
- Create: `docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`
- Modify: `docs/superpowers/specs/2026-09-03-sslvpn-worker-production-readiness-design.md`

**Interfaces:**
- Consumes the Worker deployment, KAF internal webhook, task-scoped completion API, configured operational email and the existing controlled Graph fixture.
- Produces a redacted evidence report with one change/reference ID and explicit Go/No-Go result.

- [x] **Step 1: Run code-level regressions before touching external systems.**

Run: `cd itsm-backend && go test ./tests/e2e -run '^TestSSLVPNScenarioE2E$' -count=1`

Run: `cd itsm-backend && go test ./handlers/service_request -run 'SSLVPN.*(KAF|Delegation)|KAF.*SSLVPN' -count=1`

Run: `cd itsm-backend && go test ./service -run 'KafOutboxDispatcher|KafDelegation|BPMNKafCompletion' -count=1`

Expected: all targeted SSLVPN and KAF delivery paths pass before the controlled change window opens.

Executed 2026-09-04: all three targeted commands passed locally.

- [ ] **Step 2: Validate production-equivalent topology without external side effects.**

Verify both Worker readiness endpoints, one API readiness endpoint, two Worker replicas, KAF Gateway health, KAF recovery configuration, cross-database denial using actual runtime users and migration version. Capture only IDs, status classes and timestamps. Mail test delivery is deferred with Task 8.

- [ ] **Step 3: Obtain explicit execution-window confirmation and establish the non-member baseline.**

Use the existing controlled fixture as the only source of test identity, target group and recovery tool. The designated recovery owner performs a read-only Graph membership check. If membership is present, execute the fixture recovery path and re-check; do not start the authorization test without a recorded non-member baseline.

- [ ] **Step 4: Execute exactly one controlled SSLVPN request and collect evidence.**

Create one SR with the change/reference ID, complete the two normal approvals, record task/event/correlation IDs, observe exactly one Worker claim and KAF delivery, verify one external group add, then replay the same completion/action and prove no second external effect and only one BPMN advancement.

- [ ] **Step 5: Recover and publish the Go/No-Go report.**

Run the fixture recovery Tool, confirm by read-only Graph query that the member is absent. Record API/Worker/KAF/Graph/audit evidence in the report without secrets, trace bodies, prompts or payloads. Mark No-Go if recovery is uncertain, a duplicate effect occurs, an authorization boundary fails or any sensitive content appears. Alert delivery validation remains Task 8 Backlog.

---

## Plan Self-Review

### Spec coverage

- Unique KAF Worker consumer and no API fallback: Tasks 1, 3 and 5.
- Failure classification, lease recovery, dedupe and replay: Task 3.
- Worker-specific readiness, metrics, two replicas and secret injection: Tasks 2, 4 and 5.
- KAF network/callback boundary: Task 6.
- ITSM-owned reconciliation: Task 7.
- Email and Langfuse operational boundary: Task 8, gated by the separately owned data-governance Backlog.
- Production-equivalent SSLVPN and external cleanup: Task 9.
- Separate databases and no data migration: global constraints and Task 5; data migration remains out of scope.

### Placeholder scan

The plan contains no unresolved placeholders or unscoped testing/error-handling steps. Each task names concrete files, interfaces and verification commands.

### Type consistency

The API/Worker split uses `Application.RunAPI`, `KAFWorkerApplication`, and the existing `kafOutboxRunner` interface. The delivery task keeps `KafOutboxDispatcher` as the only KAF consumer and reuses existing `OutboxEventRepository` transition methods. Reconciliation uses configuration-backed capability IDs rather than hardcoded roles.
