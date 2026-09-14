# Source baseline integration plan

Status: accepted for execution by maintainer on 2026-09-14; completion requires verification below.

**Goal:** Consolidate completed ITSM source and open PR ancestry into main, preserving existing worktrees and uncommitted work.
**Architecture:** Integrate frozen candidate 0788a9bb (contains PR17/18 and classification/RCA/design dependencies), UI runtime c3347992 (contains PR12 and PR19–22), and completed configuration/G-A documentation. Resolve actual overlaps in one authoritative implementation. PR9 head already belongs to main.
**Tech Stack:** Git, Go/Gin/Ent, Next.js/TypeScript, GitHub Actions.
**Requirements:** User explicitly authorizes main source baselining. This is not database migration, deployment, R038 execution, historical import or production authorization.

## Constraints

- Use codex/chore/source-baseline-20260914 and its isolated worktree, based on a25e108d.
- Preserve uncommitted work and other agents' evolving branches; do not ingest unfinished routing work.
- Do not run application startup, shared DB tests, seed/migrate commands or deployment workflows.
- Existing accepted configuration decisions remain authoritative; documentation drafts do not become implemented by merging.

## Tasks

- [x] Inspect PRs, current main, dependencies and dirty worktrees; fetch remote references.
- [x] Establish safe baseline frontend/backend checks; log any baseline failures separately.
- [x] Merge fixed candidate and UI completed source; inspect each conflict and preserve latest domain authorization/API behavior and UI functionality.
- [x] Include completed migration/G-A documents and offline clone-tool hardening; retain snapshot revisions.
- [x] Run frontend lint/type-check/unit/production build and backend build/unit suites with no shared-service credentials; fix integration regressions with meaningful tests.
- [x] Obtain independent backend and UI review of the resulting source; address Critical/Important findings.
- [ ] Push reviewed integration branch, obtain current-head CI evidence, merge to main without force push; reconcile PRs already included by ancestry.
- [ ] Verify remote main SHA and local main synchronization, report included/excluded source and runtime-validation limits.

## Verification commands

Frontend: reused dependencies with an identical lockfile from the completed UI worktree; npm run lint:check; npm run type-check -- --incremental false; npm run test:ci -- --runInBand --forceExit --modulePathIgnorePatterns=<rootDir>/.next/; npm run build.
Backend: inspect TestMain/integration guards before running go test -p 2 ./...; go build ./.... No live-PG tests without an independently provisioned disposable target.
Repository: git diff --check and offline node --test scripts/__tests__/clone-itsm-migration-db.test.js when present.

## Delivery record

Frozen inputs: candidate `0788a9bb196ab37a8389b3f366bed9877b2f72c3`, UI runtime `c33479922e2f41c09e7d66ab7af957bc031de72c`, plans `85794de2c0edb468bbd4839f4a6aecb230749d7f`, G-A `d91b587fe3ab40cc863321346d217d258a3a96d8`, config docs `50265d75884aa4724449fda2442acee608c407cf`.

Resolved canonical BPMN queries, attachment permissions, projection directory/transaction preservation, and collaboration-versus-relation authorization. Updated stale notification/tenant/version fixtures without weakening production checks. Isolated SQLite tests retain within-test shared connections and roll back owned transactions. Independent backend/UI reviews found no remaining Critical/Important production issue in the reviewed integration.

Local verification:
- Backend full go test and build passed; offline migrate CLI tests passed. VCS stamping used process-local safe.directory for the differently-owned shared root; no global Git config changed.
- Frontend lint passed with one existing warning; types and production build passed. Jest: 250 suites passed, 3415 tests passed, 13 skipped. Coverage: statements 81.36%, branches 67.60%, functions 83.06%, lines 82.80%, meeting unchanged thresholds. Original provider retained with documented standalone-script instrumentation exclusion; all bootstrap behavior tests run.
- Engineering contracts 7/7, API paths 789/789, ACL 548 routes at 100%, offline clone/contract tests 9/9, mapping tests 3/3. Cross-file mappings resolve filename-heuristic misses without blanket source skips.

Embedded development Compose API credential replaced with environment injection; historical exposure is not erased and provider rotation remains separate.

PR23/24 appeared during concurrent work after the frozen inventory and remain excluded. No shared dev DB, production, R038, historical import or runtime deployment was operated. Source integration does not approve a new G-A runtime revision. Conditional PostgreSQL and browser E2E tests were not rerun against shared services.

WSL new-session startup returned Wsl/Service/E_UNEXPECTED while the existing Jest process completed. The verified staged tree `c2b6cccdd0b7372d661e299472baf9557781effc` and its history were transferred as Git objects to an independent Windows checkout. The merge keeps parents f5f501c4 and c3347992, with generated test reports restored and delivery documentation updated. Original WSL worktree remains preserved; no shared service restart. Remote CI and merge evidence follow.
