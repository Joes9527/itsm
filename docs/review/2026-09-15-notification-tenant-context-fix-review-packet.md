# Review packet — notification tenant-context fix and GA runtime ACL broadening

- **Date**: 2026-09-15
- **Scope**: two changes on branch `codex/feat/config-launch-integration` plus one target-database
  privilege change. Implementation agent: codex (this session).
- **Status**: **awaiting independent review**. The running artifact is `d395dbd5…`, which is *not*
  the artifact covered by the previous review (`62aefd8d…`); the review gate now blocks switching or
  restarting through the coordinator tools until this packet is approved.

## 1. What is being reviewed

| # | Item | Location |
| --- | --- | --- |
| 1 | Notification controller now passes the request context to the persistence layer (8 call sites) | `itsm-backend/controller/notification_controller.go`, commit `f3844a19` |
| 2 | Regression test guarding the tenant requirement (driver-level) plus a cross-tenant negative case | `itsm-backend/controller/notification_controller_test.go`, commit `0ca6fb5c` |
| 3 | GA runtime ACL broadened from the ad-hoc narrowing to the product default, with explicit control-table exceptions | target database `itsm_ga_ready` (container `ga-itsm-20260914`), artifact `runtime-acl-ga.sql` |
| 4 | Review bound to the reviewed artifact hash; coordinator switch/restart tools fail closed on a mismatch | `admitted-launch.json`, `ga-backend-switch.py`, `ga-backend-restart.py` |

Diff: `git diff c9b887ab..0ca6fb5c` (160 insertions, 8 deletions, 2 files). Both commits are on the
integration branch only; `main` is untouched.

## 2. Why the code change is correct

`middleware/auth.go:129` places the authenticated tenant on the **request** context
(`tenantctx.WithTenantID`). `database/rls.AcquireConn` reads the tenant from that context and sets
`app.current_tenant`; with `RLS_MODE=enforce`, any statement without it fails closed with
`rls: no tenant_id in context and system bypass not set`.

`notification_controller.go` passed the bare `*gin.Context` to the notification service. A
`*gin.Context` is a `context.Context`, but it does not expose the request-context values, so the
tenant was invisible and every notification query failed:

```
GET /api/v1/notifications -> 500
{"code":5001,"message":"获取通知失败: 获取通知总数失败: rls: no tenant_id in context and system bypass not set"}
```

The fix passes `ctx.Request.Context()` for the eight persistence calls, matching the convention
already used by the incident, cmdb and connector controllers. The two calls that read gin keys on
purpose (`GetCurrentUserID`, `GetCurrentTenantID`) are unchanged.

The same RLS policy exists on the DEV database, so this was a pre-existing product defect rather
than a target-environment difference.

## 3. Evidence

**Regression test is genuinely red before the fix** (verified by temporarily restoring the
pre-fix controller from `HEAD~1`):

```
--- FAIL: TestNotificationController_ReadCarriesRequestTenantContext (0.07s)
  Error: Should be empty, but was [
    SELECT COUNT(`notifications`.`id`) FROM `notifications` WHERE ... AND `notifications`.`tenant_id` = ?,
    SELECT `notifications`.`id`, ... FROM `notifications` WHERE ... LIMIT 20]
  Messages: notification statement reached the driver without tenant context
```

With the fix in place the same command reports `ok`.

**Package result is otherwise unchanged**: `go test ./controller` reports the same two pre-existing
baseline failures with and without the change — `TestGetBPMNTenantContextRejectsRequestSelectedTenantForTenantlessJWT`
and `TestTicketNotificationController_QueuedReturnsAccepted`. Static checks: `gofmt` clean,
`go vet ./controller` clean, `go build ./controller ./service` ok, `git diff --check` clean.

**Runtime, through the running environment (backend 8080, web 3010, database `itsm_ga_ready`)**:

| Check | Result |
| --- | --- |
| `GET /api/v1/notifications` | 200, returns the notifications produced by the earlier end-to-end run |
| `GET /api/v1/notifications/unread-count` | 200 |
| `PUT /api/v1/notifications/13/read` | 200 `标记已读成功`; unread count 7 → 6, persisted |
| generic ticket flow (create, idempotent replay, detail/refresh, comment, assign, status) | passed with zero API failures |

**ACL change**, measured before and after:

| | before | after |
| --- | --- | --- |
| tables writable by `ga_runtime` | 20 / 159 | 152 / 159 |
| tables readable | 55 | 155 |
| sequences usable | 20 / 137 | 137 / 137 |
| RLS policies / tables with row security | 129 / 129 | 129 / 129 (unchanged) |

Verified with the real `ga_runtime` credential in a transaction that was rolled back: notification
UPDATE and INSERT, `users` UPDATE (core4), `ticket_types` UPDATE and a sequence `nextval` all
succeed; with `app.current_tenant` pointing elsewhere the same UPDATE matched **zero** rows. The
deliberate controls still fail closed — `execution_runtime_bindings` UPDATE returns
`permission denied`, `kaf_task_action_ledgers` SELECT returns `permission denied`.
`schema_migrations`, `work_item_migration_evidence` and `auth_state_authorities` keep no privileges.

**Artifact identity**: `itsm-api-ga-workbench` sha256 `d395dbd5a03739d48cde6fe7898ec45d7daadb19686ac265f5bf6e70239a58ef`,
built from this worktree with `CGO_ENABLED=1 go build .`. The previous artifact
(`62aefd8d0c3258158b07001b8cf5e9d9a13c159b2c7c85858a2b36a238045db6`) is kept at
`itsm-api-ga-workbench-prev-20260915-1127`.

## 4. Decisions this change depends on

The ACL broadening is **not** a mechanical fix; it encodes product decisions that were taken
explicitly on 2026-09-15 and should be reviewed as such:

1. The switch-time least-privilege narrowing is abandoned in favour of the product's own role model
   (`itsm-backend/database/rls/migrations/001_roles.sql:48-58`). Reason: the narrowing silently
   broke product write paths — persisting the connector config failed with a warn-level log while
   the API still returned 200, and ticket creation then failed with `403 notification target is not
   permitted`.
2. `core4` (`users`, `roles`, `permissions`, `user_roles`) is included, which explicitly relaxes the
   earlier "do not change core4" constraint. Only privileges changed; schema, data and policies did
   not.
3. Seven cross-role controls stay closed: `execution_runtime_bindings`, `execution_scopes`,
   `execution_scope_members` (read-only) and `auth_state_authorities`,
   `kaf_task_action_ledgers`, `work_item_migration_evidence`, `schema_migrations` (no access).

## 5. Unverified or open items

- **No HTTP-level test for the tenant-isolation negative case**. The cross-tenant assertion is at
  the controller/driver level on sqlite; Postgres RLS enforcement for this table is covered by its
  own policy but not re-tested here.
- **Class-level guard is missing.** The sweep of sibling controllers found no other live defect
  (`simple_notification_controller.go` is unreferenced dead code; `auth_controller.go` calls are
  pre-auth/session flows with no tenant by design; the BPMN AI generator service performs no ent
  persistence), but nothing prevents a new controller from repeating this mistake. A lint or CI
  check for service calls receiving a bare `gin.Context` would need its own governance decision.
- **Acknowledge/close lifecycle paths were not re-tested** after the ACL change.
- The `pg_policies` before/after comparison captured only the first line of multi-line policy
  expressions; policy counts matched (129) and the executed statements were grants and revokes only,
  but the comparison is not a full policy-text digest.
- Earlier session evidence (generic flow, session lifecycle 8/8) was captured on the previous GA
  stack before the environment was restarted by the operator at 12:15; the notification and ACL
  checks in this packet were re-run afterwards on the current environment.

## 6. Reviewer checklist

1. Confirm the fix matches the failure mechanism described in section 2 and that no other
   notification path still receives a bare `gin.Context`.
2. Confirm the regression test fails when the controller change is reverted (command in section 3).
3. Confirm the ACL decision matches what the operator authorised, and that the seven control tables
   are still closed.
4. Confirm the open items in section 5 are acceptable for this stage, or list what must be closed
   first.

**To record approval** (clears the gate for this artifact):

```bash
python3 /home/administrator/bind-review-to-artifact.py approve --reviewer "<name>" --note "<summary>"
```

Until then the artifact stays blocked: switching or restarting through the coordinator tools is
refused with `{"review_gate": "blocked"}` before any process is touched.

## 7. Review-ready snapshot (refreshed 2026-09-15 16:05 CST)

| Item | Value |
| --- | --- |
| Serving process | pid 1896647 on port 8080, `/proc/1896647/exe` sha256 = `d395dbd5a03739d48cde6fe7898ec45d7daadb19686ac265f5bf6e70239a58ef` |
| Process cwd | `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/itsm-wsl-development` |
| Manifest artifact hash | `d395dbd5…` (matches the serving process) |
| Previously reviewed artifact | `62aefd8d0c3258158b07001b8cf5e9d9a13c159b2c7c85858a2b36a238045db6` |
| Review gate | blocked: `reviewed_binary_sha256` != artifact hash, 0 review records, 0 acceptances |
| Regression test re-run | `go test ./controller -run 'TestNotificationController_' -count=1` → `ok` |
| Runtime state | backend 8080, web 3010, database `itsm_ga_ready` on the clone; ports 3000/3001 unused |

The artifact under review is therefore the one currently serving traffic, and the packet's evidence is
reproducible on demand. The review itself must be performed by someone other than the implementing
agent; see section 6 for the checklist and the approval command.
