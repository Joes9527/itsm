# Task 4 P1 Fix Report

## Delivered

- 4xx KAF webhook delivery rejections now persist the outbox retry transition
  and the `kaf_outbox.delivery_rejected` audit record in one Ent transaction.
- The audit row uses the immutable outbox `event_id` as `request_id`, allowing
  operators to correlate a rejection back to the delivered event.
- Audit records deliberately contain no request body or delivery payload. Retry
  errors continue through the existing redaction and length-limiting path.
- If the audit insert fails, the transaction rolls back: the event remains
  claimed, with no retry count, error, or next-attempt update committed.
- A later successful delivery only publishes the event and does not remove the
  already committed rejection audit row.

## Regression Coverage

- `TestKafOutboxDispatcher_KeepsClientRejectionAuditAfterSuccessfulRetry`
  verifies a 401 audit remains queryable by `request_id` after a 202 retry.
- `TestKafOutboxDispatcher_RollsBackRetryWhenClientRejectionAuditFails`
  injects an audit persistence failure and verifies that retry state is rolled
  back with it.

## Verification

```text
cd itsm-backend && go test ./config -run '^TestKafOutboxConfigFromEnvironment$' -count=1 -v
cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_|TestKafOutboxRetryDelayCapsAtFiveMinutes|TestOutboxEventRepository_|TestSummarizeOutboxError_|TestSignKafDelegateRequest)' -count=1 -v
cd itsm-backend && go test ./internal/bootstrap -run '^(TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation|TestServeUntilContextCancelledShutsDownServer)$' -count=1 -v
cd itsm-backend && go build ./...
```

All commands completed successfully on 2026-08-30.
