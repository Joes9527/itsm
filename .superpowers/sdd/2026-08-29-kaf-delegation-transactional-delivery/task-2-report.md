# Task 2 Report: Tenant-Scoped OutboxEvent Persistence

## Status

Completed after review and rereview fixes.

## Commit

- `2db8f3ec feat(outbox): persist tenant-scoped delegation events`

## Files

- Added `itsm-backend/ent/schema/outbox_event.go`.
- Regenerated `itsm-backend/ent/` for the `OutboxEvent` client, mutation, predicate, query, migration schema, hooks, runtime, and transaction support.
- Added `itsm-backend/service/outbox_event_repository.go`.
- Added `itsm-backend/service/outbox_event_repository_test.go`.

## Review Fixes

- Made `tenant_id` immutable and regenerated Ent so normal update builders cannot move an event between tenants.
- Added a five-minute publishing lease with an opaque claim token. `ClaimDue` atomically recovers expired or legacy unleased publishing rows before claiming; `MarkRetry` and `MarkPublished` require the matching unexpired token.
- Marked payload, claim token, and last error sensitive. Retry errors are whitespace-normalized, credential-value-redacted, and truncated to 512 bytes before persistence.
- Added regression coverage for caller-transaction rollback, tenant immutability and scoped reads, concurrent claims, stale-lease recovery, stale retry/publish rejection, and generated entity redaction.

## Re-Review Fixes

- Expanded retry-error sanitization to redact case-insensitive `access_token` and `client_secret` spellings with underscore, hyphen, or space separators, as well as URL userinfo credentials.
- Added table-driven regression coverage for the newly redacted credential formats.

## Verification

- `cd itsm-backend && go generate ./ent` exited 0.
- `cd itsm-backend && go test ./service -run 'TestOutboxEventRepository_' -count=1 -v` exited 0: 9 tests passed.
- `cd itsm-backend && go build ./...` exited 0.
- `git diff --check` exited 0 before commit.
- Review red/green evidence: the new regression suite first failed because the generated model lacked lease fields and completion methods lacked a claim token; the concurrent claim test then exposed SQLite's transient write lock and passed after `ClaimDue` added bounded retry handling.
- Re-review red/green evidence: credential-spelling regression cases first failed because the retry-error sanitizer did not match underscore key separators or URL userinfo; they passed after expanding the sanitizer patterns.
- Re-review verification: `cd itsm-backend && go test ./service -run 'TestOutboxEventRepository_|TestSummarizeOutboxError_' -count=1 -v` exited 0: 10 tests passed.
- Re-review verification: `cd itsm-backend && go build ./...` exited 0.

## Deviations

- No functional deviations from Task 2.
- `MarkPublished` and `MarkRetry` now require the `claimToken` returned by `ClaimDue`; a dispatcher must retain it through completion.

## Concerns

- Task 2 follows the plan's Ent generation convention and does not add a versioned operational migration. Deployment must use the repository's established Ent schema migration path.
