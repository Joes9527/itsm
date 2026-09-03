# Task 8 Report

## Scope

- Implemented the Task 8 authoritative ownership change in the intake handler surface only.
- Changed `Service.writeFieldValues` so dynamic form values for service request intake always persist against the authoritative WorkItem record using `entity_type=ticket` and `entity_id=<work_item_id>`.
- Changed `ServiceRequestItemCreator.CreateExtension` so the service request extension writes schema-required `tenant_id` and `requester_id` from the authoritative WorkItem fields.
- Added TDD coverage for both requirements before production edits.
- Kept field ownership single-source: no dual write to `service_request` field value ownership and no duplicate dynamic value storage in the extension.

## Files

- `itsm-backend/handlers/intake/service.go`
- `itsm-backend/handlers/intake/service_request_creator.go`
- `itsm-backend/handlers/intake/service_test.go`
- `itsm-backend/handlers/intake/creator_test.go`

## Red Evidence

### 1. Field value ownership test failed before fix

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest -v
```

Observed failure:

```text
expected: "ticket"
actual  : "service_request"
```

### 2. Service request extension owner-field test failed before fix

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestServiceRequestItemCreatorCreatesExactlyOneExtension -v
```

Observed failure:

```text
Received unexpected error:
could not create service request extension
```

This failure was caused by the schema-required `tenant_id` and `requester_id` not being set during `CreateExtension`.

## Green Evidence

### Focused tests after minimal implementation change

Commands:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestServiceRequestItemCreatorCreatesExactlyOneExtension -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestWriteFieldValues|TestServiceCreateCommitsOneAuthoritativeGraphAndReplays|TestServiceRequestItemCreatorCreatesExactlyOneExtension' -v
```

Observed result:

```text
PASS
ok      itsm-backend/handlers/intake
```

### Final full handlers/intake suite

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -v
```

Observed result:

```text
PASS
ok      itsm-backend/handlers/intake    0.939s
```

## Exact Commands

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestServiceRequestItemCreatorCreatesExactlyOneExtension -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && gofmt -w handlers/intake/service.go handlers/intake/service_request_creator.go handlers/intake/service_test.go handlers/intake/creator_test.go
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestWriteFieldValues|TestServiceCreateCommitsOneAuthoritativeGraphAndReplays|TestServiceRequestItemCreatorCreatesExactlyOneExtension' -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -v
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation && git diff -- itsm-backend/handlers/intake/service.go itsm-backend/handlers/intake/service_request_creator.go itsm-backend/handlers/intake/service_test.go itsm-backend/handlers/intake/creator_test.go
```

## SHA

- Base SHA: `02c3f080fc4deedfbf045644d64620b2d32610dd`
- Task 8 implementation commit SHA: `c303a79923ccd7bd6e9d7f1ae86ef4d38ca75305`

## Remaining Concerns

- Verification was intentionally scoped to the changed intake package and the focused task-surface regressions, matching the task request.
- No broader backend suite was run beyond `./handlers/intake`; any wider integration coverage remains unchanged by this task.