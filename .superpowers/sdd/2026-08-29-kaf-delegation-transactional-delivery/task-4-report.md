# Task 4 Report: KAF Outbox Dispatcher and Runtime Configuration

## Status

Implemented and verified.

## Changes

- Added fail-closed `KAF_WEBHOOK_*` runtime configuration with disabled-by-default delivery, required secret validation, HTTP(S)-only endpoint validation, batch limit validation, and poll interval validation.
- Added `KafOutboxDispatcher` with canonical payload-byte HMAC signing, `X-Event-ID`, ten-second HTTP timeout, 2xx publish completion, and capped exponential retry for transport, 4xx, and 5xx failures.
- Added a payload-free, tenant-scoped audit record for 4xx delivery rejections. Error persistence remains subject to the existing sensitive-value redaction and size limit; dispatcher logs contain neither secrets nor event payloads.
- Added type-scoped outbox claims so the KAF dispatcher cannot claim, recover, or send events belonging to another integration.
- Wired a single dispatcher into the application lifecycle. An empty URL emits one warning and starts no worker; application cancellation gracefully shuts down HTTP serving and waits for the dispatcher before database shutdown.

## Verification

- RED: configuration tests initially failed because KAF configuration parsing and validation did not exist; invalid endpoint tests later failed until HTTP(S)-only and no-userinfo checks were added.
- RED: dispatcher tests initially failed because the dispatcher API was absent; transport, 4xx, 5xx, retry-cap, lifecycle, and type-isolation tests each exposed the missing state transition or lifecycle behavior before implementation.
- `cd itsm-backend && go test ./config -run '^TestKafOutboxConfigFromEnvironment$' -count=1 -v` passed.
- `cd itsm-backend && go test ./service -run '^(TestKafOutboxDispatcher_|TestKafOutboxRetryDelayCapsAtFiveMinutes|TestOutboxEventRepository_|TestSummarizeOutboxError_|TestSignKafDelegateRequest)' -count=1 -v` passed.
- `cd itsm-backend && go test ./internal/bootstrap -run '^(TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation|TestServeUntilContextCancelledShutsDownServer)$' -count=1 -v` passed.
- `cd itsm-backend && go build ./...` passed.
- `git diff --check` passed.

## Scope Note

The brief permits wiring the worker in the existing application bootstrap instead of `router/router.go`. Bootstrap owns the process context and shutdown ordering, while route registration remains free of background-worker side effects.
