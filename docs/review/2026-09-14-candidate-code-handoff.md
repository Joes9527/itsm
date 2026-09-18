# Candidate code handoff — 2026-09-14

Status: fixed candidate code; live G2/G3 progress is separate. This document identifies the deliverable, not another remaining-task list.

- CandidateSHA: `0788a9bb196ab37a8389b3f366bed9877b2f72c3`.
- Branch: `codex/fix/candidate-approval-contract`; isolated worktree: `/home/administrator/project/itsm/.worktrees/candidate-approval-contract`.
- Verified bundle: `~/.local/state/itsm-candidate-delivery/handoff/candidate-0788a9bb.bundle`; required base `a25e108d2a08a55469fa5ad547aac5a9adc251ff`.
- Runtime binaries: `~/.local/state/itsm-candidate-delivery/t3/build-approval-contract/{itsm-api,itsm-worker,itsm-migrate}`. Digest evidence: environment-20260914/redeploy-0788a9bb.json. Worker entry is cmd/kaf_worker; migrate requires the migrate build tag.
- Frontend source tree is unchanged from3142247e; its validated production standalone and locked dependencies are reused from candidate-release-windows. The current environment revision independently checks tree identity. No unnecessary frontend rebuild was used as a substitute for live validation.

The upstream integration/M1/R4 code is retained. Four bounded fixes after the prior3142247e handoff are included: terminal manual038 catalog ordering; exact039 trigger admission after preparation; Incident assignment event decoding of producer-supplied previousAssigneeId while preserving strict payload/audit provenance. The fourth maps Change approval HTTP history through the existing camelCase DTO so approved CAB records display correctly. All were independently reviewed with red/green evidence. No historical migration SQL or main checkout was overwritten.

Prior G1 evidence: locked install, type/theme checks, production frontend build, lint with one existing warning, seven focused suites159 tests, and backend builds. New delta evidence: agent-a-windows/r6-catalog-*, r6-trigger-*, r7-assignment-* and corrected API/kaf_worker/tagged migration builds. Opt-in skips in older package runs are not counted as executed checks. Current API/Worker binaries ran real candidate acceptance; [T4 evidence](2026-09-14-candidate-t4-evidence.md) gives exact logs and limits.

## Operator constraints

Use [candidate runtime instructions](../deployment/itsm-candidate-runtime.md) and [T3 evidence](2026-09-14-candidate-t3-handoff.md). The sole progress source remains the remaining-delivery plan on codex/docs/candidate-a-remaining-delivery. Documentation commits are separate from the code SHA and do not imply another binary revision.

New PG authentication storage is **not wired to startup/login/refresh/logout; its real restart and recovery validation remains unfinished and accepted as such by the maintainer**. Runtime continues on the old Redis/memory path. Do not claim the presence of046 proves production use or session recovery safety.

Incident defaults to the explicitly declared manual lifecycle; automatic emergency orchestration remains unenabled under the communicated reversible functional-priority assumption, with maintainer preference still pending. Change and RequestedItem use real BPMN. Feishu is outside scope; local SMTP/Webhook/KAF capture is not enterprise delivery or real KAF execution. R038 is forbidden; original and test history is preserved. No main merge, push, production rollout or external enterprise write is represented by this handoff.
Latest delta evidence: r7-approval-contract-red.log (actual missing camelCase assertion), r7-approval-green.log (focused handler race pass), t4/approval-ui-red.log and approval-ui-green.log (real API plus approved timeline/reload, 1/1 passed). Independent review permits reuse of unchanged04bc business and periodic-runtime evidence; no claim that the full suite reran on0788. API, Worker and migrate binaries all rebuilt successfully. Git commit succeeded despite an unrelated existing worktree reflog permission error from automatic GC; that workspace was not modified to suppress the warning.