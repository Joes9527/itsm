# KAF / ITSM maintained WSL development environment

Status: maintained operational contract, updated 2026-09-15. The maintainer selected **3010 as the ITSM frontend port**. Deployment filenames containing `ga`, `candidate`, or `prod` do not establish environment identity or release acceptance. This environment is named **WSL development**.

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

The 2026-09-15 frontend integration starts from the running workbench source `93480226` and merges A/C visual theme source `eb76c3bc`. The exact deployed merge/fix revision and Next.js build ID are recorded in `active-release.json`. This preserves current workbench commands while adding the completed theme. Newer `main` also contains unrelated domain/database work; updating it is not authorization to deploy its entire backend.

The backend source provenance recorded by the previous deployment is `c3c880df`; its binary fingerprint before the port change is `d395dbd5a03739d48cde6fe7898ec45d7daadb19686ac265f5bf6e70239a58ef`. A source label is recorded provenance, not a fresh reproducible-build attestation. The frontend-port task keeps that binary and database target, changes only the frontend URL/origins necessary for 3010, and records the resulting configuration fingerprint. It does not run migrations or grant database permissions.

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
