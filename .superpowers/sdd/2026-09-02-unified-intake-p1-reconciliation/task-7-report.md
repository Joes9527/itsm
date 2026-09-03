# Task 7 Implementation Report

## Scope

Implemented Task 7 only in the isolated worktree:

- Extended intake incident input propagation to carry the full legacy `dto.CreateIncidentRequest` field set needed by this task.
- Reused the existing incident assignee tenant validator via an exported `IncidentService.ValidateIncidentAssignee` method.
- Persisted incident `impactAnalysis` and `metadata` through the intake incident extension path.
- Made incident `source` authoritative from `IncidentInput.Source` when present.
- Widened the canonical idempotency digest to cover the full `IncidentInput` field set and bumped `CanonicalDigestVersion` to `intake-v2`.
- Preserved Task 6 behavior, including `ExplicitPriority`, while updating `IncidentCreator` to the required final four-argument constructor.

## Files Changed

- `itsm-backend/handlers/intake/canonicalize.go`
- `itsm-backend/handlers/intake/canonicalize_test.go`
- `itsm-backend/handlers/intake/command.go`
- `itsm-backend/handlers/intake/creator_test.go`
- `itsm-backend/handlers/intake/idempotency_repository_test.go`
- `itsm-backend/handlers/intake/incident_creator.go`
- `itsm-backend/handlers/intake/postgres_integration_test.go`
- `itsm-backend/handlers/intake/service_test.go`
- `itsm-backend/handlers/intake/work_item_creator.go`
- `itsm-backend/service/incident_service.go`

## Failing-Test Evidence Before Implementation

### 1. New Task 7 propagation test failed in the expected way

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run TestIncidentCreatorCarriesFullDTOFieldSet -v
```

Observed failure summary:

- `unknown field AssigneeID in struct literal of type IncidentInput`
- `unknown field ImpactAnalysis in struct literal of type IncidentInput`
- `unknown field Metadata in struct literal of type IncidentInput`
- `plan.WorkItem.AssigneeID undefined (type WorkItemDraft has no field or method AssigneeID)`
- `extPlan.ImpactAnalysis undefined`
- `extPlan.Metadata undefined`
- `too many arguments in call to NewIncidentCreator` because the code still exposed the three-argument constructor.

### 2. New canonical digest coverage test failed in the expected way

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run TestCanonicalizeCommandDigestChangesWithFullIncidentFieldSet -v
```

Observed failure summary:

- Build failed because `IncidentInput` did not yet carry `AssigneeID`, so the digest could not include it.

### 3. Existing digest-version stability test failed after the mandated version bump until its expectation was updated

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run TestCanonicalDigestVersionIsStable -count=1 -v
```

Observed failure summary:

- Expected `intake-v1`
- Actual `intake-v2`

## Passing-Test Commands and Output Summary

### Focused Task 7 tests

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run 'TestIncidentCreatorCarriesFullDTOFieldSet|TestCanonicalizeCommandDigestChangesWithFullIncidentFieldSet' -count=1 -v
```

Summary:

- `TestCanonicalizeCommandDigestChangesWithFullIncidentFieldSet`: PASS
- `TestIncidentCreatorCarriesFullDTOFieldSet`: PASS

### Existing digest-version regression

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run TestCanonicalDigestVersionIsStable -count=1 -v
```

Summary:

- `TestCanonicalDigestVersionIsStable`: PASS

### Existing idempotency version-conflict regression

Command:

```bash
cd itsm-backend
go test ./handlers/intake -run TestIdempotencyClaimRejectsDifferentDigestOrVersion -count=1 -v
```

Summary:

- `TestIdempotencyClaimRejectsDifferentDigestOrVersion/digest`: PASS
- `TestIdempotencyClaimRejectsDifferentDigestOrVersion/version`: PASS

### Brief-required focused package command

Command:

```bash
cd itsm-backend
go test ./handlers/intake ./service -run 'TestIncidentCreator|TestIncidentService|TestCanonicalizeCommand' -count=1
```

Summary:

- `itsm-backend/handlers/intake`: PASS
- `itsm-backend/service`: PASS

## Commit

- Commit SHA: `65cefc96ecce7fc49d80e487464ceb81a4842579`

## Remaining Concerns

- A full `go test ./handlers/intake -count=1` still fails in this worktree on out-of-scope service-request-extension tests:
  - `TestServiceRequestItemCreatorCreatesExactlyOneExtension`
  - `TestMetricsRecordCreateReplayConflictLatencyAndWorkflowStates`
  - `TestServiceCreateCommitsOneAuthoritativeGraphAndReplays`
- Those failures all surface `could not create service request extension` and align with the separate Task 8 service-request creator issue called out in the task request. I did not change that code path.
- The task report file itself was written after the code commit so the worktree now contains this uncommitted report artifact.

## Fix Round 1

### Review Finding Addressed

- Added the missing DB-layer persistence coverage for non-nil `assigneeId`, `impactAnalysis`, and `metadata` across `Prepare -> CreateBase -> CreateExtension -> Commit`.

### Changed Files

- `itsm-backend/handlers/intake/creator_test.go`
- `.superpowers/sdd/2026-09-02-unified-intake-p1-reconciliation/task-7-report.md`

### Red Evidence

- The pre-existing Task 7 coverage in `TestIncidentCreatorCarriesFullDTOFieldSet` only verified the in-memory `CreationPlan` and did not execute `CreateBase`, `CreateExtension`, or `Commit`, so persisted `ent.Incident.ImpactAnalysis`, `ent.Incident.Metadata`, and `ent.Ticket.AssigneeID` remained unverified at the database layer.

### Green Evidence

- Added `TestIncidentCreatorPersistsFullDTOFieldSetAfterCommit`, which:
  - prepares an incident with non-nil `assigneeId`, `impactAnalysis`, and `metadata`
  - creates the base work item
  - creates the incident extension
  - commits the transaction
  - reloads `ent.Ticket` and `ent.Incident` from the database and asserts persisted values
- Verification commands:

```bash
cd itsm-backend && go test ./handlers/intake -run TestIncidentCreatorPersistsFullDTOFieldSetAfterCommit -count=1 -v
cd itsm-backend && go test ./handlers/intake -run TestIncidentCreator -count=1
```

- Observed results:
  - `TestIncidentCreatorPersistsFullDTOFieldSetAfterCommit`: PASS
  - `go test ./handlers/intake -run TestIncidentCreator -count=1`: PASS

### Change SHA

- `982921a1fba17972782478a2b0f7d46e3640da50`