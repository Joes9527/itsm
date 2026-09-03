# Task 9 Report

## Scope

Implemented Task 9 for unified intake change-request support in `/home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation`.

Base provided by user: `a2ebb205fa398590ab6ac630f0a3fa2e48c2140f`

Final commit:

- `7c503fcf71f6337bdd12dea4a8d92ec43b8222a5` - `feat(intake): add change request creator`

## Files Changed

- `itsm-backend/handlers/intake/change_creator.go`
- `itsm-backend/handlers/intake/change_creator_test.go`
- `itsm-backend/handlers/intake/command.go`
- `itsm-backend/handlers/intake/canonicalize.go`
- `itsm-backend/handlers/intake/canonicalize_test.go`
- `itsm-backend/handlers/intake/resolver.go`
- `itsm-backend/handlers/intake/resolver_test.go`
- `itsm-backend/handlers/intake/service_test.go`
- `itsm-backend/handlers/intake/postgres_integration_test.go`
- `itsm-backend/service/bpmn_process_binding_service.go`

## What Changed

### Intake command and canonicalization

- Added `ChangeInput` to `CreateWorkItemCommand`.
- Added change payload participation to `CanonicalizeCommand` and its digest.
- Normalized and sorted `ChangeInput.AffectedCIs` so list order does not create false idempotency conflicts.

### Change creator

- Added `ChangeCreator` implementing `ProfessionalCreator`.
- `Prepare` parses optional RFC3339 planned start/end timestamps and builds a `ChangeExtensionPlan`.
- `CreateExtension` persists the real `Change` professional extension against the authoritative `tickets` WorkItem created by `WorkItemCreator`.
- No extension-level number allocation was introduced, consistent with `ent/schema/change.go`.

### Resolver and workflow binding support

- Widened intake catalog target-class acceptance to include `change_request`.
- Corrected target permission routing so `change_request` resolves to `change:write`, not `incident:write`.
- Added workflow binding support in `service/bpmn_process_binding_service.go` by mapping `change_request` to the existing `change` business subtype.

### Registry wiring present in this worktree

- Registered `NewChangeCreator()` in the intake service test harness and the postgres intake integration harness.
- No live `internal/bootstrap/app.go` intake assembly existed in this worktree, so there was no runtime bootstrap callsite to update there.

## TDD Evidence

### Red

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestChangeCreatorCreatesRealChangeExtension -v
```

Result:

- `FAIL`
- Compile-time red state from missing Task 9 API surface:
  - `unknown field Change in struct literal of type CreateWorkItemCommand`
  - `undefined: ChangeInput`
  - `undefined: NewChangeCreator`

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run TestCanonicalizeCommandDigestChangesWithChangeFieldSet -v
```

Result:

- `FAIL`
- Same compile-time red state, confirming change input was absent from the command/canonicalization surface.

### Intermediate focused validation

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestChangeCreator|TestCanonicalizeCommand|TestResolverResolvesDirectAndCatalogTargets|TestResolverRejectsHiddenCatalogInvalidFormAndPermissionDenial' -v
```

First result after initial implementation:

- `FAIL`
- Newly added resolver coverage exposed remaining workflow-binding gap:
  - `TestResolverResolvesDirectAndCatalogTargets/change_catalog`
  - error: `record class is unsupported`

Fix applied:

- Added `change_request -> change` mapping in `service/bpmn_process_binding_service.go`.

### Green

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestChangeCreator|TestCanonicalizeCommand|TestResolverResolvesDirectAndCatalogTargets|TestResolverRejectsHiddenCatalogInvalidFormAndPermissionDenial' -v
```

Result:

- `PASS`
- Verified:
  - `TestChangeCreatorCreatesRealChangeExtension`
  - `TestCanonicalizeCommandDigestChangesWithChangeFieldSet`
  - `TestResolverResolvesDirectAndCatalogTargets` including `change_catalog`
  - `TestResolverRejectsHiddenCatalogInvalidFormAndPermissionDenial` including `change target permission`

### Required broader verification

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go build ./...
```

Result:

- Exit code `0`
- No build errors emitted.

## Diff Inspection

Reviewed scoped diff after verification with:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation && git diff -- itsm-backend/handlers/intake itsm-backend/service/bpmn_process_binding_service.go
```

Outcome:

- Diff stayed within Task 9’s intake change-request surface plus the one-hop workflow binding mapping fix that the new resolver test exposed.

## Concerns

- `handlers/intake/service.go` replay loading still only knows how to hydrate `incident` and `service_request_item` professional references. Task 9 did not specify extending replay/load-result behavior, so I left that surface unchanged.
- `handlers/intake/metrics.go` still bounds metric record classes to incident and service request only. Functional intake creation now supports `change_request`, but metrics will classify it as unknown until a later task widens that bound.
- The report file itself was written after the verified code commit so the commit SHA above refers to the implementation commit, not this report artifact.

## Fix Round 1

Review findings addressed from base `7c503fcf71f6337bdd12dea4a8d92ec43b8222a5`:

- Added a full `Service.Create` create-then-replay regression for `change_request` idempotency, asserting the same authoritative WorkItem and Change professional reference are returned on replay.
- Extended `Service.loadResult` to hydrate the Change extension inside the transaction for completed `change_request` replays.
- Widened `boundedMetricRecordClass` to recognize `change_request` and added a focused metric test.

Fix commit:

- `4b59b76b` - `fix(intake): replay change request idempotently`

### Red

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestServiceCreateChangeRequestCommitsAndReplaysSameProfessionalReference|TestBoundedMetricRecordClassRecognizesChangeRequest' -count=1
```

Result:

- `FAIL`
- `TestBoundedMetricRecordClassRecognizesChangeRequest`
  - expected: `change_request`
  - actual: `unknown`
- `TestServiceCreateChangeRequestCommitsAndReplaysSameProfessionalReference`
  - error: `completed work item has an unsupported record class`

### Green

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake -run 'TestServiceCreateChangeRequestCommitsAndReplaysSameProfessionalReference|TestBoundedMetricRecordClassRecognizesChangeRequest' -count=1
```

Result:

- `PASS`
- `ok      itsm-backend/handlers/intake    0.061s`

### Broader Verification

Command:

```bash
cd /home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation/itsm-backend && go test ./handlers/intake ./service/... -count=1
```

Result:

- `PASS`
- `ok      itsm-backend/handlers/intake    1.796s`
- `ok      itsm-backend/service    24.959s`
- `ok      itsm-backend/service/approver   0.825s`
- `ok      itsm-backend/service/bpmn       3.528s`
- `ok      itsm-backend/service/cloud      0.010s`
- `?       itsm-backend/service/cloud/aliyun       [no test files]`
- `?       itsm-backend/service/common/event       [no test files]`
- `ok      itsm-backend/service/marketplace        0.120s`
- `?       itsm-backend/service/scenario   [no test files]`