# Task 4 Scoped Re-review

Range reviewed: `26e026a3..4ed14f84`  
Prior finding: [task-4-review.md](task-4-review.md)

## Verdict: NEEDS_CHANGES

## Confirmed

- The prior atomicity/correlation P1 is fixed for normal 4xx handling. `MarkRetryWithAudit` updates the active claim to `pending` and inserts the audit row in the same Ent transaction; a failed audit insert rolls the outbox update back.
- The rejection audit is event-correlated through `request_id = event.EventID` and does not set `request_body` (the audited tests assert `nil`).
- A successful later retry leaves the already-committed rejection audit in place. The new focused test covers the 401-to-202 sequence.

## Finding

### P1 - A KAF 4xx response can persist a quoted JSON credential in `last_error`

`dispatchEvent` appends the raw (up to 1 KiB) 4xx response body to
`deliveryError` before `MarkRetryWithAudit` persists it. The sole redactor,
`summarizeOutboxError`, only recognizes credential keys immediately followed by
`:` or `=`. It does not match standard JSON such as
`{"token":"webhook-secret"}` because the closing quote follows `token` before
the colon. A rejecting KAF endpoint can therefore cause `webhook-secret` to be
stored in `outbox_events.last_error`.

The audit row itself is payload-free, but this is still a persistent sensitive
data leak on the same 4xx error path. Do not persist response bodies for KAF
delivery failures, or apply a format-aware redactor that covers quoted JSON and
add an end-to-end 4xx regression test proving the secret is absent from
`last_error`.

References:

- `itsm-backend/service/kaf_outbox_dispatcher.go:104-117`
- `itsm-backend/service/outbox_event_repository.go:322-329`

## Verification

- `git diff --check 26e026a3..4ed14f84`
- `cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_|TestKafOutboxRetryDelayCapsAtFiveMinutes|TestOutboxEventRepository_|TestSummarizeOutboxError_|TestSignKafDelegateRequest)' -count=1 -v`
- `cd itsm-backend && go test ./config -run '^TestKafOutboxConfigFromEnvironment$' -count=1 -v`
- `cd itsm-backend && go test ./internal/bootstrap -run '^(TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation|TestServeUntilContextCancelledShutsDownServer)$' -count=1 -v`
- `cd itsm-backend && go build ./...`
