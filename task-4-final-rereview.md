# Task 4 Final Re-review

Range reviewed: `4ed14f84..681887c0`  
Scope: Task 4 JSON/escaped credential redaction follow-up only.

## Verdict: APPROVED

The JSON redaction fix closes the prior persistence leak. Both ordinary retries
and 4xx retries with audit pass the delivery error through
`summarizeOutboxError` immediately before writing `outbox_events.last_error`.
The redactor normalizes string-escaped quotes, then removes quoted JSON values
for the established common credential keys. The existing key/value and URL
userinfo redaction still runs afterwards.

No KAF response body is written to the rejection audit record: the atomic audit
write contains only tenant, event correlation, fixed delivery metadata, and the
HTTP status. This keeps direct and escaped JSON credential values out of audit
fields as well.

Sanitized retry diagnostics are retained. The focused 401 integration test
asserts that `last_error` retains the rejection detail while excluding the JSON
token, and that the correlated audit entry contains neither the secret nor a
request body. Unit coverage exercises direct JSON `token`, `access_token`, and
`client-secret` values, plus string-escaped `api-key` and `password` values.

## Verification

- `git diff --check 4ed14f84..681887c0`
- `cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_(RetriesNon2xxWithoutDroppingEvent|RedactsJSONCredentialsFromClientRejection|KeepsClientRejectionAuditAfterSuccessfulRetry|RollsBackRetryWhenClientRejectionAuditFails)|TestSummarizeOutboxError_)' -count=1 -v`

Both checks completed successfully.
