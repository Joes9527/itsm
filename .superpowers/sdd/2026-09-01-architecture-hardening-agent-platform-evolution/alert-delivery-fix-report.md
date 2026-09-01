# Final review finding 4 — Incident alert durable delivery report

## Scope and baseline

- Worktree: `.worktrees/p1-final-alert-delivery`
- Branch: `fix/p1-final-alert-delivery`
- Baseline: `28e5c6dafd10b82e94fc6db1183756e35811447e`
- Constraint: reuse the authoritative `outbox_events` boundary; no `AlertOutbox`, no callback/KAF table coupling, no synchronous delivery fallback.
- Database: no schema shape change and no migration. Existing `status`, `attempt_count`, `next_attempt_at`, lease/token, and `last_error` fields express `blocked` and `dead_letter`; final migration order remains unchanged and no `023` is introduced.

## Verified root cause

`IncidentAlertingService.CreateIncidentAlert` committed the alert before delivery and then called `sendAlertNotifications` synchronously with a five-second request budget. The method returned no error. Disabled channels, explicit production-only unsupported SMS/Slack/Webhook behavior, and provider errors were logged and discarded, so HTTP success could mean delivered, skipped, unsupported, or failed. In-app notification writes were also separate and their errors were discarded.

The repository already had one suitable durable boundary:

- `OutboxEventRepository.Enqueue` can participate in the caller transaction.
- `event_id` is unique.
- claims use a lease token and expiry recovery.
- retry/publish transitions require the active claim token.
- event-type filtering prevents the existing KAF dispatcher from claiming alert deliveries.

It lacked a shared handler registry/runner and terminal `blocked`/`dead_letter` transitions. Those capabilities were added to the existing boundary rather than creating an alert-specific outbox.

## Resulting architecture

1. `CreateIncidentAlert` validates channels before persistence. Only `email` and `in_app` are accepted; SMS, Slack, Webhook, unknown, duplicate channels, and invalid email destinations fail closed.
2. Incident alert, in-app notification projection, one `outbox_events` row per email recipient, and `incident_alert.delivery_accepted` audit evidence commit in one Ent transaction. Any enqueue/audit/write error rolls everything back.
3. The outbox payload captures tenant, alert, destination, payload version, event ID, actor, source, and correlation ID. The controller supplies authenticated user/request identity; automation defaults explicitly to `system`.
4. `OutboxDeliveryWorker` owns claim, handler registry dispatch, bounded external-call timeout, retry, blocked, dead-letter, publish, restart recovery, and claim-token fencing. API success means durable acceptance only.
5. `IncidentAlertDeliveryHandler` reuses the existing tenant-aware `EmailService.SendForTenant`; it does not implement a second SMTP/Graph route. The worker is wired to application signal lifecycle and is awaited during shutdown.
6. The old `AlertChannel`, Email/SMS/Slack/Webhook channel classes, configuration switch, simulation path, and `sendAlertNotifications` were deleted.
7. The existing `EmailService` SMTP transport now consumes the worker context through a context-aware dial/connection deadline and preserves STARTTLS. This makes the configured five-second handler timeout real instead of advisory.

## TDD evidence

The following tests were observed failing before their corresponding implementation:

- durable alert creation: `outbox_event not found`;
- unsupported SMS: expected an error, received `nil`;
- enqueue failure: expected rollback error, received `nil` and alert persisted;
- worker registry: undefined constructor/config/handler symbols;
- bootstrap lifecycle: missing application worker field/start method;
- actor/audit: `audit_log not found`;
- one-row-per-destination: expected two deliveries, found one;
- handler timeout lease bound: five-minute timeout was accepted;
- SMTP context transport: context-aware SMTP seam did not compile against the old non-context signature.

Green coverage now verifies:

- transaction acceptance and rollback;
- unsupported/invalid channel rejection;
- one independent delivery lifecycle per recipient;
- retry then exactly-one published delivery;
- expired-lease reclaim after simulated restart;
- stale claim rejection for retry/publish/blocked/dead-letter;
- unknown payload channel becomes `blocked`;
- exhausted transient failure becomes `dead_letter`;
- external call obeys handler timeout;
- actor/source/correlation and audit persistence;
- application starts one worker and waits for cancellation;
- SMTP deadline, Graph-to-SMTP fallback, and legacy SMTP callers.

## Verification evidence

- `go test ./... -count=1` — exit `0`, all backend packages passed after the final SMTP transport change.
- `go build ./...` — exit `0`.
- `go test -race ./service ./controller ./internal/bootstrap -run 'Test(EmailServiceSMTPTransportHonorsContextDeadline|IncidentAlert|OutboxDeliveryWorker|OutboxEventRepository_RejectsStaleLeaseCompletion|Application_StartOutboxDeliveryWorker)' -count=1` — exit `0`; service/controller/bootstrap passed.
- `git diff --check` — exit `0`.
- deletion scan for `sendAlertNotifications`, `getAlertChannels`, legacy alert channel types, `ENABLE_EMAIL_SENDING`, and `alerting.smtp/sms/slack/webhook` — zero matches in Go code.
- coupling scan of new incident alert/outbox worker files for `ProcessCallbackOutbox` and `KafTaskActionLedger` — zero matches.
- `go vet ./service ./controller ./config ./internal/bootstrap` — exit `1` only for two unchanged pre-existing copylock warnings:
  - `service/bpmn_callback_outbox_schema_test.go:249`
  - `internal/bootstrap/ticket_cc_index_migration_test.go:354`
  Neither file is modified by this branch; controller/config report no new vet finding.

## Configuration

The shared worker is configured through:

- `OUTBOX_DELIVERY_BATCH_SIZE` (default `20`)
- `OUTBOX_DELIVERY_POLL_INTERVAL` (default `5s`)
- `OUTBOX_DELIVERY_HANDLER_TIMEOUT` (default `5s`, must remain shorter than the five-minute lease)
- `OUTBOX_DELIVERY_MAX_ATTEMPTS` (default `5`)

These are documented in `docs/configuration.md`. No apply/reset/verify script was added or changed because the existing schema is sufficient.

## Independent-review remediation

The follow-up review found four contracts that the first implementation had not fully closed. They are now resolved without another outbox or migration:

1. The duplicate `IncidentService.CreateIncidentAlert` writer was deleted. `IncidentService`, incident rule actions, and escalation processing depend on the single `IncidentAlertCreator` port implemented by `IncidentAlertingService`; bootstrap injects that authority explicitly. The former escalation log-only notification path was removed.
2. `OutboxEventTypeRegistry` is now the authoritative registry for the shared table. It contains generic handlers and reserves `kaf_delegate_requested` for the specialised KAF dispatcher. Due unknown event types are atomically moved from `pending` to `blocked` with audit evidence; KAF events are neither claimed nor blocked by the generic worker.
3. The worker writes a durable delivery-attempt marker before entering an external handler. If the process exits after a possible side effect but before `published`, lease recovery moves the event to observable `delivery_unknown`/`blocked` with audit evidence and does not resend. Graph and SMTP receive the stable outbox event ID (`X-ITSM-Delivery-ID`; SMTP also receives `Message-ID`). Durable Graph delivery disables Graph-to-SMTP fallback after an ambiguous Graph result. This is deliberately conservative at-least-once/ambiguous handling, not a claim of SMTP exactly-once delivery.
4. Incident alert capabilities are registry-driven (`email`, `in_app`). Incident alert requests, automation notification actions, escalation-rule create/update, and SLA alert-rule create/update reject unsupported capabilities before persistence/execution. Built-in alert defaults were converged to registered email delivery.

Additional green evidence:

- unknown event blocked + audited while reserved KAF remains pending;
- simulated crash-after-send/lease-expiry becomes `delivery_unknown` and the restarted worker performs zero sends;
- Graph and SMTP both carry the stable delivery marker;
- durable Graph ambiguity performs no SMTP fallback;
- unsupported rule channels fail before persistence;
- focused race suite for repository/worker/email/channel contracts passes.
