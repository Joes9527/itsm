# BPMN Integration Blocker Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the tenant-email, ticket-notification read-contract, and pre-enqueue BPMN channel-validation blockers found by the final scoped review at `fb0b2560`.

**Architecture:** Keep `TicketNotificationService` as the durable delivery owner, but pass tenant identity into the existing `EmailService` boundary and separate logical email routing from connector-name lookup. Expose ticket-notification read operations under an unambiguous API namespace backed by `TicketNotificationController`, and make the CC callback normalizer validate/canonicalize channels before the completion transaction mutates task or process state.

**Tech Stack:** Go 1.24, Gin, Ent, PostgreSQL/SQLite integration tests, Next.js/TypeScript, Jest, React Testing Library.

**Spec:** `docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md`

## Global Constraints

- Work only in `/home/administrator/project/itsm/.worktrees/bpmn-instance-authorization` on `feat/bpmn-instance-authorization`; do not touch main or any other worktree.
- Backend remains authoritative for tenant isolation, RBAC, callback validation, delivery state, and audit behavior.
- Every callback configuration error fails before task/process/audit/outbox mutation; already-enqueued callback rows remain durably retryable.
- External delivery is at-least-once, uses stable internal keys, and never crosses tenant/provider boundaries.
- Logs must not contain recipients, subjects, ticket content, raw provider errors, credentials, tokens, or recipient/user ID lists.
- Use TDD, normal Ent generation only if schema changes, real PostgreSQL tests without skips, and one commit per task.

---

### Task 1: Tenant-Aware Email Delivery And Sanitized Failure Semantics

**Files:**
- Modify: `itsm-backend/service/email_service.go`
- Modify: `itsm-backend/service/email_service_test.go`
- Modify: `itsm-backend/service/ticket_notification_service.go`
- Modify: `itsm-backend/service/ticket_notification_delivery_worker_test.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Test: `itsm-backend/internal/bootstrap/bpmn_callback_worker_test.go`
- Test: `itsm-backend/service/ticket_workflow_service_test.go`

**Interfaces:**
- Consumes: `TicketNotification.TenantID`, `EmailService`, `connector.Manager`, stable notification `delivery_key`.
- Produces: `type GraphProvider func(tenantID int) (GraphMailSender, string, bool)`, `SetGraphProvider(GraphProvider)`, `SendForTenant(context.Context, int, *EmailMessage) error`, and `SendTicketNotificationForTenant(context.Context, int, []string, string, string, string, string) error`.

- [ ] **Step 1: Write failing tenant/provider, fallback, classification, and log tests**

```go
func TestEmailServiceUsesOnlyRequestedTenantGraphProvider(t *testing.T) {
    // Record provider tenant IDs; tenant 2 must never consult or send through tenant 1.
}

func TestEmailServiceFallsBackToSMTPAfterGraphRuntimeFailure(t *testing.T) {
    // Inject a failing Graph sender and an SMTP send seam; assert one Graph attempt then SMTP success.
}

func TestTicketNotificationWorkerTreatsMalformedEmailAsPermanent(t *testing.T) {
    // A nonempty malformed address must finish as failed/delivery_target_invalid with no retry lease.
}

func TestEmailAndCCLogsContainOnlyFixedErrorClasses(t *testing.T) {
    // Observer output must exclude recipient, subject, raw provider error, ticket content, and user IDs.
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `cd itsm-backend && go test ./service ./internal/bootstrap -run 'TestEmailService|TestTicketNotificationWorker.*Email|TestEmailAndCCLogs' -count=1 -v`

Expected: FAIL because provider lookup lacks tenant input, Graph errors return before SMTP, malformed addresses map to transient send failure, and logs contain sensitive fields.

- [ ] **Step 3: Implement tenant-aware routing and sanitized fallback**

```go
type GraphProvider func(tenantID int) (GraphMailSender, string, bool)

func (s *EmailService) SendForTenant(ctx context.Context, tenantID int, msg *EmailMessage) error {
    // Validate first. Try only graphProvider(tenantID). On Graph runtime failure,
    // emit a fixed error_class and attempt configured SMTP. Return an error only
    // when every available route fails; never log provider errors or message data.
}
```

Update bootstrap provider lookup to `connectorManager.Get(tenantID, "msgraph-email")`; remove tenant `1`. Make the durable worker validate email syntax before sending and call `SendTicketNotificationForTenant` with `row.TenantID`. Remove PII/raw-error fields from Graph, SMTP, template, and CC logs touched by this path.

- [ ] **Step 4: Run focused normal/race and production-wiring tests**

Run: `cd itsm-backend && go test ./service ./internal/bootstrap -run 'Email|TicketNotification|CCTicket' -count=1`

Run: `cd itsm-backend && go test -race -p 1 ./service ./internal/bootstrap -run 'Email|TicketNotification|CCTicket' -count=1`

Expected: PASS; tenant 2 never uses tenant 1, runtime Graph failure reaches SMTP, malformed email is terminal, transient transport errors remain retryable, and log scans are clean.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/email_service.go itsm-backend/service/email_service_test.go itsm-backend/service/ticket_notification_service.go itsm-backend/service/ticket_notification_delivery_worker_test.go itsm-backend/internal/bootstrap/app.go itsm-backend/internal/bootstrap/bpmn_callback_worker_test.go itsm-backend/service/ticket_workflow_service_test.go
git commit -m "fix(notification): enforce tenant-aware email delivery"
```

### Task 2: Authoritative Ticket-Notification Read API And UI Contract

**Files:**
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/controller/ticket_notification_controller.go`
- Test: `itsm-backend/controller/ticket_notification_controller_test.go`
- Modify: `itsm-frontend/src/lib/api/ticket-notification-api.ts`
- Modify: `itsm-frontend/src/lib/api/__tests__/ticket-notification-api.test.ts`
- Test: `itsm-frontend/src/components/business/__tests__/TicketNotificationSection.test.tsx`

**Interfaces:**
- Consumes: `TicketNotificationService.MarkNotificationRead(ctx, notificationID, userID, tenantID)` and `MarkAllNotificationsRead(ctx, userID, tenantID)` from the approved read/delivery-state fix.
- Produces: tenant/RBAC-protected `PUT /api/v1/ticket-notifications/:id/read` and `PUT /api/v1/ticket-notifications/read-all`; frontend methods use only these endpoints for `TicketNotification` records.

- [ ] **Step 1: Write failing backend route/controller and frontend API tests**

```go
func TestTicketNotificationReadRoutesUseTicketNotificationController(t *testing.T) {
    // Build router with distinct recording generic/ticket controllers.
    // Assert /ticket-notifications/:id/read reaches only the ticket controller.
}
```

```ts
it('marks a TicketNotification through its authoritative API', async () => {
  await TicketNotificationApi.markNotificationRead(5);
  expect(httpClient.put).toHaveBeenCalledWith('/api/v1/ticket-notifications/5/read', {});
});
```

- [ ] **Step 2: Run backend/frontend tests and verify RED**

Run: `cd itsm-backend && go test ./controller ./router -run 'TicketNotification.*Read' -count=1 -v`

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/ticket-notification-api.test.ts src/components/business/__tests__/TicketNotificationSection.test.tsx`

Expected: FAIL because dedicated routes do not exist and frontend calls generic `/notifications`.

- [ ] **Step 3: Wire the dedicated namespace and update frontend calls**

```go
ticketNotifications := tenant.(*gin.RouterGroup).Group("/ticket-notifications")
ticketNotifications.PUT("/:id/read", middleware.RequirePermission("notification", "update"), config.TicketNotificationController.MarkNotificationRead)
ticketNotifications.PUT("/read-all", middleware.RequirePermission("notification", "update"), config.TicketNotificationController.MarkAllNotificationsRead)
```

Keep generic `/notifications` routes for the separate generic model; do not alias one ID space to the other. Update only `TicketNotificationApi` read methods and component expectations.

- [ ] **Step 4: Run backend/frontend contract, type, and component tests**

Run: `cd itsm-backend && go test ./controller ./router ./service -run 'TicketNotification.*Read' -count=1`

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/ticket-notification-api.test.ts src/components/business/__tests__/TicketNotificationSection.test.tsx`

Run: `cd itsm-frontend && npm run type-check`

Expected: PASS; marking read updates `ticket_notifications.read_at` without changing delivery status, and the active UI calls the dedicated endpoint.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/router/router.go itsm-backend/controller/ticket_notification_controller.go itsm-backend/controller/ticket_notification_controller_test.go itsm-frontend/src/lib/api/ticket-notification-api.ts itsm-frontend/src/lib/api/__tests__/ticket-notification-api.test.ts itsm-frontend/src/components/business/__tests__/TicketNotificationSection.test.tsx
git commit -m "fix(notification): wire authoritative read contract"
```

### Task 3: Reject Invalid BPMN CC Channels Before Completion Mutation

**Files:**
- Modify: `itsm-backend/service/bpmn/cc_handler.go`
- Modify: `itsm-backend/service/bpmn/cc_handler_test.go`
- Modify: `itsm-backend/service/bpmn_callback_recovery_test.go`
- Test: `itsm-backend/service/bpmn_final_security_wave_test.go`

**Interfaces:**
- Consumes: `CallbackPayloadNormalizer.NormalizeCallbackPayload`, static `CallbackPayloadFields`, and `parseNotifyChannels(string) ([]string, error)`.
- Produces: `NormalizeCallbackPayload` validates channels and persists canonical comma-separated `notifyChannels` before task mutation; `Execute` retains defense-in-depth validation.

- [ ] **Step 1: Write failing process-level rollback tests**

```go
func TestCompleteTaskRejectsInvalidCCChannelBeforeMutation(t *testing.T) {
    // Complete a real CC user task with notifyChannels="in_app,emial".
    // Assert task/instance/audit/outbox snapshots are unchanged.
}

func TestCompleteTaskCanonicalizesValidCCChannelsBeforeEnqueue(t *testing.T) {
    // Input " email, in_app,email " persists exactly "email,in_app".
}
```

- [ ] **Step 2: Run process-level tests and verify RED**

Run: `cd itsm-backend && go test ./service ./service/bpmn -run 'InvalidCCChannel|CanonicalizesValidCCChannels|CallbackPayload' -count=1 -v`

Expected: FAIL because invalid channels currently survive normalization and are rejected only after durable enqueue.

- [ ] **Step 3: Validate and canonicalize inside the callback normalizer**

```go
channels, err := parseNotifyChannels(GetStringFromVars(variables, "notifyChannels"))
if err != nil { return nil, err }
payload["notifyChannels"] = strings.Join(channels, ",")
```

Perform this for every CC type before returning the normalized payload. Preserve omitted/empty input as canonical `in_app`, static allowlist validation, recipient resolution, and direct `Execute` validation.

- [ ] **Step 4: Run callback normal/race, real PostgreSQL, and whole-backend gates**

Run: `cd itsm-backend && go test ./service ./service/bpmn -run 'CC|Callback|Outbox|CounterSign' -count=1`

Run: `cd itsm-backend && go test -race -p 1 ./service ./service/bpmn -run 'CC|Callback|Outbox|CounterSign' -count=1`

Run: `cd itsm-backend && go test -race -tags integration -p 1 ./internal/bootstrap ./service -run 'Postgres.*(TicketCC|TicketNotification|Callback)|BPMNCallbackOutboxLeaseRecoveryPostgres' -count=1 -v`

Run: `cd itsm-backend && go test ./... -count=1 && go build ./...`

Expected: PASS without integration skips; invalid configuration leaves no mutation and all previously approved durability/concurrency behavior remains green.

- [ ] **Step 5: Run final repository gates and commit**

Run: `git diff fb0b2560..HEAD --check`

Run: added-log, credential-pattern, DTO/Ent JSON, and worktree-path scans described in the prior remediation reports.

```bash
git add itsm-backend/service/bpmn/cc_handler.go itsm-backend/service/bpmn/cc_handler_test.go itsm-backend/service/bpmn_callback_recovery_test.go itsm-backend/service/bpmn_final_security_wave_test.go
git commit -m "fix(bpmn): validate CC channels before enqueue"
```

## Final Verification

- Run full backend tests and build from `itsm-backend`.
- Run frontend API/component Jest tests, type-check, and production build from `itsm-frontend`.
- Run real PostgreSQL normal/race suites without skips.
- Run `git diff fb0b2560..HEAD --check`, sensitive-log, credential, DTO/JSON, and changed-path audits.
- Dispatch one whole-plan reviewer over the new plan base through `HEAD`; one consolidated fix dispatch and one scoped re-review are the breaker.
