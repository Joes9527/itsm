# Task 4 P1 Fix Report

## Delivered

- 4xx KAF webhook delivery rejections now persist the outbox retry transition
  and the `kaf_outbox.delivery_rejected` audit record in one Ent transaction.
- The audit row uses the immutable outbox `event_id` as `request_id`, allowing
  operators to correlate a rejection back to the delivered event.
- Audit records deliberately contain no request body or delivery payload. Retry
  errors continue through the existing redaction and length-limiting path.
- Retry-error sanitization now redacts quoted credential values in direct and
  string-escaped JSON response details before they can reach `last_error`.
  It retains the HTTP status and non-sensitive diagnostic fields, and the
  rejection audit remains payload-free.
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
- `TestKafOutboxDispatcher_RedactsJSONCredentialsFromClientRejection` verifies
  a 401 JSON response secret reaches neither persisted `last_error` nor audit
  data while retaining the rejection detail.
- `TestSummarizeOutboxError_RedactsQuotedJSONCredentialValues` covers common
  quoted JSON credential keys and their string-escaped variants.

## Verification

```text
cd itsm-backend && go test ./config -run '^TestKafOutboxConfigFromEnvironment$' -count=1 -v
cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_|TestKafOutboxRetryDelayCapsAtFiveMinutes|TestOutboxEventRepository_|TestSummarizeOutboxError_|TestSignKafDelegateRequest)' -count=1 -v
cd itsm-backend && go test ./internal/bootstrap -run '^(TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation|TestServeUntilContextCancelledShutsDownServer)$' -count=1 -v
cd itsm-backend && go build ./...
```

All commands completed successfully on 2026-08-30.
