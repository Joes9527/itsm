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
- [ ] Establish safe baseline frontend/backend checks; log any baseline failures separately.
- [ ] Merge fixed candidate and UI completed source; inspect each conflict and preserve latest domain authorization/API behavior and UI functionality.
- [ ] Include completed migration/G-A documents and offline clone-tool hardening; retain snapshot revisions.
- [ ] Run frontend lint/type-check/unit/production build and backend build/unit suites with no shared-service credentials; fix integration regressions with meaningful tests.
- [ ] Obtain independent backend and UI review of the resulting source; address Critical/Important findings.
- [ ] Push reviewed integration branch, obtain current-head CI evidence, merge to main without force push; reconcile PRs already included by ancestry.
- [ ] Verify remote main SHA and local main synchronization, report included/excluded source and runtime-validation limits.

## Verification commands

Frontend: npm ci --legacy-peer-deps; npm run lint:check; npm run type-check -- --incremental false; npm run test:ci -- --runInBand --forceExit --modulePathIgnorePatterns=<rootDir>/.next/; npm run build.
Backend: inspect TestMain/integration guards before running go test -p 2 ./...; go build ./.... No live-PG tests without an independently provisioned disposable target.
Repository: git diff --check and offline node --test scripts/__tests__/clone-itsm-migration-db.test.js when present.

## Delivery record

Pending source integration and current-head verification; no runtime acceptance asserted.
