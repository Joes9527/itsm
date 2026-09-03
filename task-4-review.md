# Task 4 Review

Range reviewed: `1dddd6e9..26e026a3`  
Brief: `docs/superpowers/plans/2026-08-29-kaf-delegation-transactional-delivery.md`, Task 4

## Verdict: NEEDS_CHANGES

## Findings

### P1 - A 4xx delivery rejection is not durably attributable in the audit trail

`dispatchEvent` first commits the retry transition through `MarkRetry`, then separately inserts the audit record. If that second write fails, the outbox event is already pending and a subsequent 2xx delivery can publish it without any durable record that the 4xx occurred. This violates Task 4's requirement that every 4xx create an error audit record for operator investigation.

The successful audit row is also not attributable to a specific delivery: `recordKafWebhookClientError` records only the tenant, generic `outbox_event` resource, action, path, method, and HTTP status. It leaves the existing `request_id` field empty and does not otherwise retain the event ID, task ID, or correlation ID. Multiple rejected events in one tenant cannot be correlated from the audit log back to the outbox record.

References:

- `itsm-backend/service/kaf_outbox_dispatcher.go:116-123` commits the retry before attempting the audit write.
- `itsm-backend/service/kaf_outbox_dispatcher.go:140-150` creates an uncorrelated audit row.
- `itsm-backend/service/kaf_outbox_dispatcher_test.go:145-150` asserts tenant and status only, so it does not protect the required correlation or the audit-write-failure path.

Recommended correction: persist the retry state and a payload-free, event-correlated audit record in one transaction (or use an equivalent durable audit outbox), set `request_id` to `event.EventID` or another stable delivery correlation, and add regression coverage for audit-write failure plus correlation.

## Checks Completed

- Config defaults and validation: disabled default, required secret, URL scheme/userinfo, batch range, and polling lower bound.
- Bootstrap lifecycle: disabled worker, one startup call, context cancellation, and wait before database shutdown.
- Webhook contract: exact persisted payload bytes are signed with HMAC-SHA256; `Content-Type`, `X-Webhook-Signature`, and `X-Event-ID` are set.
- Delivery semantics: any 2xx publishes; transport errors, 4xx, and 5xx retry with capped exponential backoff; KAF event type filtering prevents other integrations from being claimed.
- Lease and tenant boundaries: claim token is required for retry/publish; stale leases are rejected; rejection audits use the event tenant.
- Sensitive-data handling: the dispatcher does not log the webhook secret or event payload; retry error persistence is length-limited and redacts tested credential forms.

## Verification

- `cd itsm-backend && go test ./config -run '^TestKafOutboxConfigFromEnvironment$' -count=1 -v`
- `cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_|TestKafOutboxRetryDelayCapsAtFiveMinutes|TestOutboxEventRepository_|TestSummarizeOutboxError_|TestSignKafDelegateRequest)' -count=1 -v`
- `cd itsm-backend && go test ./internal/bootstrap -run '^(TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation|TestServeUntilContextCancelledShutsDownServer)$' -count=1 -v`
- `cd itsm-backend && go build ./...`
- `git diff --check 1dddd6e9..26e026a3`
