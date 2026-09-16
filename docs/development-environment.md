# KAF / ITSM maintained WSL development environment

Status: maintained operational contract, updated 2026-09-15. The maintainer selected **3010 as the ITSM frontend port**. Deployment filenames containing `ga`, `candidate`, or `prod` do not establish environment identity or release acceptance. This environment is named **WSL development**.

> Before changing Dev, read the [verified Dev031/main047 divergence analysis](review/2026-09-16-dev-schema-divergence-report.md). The maintainer accepted the [restoration design](superpowers/specs/2026-09-16-dev-restoration-two-database-design.md); use the [four-stage execution checklist](superpowers/plans/2026-09-15-migration-validation-ledger.md#two-database-execution) and its evidence gates instead of historical proposals. The selected code/schema compatibility target remains047.

## Selected schema target: 047

**Current development and migration-validation target, confirmed 2026-09-15: `047_bpmn_assignment_source`.** Both Dev and the migration-validation database must support this selected current-code schema. Do not choose an older backend to accommodate a database at 031 or 046. This is the target contract, not a statement that either live database has already been upgraded.

| Decision | Authority for the current task |
| --- | --- |
| Schema target for both database roles | **047_bpmn_assignment_source**, with the complete required canonical dependency chain and actual receipts |
| Existing Dev upgrade path from its verified 031 baseline | Read-only classification and backup/restore verification → controlled P037 → ordinary032–036 and039–047 → matching runtime/system/inspection configuration and acceptance |
| Maintained validation clone | Proposed sequence: restore and accept Dev at047, then create a traceable Dev snapshot clone. The former isolated046 target is a preserved evidence/difference source; this table does not authorize upgrading it. Follow the [two-role convergence plan](superpowers/plans/2026-09-15-migration-validation-ledger.md#dev-clone-alignment) |
| Migration tool prerequisite for the old Dev ledger | Use the merged PR38 atomic ledger preparation fix (`0e1afe997`, merged in `32c39dda3`) or a reviewed descendant |
| R038 retirement | Remains a separate manual stage; **not required to restore Dev** and not implicitly included by saying “upgrade to047” |
| Actual destination and progress | Read the [dated target-status table and single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#schema-target-status); inspect the live recipe/database before acting |

047 adds the persisted, immutable `process_tasks.assignee_source` contract for explicitly bound WorkItem-assignee tasks. It does not turn historical tasks into bound tasks, repair process routing configuration, import legacy data, or establish business acceptance. The canonical [migration registry](../itsm-backend/migration/migrations.go) and [047 SQL](../itsm-backend/migrations/047_bpmn_assignment_source.sql) define the structure; the [assignment report](review/2026-09-15-work-item-task-assignment-report.md) defines the associated behavior and evidence.

For coding agents: start from this selected target and the current source registry. Treat later sections describing earlier port/candidate work and older migration numbers as historical or feature-specific evidence. If a later task intentionally advances beyond047, update this target and its linked execution ledger together; do not silently freeze development at047 or silently deploy a newer schema. Matching the maximum receipt number alone does not prove that required migrations, privileges, configuration and UI paths are valid.

## Endpoint ownership

| Service | Windows/LAN host port | Authority |
| --- | --- | --- |
| ITSM frontend | **3010** | Native Next.js standalone process; same-origin `/api/*` forwards to ITSM 8080 |
| ITSM backend | 8080 | Native Go binary pinned by SHA-256 |
| KAF frontend | 5173 | Its own configured frontend release |
| KAF API | 8000 | Its own configured API release |
| Langfuse | **3000** | `acp-langfuse` container; never start ITSM on this host port |
| Former ITSM frontend | **3001 — retired** | Do not use for startup, tests, callbacks, or current documentation |

Windows host: `192.168.31.66`. SSH reaches Ubuntu WSL as `administrator`, port `22222` (the host also has an SSH forwarding alias on `22223`). Browser address from the LAN/Mac: `http://192.168.31.66:3010`; Windows localhost: `http://localhost:3010`. A Mac `localhost` is the Mac, not WSL. A container's internal port 3000 is not the Windows/WSL published port and need not be renamed.

## Development and migration validation database contract

**Accepted by the maintainer on 2026-09-15.** The goal is to make the agreed legacy ITSM data work in the new ITSM while normal development continues. Development and migration validation use one evolving product codebase. Their intended difference is the database's purpose and data, not a permanently older application or data model.

| Dimension | Daily development | Migration validation |
| --- | --- | --- |
| Application | Current selected, reviewed frontend/backend release | The same selected frontend/backend release for comparative acceptance |
| Database purpose | Dev database for ongoing development and development test records | A traceable clone of Dev for cleaned legacy data and acceptance records |
| Schema | Canonical migrations required by the selected code | The same required schema contract; verify migration receipts and structure independently |
| Data/configuration | Development fixtures and configuration | Approved source mappings, transformed data and target business configuration |
| Shared entry | 3010 → 8080 targets Dev for daily work | Temporarily switch the same entry to the validation profile for a scheduled validation window |

### Version and schema discipline

- Record frontend commit/build ID, backend commit/artifact hash, database instance/name/schema, migration receipts and the active profile. A branch name or a maximum migration number alone is insufficient proof of compatibility.
- Develop fixes once in the shared codebase. Propagate the selected release and its required canonical schema changes to both database roles. Different business data and environment configuration are expected; every difference affecting acceptance must be recorded.
- A schema-changing task includes dependency-aware upgrade and verification for both roles. If either database lags, record a blocking gap and complete its upgrade before using the new code there. Do not report results from different code/schema contracts as equivalent acceptance.
- **Do not restore or retain old application code as the solution for switching back to Dev.** Preserve Dev data and service stability by planning a compatible upgrade. “Keep Dev stable” does not mean freezing its schema indefinitely.
- A source merge is not a deployment. Select and verify a concrete frontend/backend release together; do not automatically deploy every new main commit or blindly run all migrations.

### Three separate workstreams

1. **Schema compatibility:** use the existing canonical Migrator, dependency checks and truthful receipts. Separate structural preparation, ordinary migration, business acceptance and controlled retirement. Do not edit historical SQL/checksums, fabricate receipts, use Ent overlays, or enroll historical WorkItems to pass admission. Apply the [controlled retirement contract](../AGENTS.md#accepted-workitem-decisions-and-migration-boundaries).
2. **Legacy data adaptation:** clean and map approved legacy master/configuration data to the new model. Old tickets, comments, attachments, approvals and process instances remain excluded. Existing five-batch reconciliation is completed evidence, not a reason to rerun all imports.
3. **Reusable validation:** reuse Migration Validation Toolkit v1 for its implemented users/departments verification and missing-object supplementation. It is not a schema upgrade tool or a replacement for the five-batch configuration executor. Its source/status and remaining tasks are maintained in the [single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#development-restoration-update).

### Switching and acceptance procedure

1. Inspect the live destination and all dependencies read-only; compare the chosen code's requirements with each target's actual schema, privileges and migration evidence. Use explicit database identity, never a label such as GA or Dev alone.
2. Before an approved Dev schema change, verify backup coverage and restoration in an isolated rehearsal target. Produce the exact migration/preparation scope, role grants, downtime expectations, acceptance checks and data-preserving recovery/remediation plan. Retirement and deletion retain their separate authorization boundaries; this document does not authorize their execution.
3. Verify the required canonical changes on the rehearsal target, then apply the reviewed and authorized scope to Dev. Keep automatic migration/seed disabled in normal service startup. Missing execution-domain tables are a real startup blocker even in standard mode; do not bypass admission or grant owner/superuser access.
4. Switch the **complete profile** through the maintained startup authority: runtime/system/inspection identities must target the same database/schema/deployment; also select Redis namespace/database, attachment storage, origins/session settings and execution capabilities. Stop affected consumers, isolate pending work and caches, and verify session handling so no state or work crosses targets. Never mix a Dev runtime connection with validation inspection or system connections.
5. Verify actual process destination, readiness, login and the representative 3010 → 8080 UI path; record the profile and release evidence. Restore daily development to Dev after the validation window. Pending migration acceptance must not become a permanent dependency for continuing development.

One shared 8080 serves one target at a time. This topology does not provide simultaneous access to both databases; coordinate the validation window with development. Any future concurrent topology needs an explicit operational decision and documented port ownership, not ad hoc use of 3000/3001.

Database labels describe roles, not lineage: `itsm_ga_ready` was prepared as an isolated new-model target, not a full Dev clone or a GA release. The maintainer reaffirmed on 2026-09-16 that the maintained validation role must use a verified Dev clone. Existing isolated targets retain their prior evidence and serve as transition sources; they are not interchangeable with that clone. See the [two-database convergence proposal and current observations](superpowers/plans/2026-09-15-migration-validation-ledger.md#dev-clone-alignment). Dated observations and the ordered recovery tasks belong in the [single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#development-restoration-update); this contract is not a live deployment report.

## One startup authority

The maintained entrypoint is `/home/administrator/apps/itsm-kaf/stack`, deployed from [`scripts/wsl-stack.py`](../scripts/wsl-stack.py). Its private state remains `/home/administrator/.local/state/itsm-kaf-baseline-20260908`; the historical date is a storage path, not a version identifier.

- `config/<service>-launch.json` is the active startup recipe: argv, cwd, private environment, port, source revision and artifact/build identity where available.
- `evidence/<service>-process.json` binds a managed process to PID, process start time, cwd and the recipe fingerprint.
- `active-release.json` is a sanitized deployment snapshot containing endpoint ownership, actual frontend revision/build ID, backend executable hash and source provenance, and known verification limits. Use current process/config checks to detect drift after this snapshot.
- `logs/<service>.log` contains service output; never publish secrets or whole environment/config files.

```bash
/home/administrator/apps/itsm-kaf/stack status
/home/administrator/apps/itsm-kaf/stack status itsm-web
# Only when startup/restart is part of the authorized task:
/home/administrator/apps/itsm-kaf/stack stop itsm-web
/home/administrator/apps/itsm-kaf/stack start itsm-web
```

Use a named service for bounded maintenance. Status detects recorded/configured version drift and untracked port owners; do not defeat it by deleting process records. Stop validates process identity and signals only the recorded PID. A listening port alone does not prove service health.

Old one-off launchers, historical JSON snapshots and handoff reports are rollback evidence, not additional active deployment authorities. Do not run historical `ga-frontend-switch.py`, `ga-backend-switch.py`, `pin-ga-frontend.py`, or `dev-services.py` to replace the active recipe. Any new deployment must update the canonical recipe, process evidence and sanitized release snapshot together.

## Version selection and build boundaries

Always verify **host → port → listener PID/start time → executable/cwd → artifact hash/build ID → source revision → backend destination**. Never infer identity from a branch name, directory name, file modification time, or port alone.

Historical port/theme integration evidence (not the current release authority): the 2026-09-15 frontend integration started from the running workbench source `93480226` and merged A/C visual theme source `eb76c3bc`. The exact deployed merge/fix revision and Next.js build ID are recorded in `active-release.json`. This preserves current workbench commands while adding the completed theme. Newer `main` also contains unrelated domain/database work; updating it is not authorization to deploy its entire backend.

The backend source provenance recorded by the previous deployment is `c3c880df`; its binary fingerprint before the port change is `d395dbd5a03739d48cde6fe7898ec45d7daadb19686ac265f5bf6e70239a58ef`. A source label is recorded provenance, not a fresh reproducible-build attestation. That completed frontend-port task kept that binary and database target and changed only the frontend URL/origins necessary for 3010, and recorded the resulting configuration fingerprint. It did not run migrations or grant database permissions.

Source checkout edits do not automatically update a standalone build. Build in an isolated worktree, verify current command/theme behavior, copy required `.next/static` and `public` artifacts into standalone output, verify `/api/*` targets 8080, and record revision/build ID before switching. Avoid concurrent builds or dependency installs against the active runtime directory.

## Verification and honest status

```bash
curl --fail --silent --output /dev/null http://127.0.0.1:3010/login
curl --fail --silent http://127.0.0.1:8080/api/v1/readyz
curl --fail --silent http://127.0.0.1:8000/health
curl --fail --silent --output /dev/null http://127.0.0.1:5173/
```

Also verify representative frontend assets against the deployed filesystem, same-origin API responses, light/dark theme behavior, LAN access, no ITSM listener on 3001, and unchanged Langfuse ownership of 3000. Authenticate through normal sessions when business UI validation is needed. Do not create requests, approvals, provider calls or IAM mutations merely to validate a port change.

On 2026-09-15 before this change, ITSM `/health` returned 200 but `/readyz` returned 503 because its database identity could not read `schema_migrations`; this is a permission/readiness failure, not proof of missing schema. KAF 8000/5173 and ITSM workers were stopped. A frontend deployment does not certify these independent services or fix their readiness. Report their current status separately.

## Shared infrastructure and rollback

Preserve existing PostgreSQL, Redis, MinIO, Qdrant, Langfuse and Ollama containers and volumes. Do not run generic `init`, `reset`, `down -v`, migration/bootstrap tools, or database cleanup against this shared environment.

Before changing a runtime, save private launch recipes, exact PID identities, executable hashes, frontend build ID and relevant configuration. The 3010 task's pre-change backup is `/home/administrator/.local/state/itsm-wsl-3010-20260915T035625Z` (private). Record any later supplemental backups alongside it. Restore only the identified affected service and configuration; verify no concurrent operator replaced its process. Rollback is an explicit operation, not a second normal startup path.

Development repositories remain `/home/administrator/project/itsm` and `/home/administrator/project/kaf`, with task worktrees. Retain linked-worktree Git metadata, uncommitted work, `.superpowers/sdd` ledgers and archived source copies. Do not delete or reorganize another agent's work as part of port/version maintenance.

## Ticket detail experience deployment (2026-09-15)

PR #35 unifies the existing detail-page refresh, preserves editing context, and separates current tasks from collapsed task history. The maintainer authorized merging this PR and applying its frontend to local WSL development on 3010. Build from the branch after integrating current main; rerun affected frontend tests before switching the canonical `itsm-web` recipe.

This release changes the frontend only. Preserve the existing 8080 executable, backend configuration, databases, workers and KAF services. Back up the old frontend recipe and `active-release.json`, retain the previous standalone directory, and record the new source revision/build ID in the canonical recipe and sanitized snapshot. Validate actual 3010 login/detail behavior with the repository's guarded Playwright tests. Failed verification requires restoring the saved frontend recipe and starting the previous release through `stack`.

The exact applied revision, build ID and verification outcome belong to the local `active-release.json`; a merged PR alone is not proof that the running frontend was switched.
