# Final Fix Report: Integration Blocker Final Review

## Scope

Implemented the single consolidated final-review fix wave from base
`6af11248`:

- `TicketNotificationSection` now derives unread count, item styling,
  unread badges, icons, and mark-read actions only from `readAt`.
- The active `components/ticket/TicketDetail` used by
  `/tickets/[ticketId]` now exposes a lazy `TicketNotificationSection`
  tab when `notification:read` is granted. The send control is independently
  derived from `notification:create`.
- Dedicated `/api/v1/ticket-notifications/:id/read` and
  `/api/v1/ticket-notifications/read-all` routes now require
  `notification:read`. Existing service predicates continue to constrain
  updates by authenticated user and tenant.
- Generic `/api/v1/notifications` read routes and frontend methods remain
  unchanged.

Task 1 tenant-aware email delivery files and Task 3 callback/channel files
were not modified.

## TDD Evidence

The component RED run failed because a row with `readAt` still rendered an
unread action when its delivery status was `pending`, and a persisted
`readAt` reload after mark-read still rendered unread while status remained
`sent`. The focused component suite passed after replacing every read-state
branch with `readAt`.

The production-mount RED run failed because `TicketDetail` had no
`工单通知` tab. After the minimal mount, tests proved that the notification
API is not called before tab activation, read permission controls tab
visibility, and create permission controls only the send action.

The router RED run produced 403 for read-only `end_user` and `security`
roles, while an update-only role received 200 on both dedicated routes. After
the middleware change, both default roles succeeded with only
`notification:read`, the update-only role received 403, `readAt` persisted,
and delivery status remained `sent`.

## Verification

All required commands exited 0:

```text
go test ./service ./controller ./router -run 'TicketNotification.*Read|TicketNotificationReadRoutes' -count=1
go test -race -p 1 ./router -run 'TestTicketNotificationReadRoutes' -count=1
go test ./... -count=1
go build ./...

JEST_JUNIT_OUTPUT_DIR=<temporary-directory> npm test -- --runInBand --coverage=false \
  src/lib/api/__tests__/ticket-notification-api.test.ts \
  src/components/business/__tests__/TicketNotificationSection.test.tsx \
  src/components/ticket/__tests__/TicketDetail.test.tsx
npm run type-check
npx eslint <changed frontend source and test files>
npm run lint:check
npm run build

git diff 6af11248 --check
```

Focused Jest executed 21 assertions with no failures. Full frontend lint
reported only three pre-existing unused-disable warnings outside this change.
The production build completed all 133 static pages and prepared the standalone
runtime.

Added-production-line scans found no credential patterns or new logging.
Changed-path scans found no DTO/Ent files, Task 1 email files, or Task 3
callback files. The tracked JUnit artifact was restored after Jest execution.

## Review

A scoped review of the complete final-fix diff found no remaining Critical or
Important findings. Subagent review was unavailable in this harness, so the
review was performed directly against the plan, latest ledger rulings, final
review findings, architecture spec, and repository constraints.
