# ITSM / KAF database reconciliation handoff

- Gate: G-A
- Status: PASS (isolated schema and configuration-migration admission; independently reviewed)
- Date: 2026-09-14, Asia/Shanghai.
- Owner: task 1 / current Agent. Consumer: separately assigned task 2 Agent.
- GARevision: the full Git commit containing this handoff, obtained with `git rev-parse HEAD` in this worktree; this is not either application SHA.
- Plan/spec input: ITSM `85794de2c0edb468bbd4839f4a6aecb230749d7f`, `docs/superpowers/plans/2026-09-14-itsm-kaf-task-1-reconciliation.md` and `docs/superpowers/specs/2026-09-14-itsm-kaf-database-convergence-design.md`.

## Admission and scope

The user's instruction to execute task 1 was followed by read-only inventory, source/CLI review, isolated-target preflight and independent review before each write batch. All task-owned writes were to new `ga-*` resources. Shared sources were queried read-only, including the Phase 1 identity snapshot. No historical ticket import, ITSM R(038), source shutdown, history/queue clearing, production change, enterprise action or main merge occurred.

G-A admits task 2's controlled configuration migration into `itsm_ga_ready`, using a protected migration identity. It establishes fixed code, actual ledger/schema checks, role boundaries and preserved Phase 1 references. It is not full application startup, login, functional acceptance, G-B, G-C or a cutover authorization. Runtime grants below deliberately identify what is available and what still needs a reviewed capability manifest before business applications can start.

## Fixed source and artifact identities

| Item | Fixed identity and evidence |
| --- | --- |
| ITSM task 1 branch | `codex/chore/database-reconciliation`, based on fetched `origin/main` `a25e108d2a08a55469fa5ad547aac5a9adc251ff`; worktree `/home/administrator/project/itsm/.worktrees/database-reconciliation` |
| ITSM target source | `0788a9bb196ab37a8389b3f366bed9877b2f72c3`; `/home/administrator/project/itsm/.worktrees/candidate-approval-contract` |
| ITSM artifacts | `/home/administrator/.local/state/itsm-candidate-delivery/t3/build-approval-contract/` |
| API SHA256 | `0d88aff47fb6e729bc14bbb665e950fec535891bd16fb2261b1521b1ce3ec25f` |
| Worker SHA256 | `c0fcfdb015aaf85fb33f467941507c30683043ac2835533dc2aad9ce7365b3b2` |
| Migrate SHA256 | `52141be80defff46bbc91f90f7a38fd6e5ff0a02adaa5e9e4fd939cfebc011a8` |
| KAF maintained baseline | `69bd0901d8306bdd5f4bb6a79af0eb9567b5b2b0`; 475 tracked runtime files under `src/acp`, `alembic` and `alembic.ini` match deployed archive `/home/administrator/apps/itsm-kaf/kaf-handoff-69bd0901` byte for byte |
| KAF target source | `23f01476b8ea7293c423d608329241477a5336a5`, branch `codex/fix/kaf-schema-reconciliation`; `/home/administrator/project/kaf-worktrees/kaf-schema-reconciliation` |
| KAF changes | New forward migration 039, offline/live regression tests and provisioning guide; no ITSM business code changed |

The ITSM binaries' embedded Go VCS metadata says `a25e108d` with `modified=true`, not 0788. Their association with 0788 uses the prior frozen deployment manifest and exact artifact digests; embedded metadata alone does not prove it. Older baseline binaries lack sufficient embedded source metadata to independently establish their exact build provenance. These limitations prohibit claiming every historical mismatch's cause has been proven.

Entry checkouts and existing work were retained: ITSM `codex/migration-legacy-config-data` at `660087795e6efe49e89b759d2527ad1b8320a651` with untracked `docs/poc/`; KAF extraction/design at `184f7868161794bd549a6fbe2d46e41fe0de854e`. Remotes are `git@github.com:Joes9527/itsm.git` and `https://github.com/DawnproIN/kaf.git`.

KAF's extraction branch is not the maintained runtime target: 46 `src/acp` files differ from the runtime baseline. Fetched KAF main also diverges from that baseline (1 main-only / 70 baseline-only commits). Reviewed deviation: 039 is based directly on the fixed maintained baseline; unrelated main integration is not smuggled into schema reconciliation. No branch was merged or pushed by this task.

## Observed environments and disposition

All table counts below are public-schema counts from the protected inventory; test schemas are retained. Production was not inspected.

| Instance / database | PG / observed ledger | Objects and target disposition |
| --- | --- | --- |
| `itsm-postgres-dev / itsm` | 17.10 / 14 entries, 019 | 152 public tables; 1,104 other test schemas. Legacy versions and checksum differences; no in-place upgrade admitted |
| same / `itsm_baseline_20260908` | 17.10 / 14, 019 | 152 tables; 1,013 other schemas; same legacy route blocked |
| same / `itsm_config_baseline_20260908` | 17.10 / 24, 031 | 152 tables; user-confirmed source. No unknown versions or checksum mismatches against candidate catalog; 14 catalog differences require semantic handling |
| same / `itsm_migration_20260914` | 17.10 / 14, 019 | 152 tables; old dev clone, not a 046 target |
| same / `itsm_p1_integration_verify_20260901` | 17.10 / 15, 022 | 149 tables; checksum differences at 015 and 022 |
| same / `itsm_intake_test` | 17.10 / absent | 138 tables; status correctly rejects a populated database without a ledger |
| `kaf-dev-postgres / control_plane` and `kaf_baseline_20260908` | 16.14 / 036 | 40 tables each; lack execution-phase upgrade; untouched |
| same / `kaf_config_baseline_20260908` | 16.14 / 038 | 44 tables; five model/column discrepancies described below; source untouched |
| `itsm-candidate-20260914-pg / itsm_candidate` | 17.10 / 38, 046 | 162 tables; genuine P037, no R038; fixed-artifact operator status passed before WSL restart |
| same / `itsm_candidate_test` | 17.10 / 24, 031 | 152 tables; old backup verification database, not 046 |
| `ga-itsm-20260914 / itsm_ga_ready` | 17.10 / 36, 046 | **Task 2 target**: 159 tables, 129 policies, current structure/status/role admission passed after identity preservation |
| `ga-kaf-20260914 / kaf_ga` | 16.14 / 039 | **KAF schema target**: 44 tables, 34 ORM tables; column types/nullability and application/checkpoint schema validators pass |

Each raw inventory includes columns, constraints, indexes, functions, triggers, sequences, policies, extensions, owners, roles/memberships and current sessions. Idle session inventories do not establish absence of future writers. Runtime evidence records the prior ITSM support-handoff API and two workers, candidate API/worker, executable digests and launch CWD. KAF was not listening on 8000 at inspection. Source workloads were not stopped and this snapshot is not a final cutover inventory/window.

### Difference decisions

- Legacy `010_add_ticket_types` is retired/unregistered; never replay it. Legacy `015_add_service_request_contact_fields` has the same SQL checksum as current `016_add_service_request_contact_fields`; full version strings, not numeric prefixes, identify entries. Current `015_process_instance_running_unique_guard` is a different migration.
- Dev/old clones also differ in checksums for 007, 009 and current 015. Do not alter checksums, delete entries or fabricate release versions to pass validation. Exact historical artifact provenance is incomplete, so their in-place upgrades remain blocked. The isolated target route avoids these histories.
- Fresh target 36 versus old candidate 38 ledger entries is expected: 022 and 027 are historical retired catalog entries. The fresh route omits old `problem_changes`, `problem_incidents`, `problem_root_cause_analyses`; current full runtime inspection passes. Do not add retired rows/tables to make counts equal.
- KAF 038 has nullable VPN identity columns `ad_code`, `vpn_code`, `vpn_guid`, while source models require NOT NULL; identity `created_at` and `updated_at` are naive timestamps while models require timezone-aware timestamps. Forward 039 repairs these five differences. No historical migration file was edited.
- 039 locks both affected tables and preflights before ALTER; NULL VPN identities abort. Empty naive timestamp tables can convert. Nonempty naive timestamps require independently established UTC provenance and explicit Alembic `Config.attributes['kaf_identity_timestamp_timezone']='UTC'`; unknown/unsupported timezones reject. Already-aware columns are preserved, rerun is safe, downgrade rejects.
- Old KAF configuration source has one enterprise-identity row with unproven historical timezone; server UTC is not proof. Its upgrade/data preservation remains blocked until provenance or a reviewed conversion decision is available. The new empty schema target does not import that row or erase the source.

## Isolated target contract

Network `ga-reconciliation-20260914` is Docker-internal. Both target containers have no published host ports and no source-data mounts. They remain two separate PG instances; task 3 will consolidate instances only after G-B.

| Target | Identity / persistence |
| --- | --- |
| ITSM | container `ga-itsm-20260914`, database `itsm_ga_ready`, schema `public`, owner/migration `ga_owner`, volume `ga-itsm-20260914-pgdata`, 1 GiB / 2 CPU limit |
| ITSM image | `sha256:7ae6051efd0e60444282c27c7e141af07f322ce033300e727a49c3dd11075e38`; PG 17.10; extensions plpgsql 1.0 and vector 0.8.6 |
| KAF | container `ga-kaf-20260914`, database `kaf_ga`, schema `public`, owner/migration `ga_kaf_owner`, volume `ga-kaf-20260914-pgdata`, 768 MiB / 2 CPU limit |
| KAF image | `sha256:e013e867e712fec275706a6c51c966f0bb0c93cfa8f51000f85a15f9865a28cb`; PG 16.14 |

Protected evidence/credential root: `/home/administrator/.local/state/itsm-db-reconciliation-20260914` (mode 0700). References only; never copy raw files into Git or echo URLs/passwords:

- ITSM migration config: `ga-ready-operator/config.yaml`; control file `ga-ready-operator/migration-control.json`; DeploymentID `ga-itsm-ready-20260914`.
- ITSM application-role secret references: `ga-ready-secrets.json`; owner infrastructure secret `ga-pg.env`.
- KAF migration target reference `ga-kaf-target.json`; readiness-role reference `ga-kaf-runtime.json`; infrastructure secret `ga-kaf.env`.
- Resolve current container address/target identity before access; internal IPs can change. Never substitute a guessed host port or arbitrary environment's DSN.

Additional task-owned databases `itsm_ga`, `itsm_ga_restore_verify`, `itsm_ga_ready_restore_verify` retain rehearsal/restore evidence. They are not task 2 targets and must not be dropped as cleanup. In particular, first rehearsal `itsm_ga` has an owner-only P037 receipt; retrofitting core grants would invalidate its evidence.

### Roles and what they actually prove

- `ga_runtime`: LOGIN, nonowner, no superuser/BYPASSRLS/CREATEDB/CREATEROLE/REPLICATION or memberships. SELECT/INSERT/UPDATE/DELETE on the four core tables (`tickets`, `incidents`, `problems`, `changes`) and their sequence privileges were declared **before** P037. CREATE in public, CREATE TEMP, ledger read/write reject with SQLSTATE 42501. Core RLS is enabled; FORCE RLS is false, so nonownership is material. No two-tenant business fixture was inserted; structural/privilege checks are not functional tenant-isolation acceptance.
- `ga_system`: separate restricted BYPASSRLS role, as required by the fixed code's runtime factory. SELECT on users, tenants, MSP allocations, process callback outboxes, external identities and connector configs; SELECT/UPDATE on outbox events and ticket notifications; audit INSERT and id SELECT/sequence USAGE. No owner fallback.
- `ga_inspection`: separate SELECT access to migration/evidence records. The actual fixed-source tenant/system factory and separate-inspector runtime admission both pass after identity preservation.
- `ga_kaf_runtime`: nonowner, no superuser/BYPASSRLS/create-role/create-db/replication/membership; SELECT only on `alembic_version`, `checkpoint_migrations`, `checkpoints`, `checkpoint_blobs`, `checkpoint_writes`. Actual application/checkpoint schema readiness passes; public/TEMP DDL and ledger UPDATE reject with 42501.
- These are verified schema-admission profiles. Full ITSM configuration/business-table runtime DML and KAF business/checkpoint write privileges are **not yet provisioned**. Before G-B/G-C application acceptance, task owners must submit the needed least-privilege capability manifest for review; no application may use owner credentials. Task 2's isolated migration writer can use `ga_owner` only for its admitted data batches. If its required runtime grants change G-A assumptions, return the delta to task 1 and publish a new GARevision before dependent acceptance.
- P037 fingerprints core-table ACLs and the reviewed grant manifest. Never edit those four ACLs, the control receipt or ledger to accommodate a later role profile. Other grants still require explicit review. Public database access including TEMP and public-schema CREATE were revoked on the actual ready targets.

## Actual initialization and verification

ITSM CLI reads CWD `config.yaml` (or `config/config.yaml`) and `.env`; there is no `-config` selector and arbitrary `DB_DSN` is not the connection contract. `database.User/Password` select the migration connection. Read-only checks used fixed binaries, reviewed CWD, `DB_SCHEMA=public` and lib/pq `PGOPTIONS` enforcing `default_transaction_read_only=on`; identity was checked first. `-status` does not create a missing ledger or run Ent schema initialization.

| Operation | Actual result / protected evidence |
| --- | --- |
| Source statuses, correct owner and forced read-only | Configuration 031 exit 0 with preparation/upgrade pending; old dev exit 1 checksum mismatch; populated ledgerless intake rejects. `status-owner-summary.json`, `status-owner-*/result.log` |
| Original candidate operator status | exit 0, no pending ordinary migrations, R038 pending_manual; `candidate-operator-status.log` |
| New empty ITSM bootstrap | `-fresh` exact development/target/destructive gates, exit 1 at genuine P037 barrier after 021; non-atomic behavior retained, not retried destructively. `ga-fresh-command.json`, `ga-fresh.log` |
| Pre-P backup and restore | First isolated snapshot verified across 149 tables; only five normalized CHECK casts differed. Ready-target backup restore has exact schema equality and matching rows, business count zero. `ga-ready-preparation.pgdump`, `ga-ready-restore-report.json` |
| Ready target P037 then ordinary upgrades | Real dry-run evidence and restore proof, `-prepare-workitem -evidence-file /operator/prepare-evidence.json`, then `-up`; exits 0. `ga-ready-prepare.log`, `ga-ready-up.log` |
| Final status / structure / factory+inspector | All exit 0 after Phase 1 data preservation: `final-itsm-status.log`, `final-itsm-runtime.log`, `final-itsm-roles.log`, timestamp in `final-checks.json` |
| KAF regression tests | 18 offline passed, 7 live tests passed; offline invocation skips live tests without explicit isolated URL; Ruff passed. `ga-kaf-live-tests.log`; independent source review of 69bd..23f |
| KAF actual provision and repeat | `acp.infrastructure.schema_management.provision_database(explicit_url)` uses frozen SQL bootstrap, 011 EHR first, Alembic head, application validation and PostgresSaver setup/checkpoint validation. Both initial and repeated provision passed. `ga-kaf-provision.log`, `ga-kaf-validation.json` |
| KAF model comparison | All 34 ORM tables' column types/nullability match, head 039 and checkpoint readiness passed; no ORM create_all or synthetic stamp used |
| Negative roles | `role-probes.json`: both roles deny DDL/TEMP/ledger writes; ITSM tenant role cannot read ledger; KAF schema validators pass under SELECT-only role |

Final check commands are recorded in protected `final_checks.py` (read-only checks only). It mounts the fixed migrate binary and source-compiled inspection helpers, with reviewed operator CWD. Do not rerun `advance_ready.py`, `ready_permissions.py`, `preserve_identity.py` or `role_probes.py` as a generic health check: they are historical write procedures, not idempotent status commands. KAF `kaf_validate.py` includes provision and must not be mistaken for a read-only probe.

The exact 36-entry target migration allowlist and full checksums are in `ga-itsm-ready-schema.json`, summarized below. P037 executes at its semantic barrier after 021, not simply in numeric order. R038 is absent and remains manual; no historical import/retirement was performed. Fresh application seed/config initialization was not run.

```text
007_add_change_execution_tables
008_add_initialization_ledger
009_enable_rls_tenant_isolation
011_add_tool_invocation_tenant_id
012_drop_service_catalog_item
013_service_request_delegates_to_ticket
014_drop_legacy_approval_workflow
015_process_instance_running_unique_guard
016_add_service_request_contact_fields
017_drop_ticket_type_legacy_approval_fields
018_convert_legacy_serial_ids_to_identity
019_kaf_execution_integrity_rls
020_work_item_number_allocator
021_add_callback_optional_declared
023_add_process_start_request_digest
024_incident_rule_action_receipts
025_email_attachment_source_identity
026_intake_actor_provenance
028_service_request_work_item_authority
029_catalog_target_class_authority
030_catalog_access_policy_result
031_kaf_action_request_digest
032_workitem_sla_cycle
033_incident_status_events
034_problem_investigation_completion
035_change_professional_evidence
036_intake_frozen_workflow_context
037_work_item_structure_preparation
039_candidate_execution_scope
040_sla_alert_notification_provenance
041_tool_invocation_execution_scope
042_tool_execution_authority_lock
043_tool_execution_authorization_lock
044_notification_connector_target
045_notification_email_target
046_auth_token_state
```

## Preserved Phase 1 data prerequisite

This is preservation of the **already migrated new ITSM** identity foundation from the user-confirmed configuration baseline, not another extraction/import from the legacy enterprise system. Protected `identity-snapshot/manifest.json` fixes the source snapshot (`114765:114765:`), 2026-09-14 17:22:57–17:22:58 CST, target, exact table/column allowlist, COPY SHA256, row fingerprints and sequence states.

| Table | Rows preserved |
| --- | ---: |
| tenants | 2 |
| departments | 7,975 |
| teams | 10 |
| groups | 3 |
| roles | 36 |
| permissions | 349 |
| permission_definitions | 0 |
| users | 7,862 |
| role_permissions | 1,414 |
| user_roles | 7 |
| msp_allocations | 0 |
| external_identities | 2 |
| department_tags | 0 |

All 13 tables had matching source/target column shapes; excluded FK references were checked and absent. A forced read-only REPEATABLE READ source transaction exported the fixed snapshot. One target transaction locked the empty allowlisted tables and restored rows with constraints active; IDs, password hashes and role relations were unchanged. Counts/fingerprints matched before commit. Sequence `setval` is nontransactional, so it was a separately recorded/checkpointed phase; captured states were checked against max IDs and target values. Sequence capture is operational, not falsely described as MVCC-consistent. Manifest states are `row_restore=committed_verified` and `sequence_restore=verified`.

Source user counts grew from the earlier inventory's 7,859 to this snapshot's 7,862. No source stop-write occurred; this is the task 2 reference snapshot, not final source freshness proof. Task 3 must re-establish final scope/cutoff and preserve allowed later changes under its plan.

Historical tickets, professional execution records, comments, attachments, old BPMN/instances and knowledge were excluded. Target core business/process-instance counts remain zero. Categories, catalog, SLA and required canonical process/config data have not been seeded: task 2 must map/admit those dependencies explicitly, without importing old BPMN or treating an empty target as functional acceptance. KAF target identity/execution counts remain zero; existing KAF execution state has not been moved.

A subsequent forced-read-only post-commit check (`verify_identity.py`, `final-identity-validation.json`) independently re-read all 13 table fingerprints, 11 sequence states and five empty business tables: PASS.

## Remaining differences and downstream boundaries

| Difference | Owner / effect |
| --- | --- |
| Historical dev ledger/provenance discrepancies | Block their in-place upgrade; retained sources and evidence. They do not require polluting the new target's ledger |
| Old KAF naive identity timestamp provenance | Blocks that source's in-place 039/data transfer until reviewed; task 3 cannot silently omit required identity/execution data |
| Runtime business privilege manifests and missing catalog/process configuration | Required before application-based G-B/G-C acceptance. Current G-A admits isolated migration/schema work only |
| PostgreSQL auth A3/A4 not connected to startup/login/refresh/logout/recovery | Remains open under original R4 plan, ITSM `96208aa147b685bf577fff5f07ed890bf5385baa`, `docs/superpowers/plans/2026-09-14-itsm-candidate-remaining-delivery.md`; 046 is not acceptance of those behaviors |
| WSL unexpected restart around 17:07 | No shutdown/restart command was issued by this task. New boot ID `0aaa41e4-47a7-4eab-ad9f-f304ad66e744`; cause unproven. Final container snapshot shows original candidate API/worker/web/ingress/PG and adjuncts Exited 255. Only task-owned GA PG was restarted; new GA KAF PG was created afterward. Shared application availability is not asserted |
| Joint runtime and physical consolidation | No new application started, no real KAF process contract accepted, no single-instance topology/cutover; task 3 retains these gates |

After this handoff's reviewed commit, task 1 releases `itsm_ga_ready` for task 2 as the single coordinated writer and will not mutate it concurrently. Task 2 must verify GARevision, fixed code/artifacts, live database identity, ledger/control evidence and preserved-reference snapshot before writing; prepare its own dry-run, backup and batch admission. Do not touch the other GA databases or sources. KAF target remains schema evidence unless a separately reviewed task needs it. Changing source/target data, schema, artifact or permissions in a way that affects this gate requires explicit delta review and a new revision; expected task 2 data batches are tracked by GBRevision and must revalidate upstream invariants.

## Evidence integrity and independent review

Raw evidence, backups, COPY data, config and secrets stay in the protected local root. The digest index below binds selected nonsecret reports plus the protected snapshot manifest to this handoff; the reports reference detailed inventories and row/file digests. Protected evidence is required to reproduce this local acceptance and must be retained through downstream handoff.

| Evidence under protected root | SHA256 |
| --- | --- |
| `summary.json` | `1546205ef8b2c4268b6dc0d2a5f94fdcbfb76a3a05906c6ad7af7511b0163cc8` |
| `runtime.json` | `e0ae2366ec1abc62888011298aa2cecb5f5e4b7b1627859be8deee048172f46a` |
| `ledger-differences.json` | `580bb7e8a42e52b699ba68d88c9c252a8338a1ee6ebc79f80823af5306fb63f3` |
| `kaf-source-schema.json` | `4d27be2007ee4805c0d43a58148eb6b4ee0800cc93335ce3c5c155ad378e5f99` |
| `kaf-data-preflight.json` | `2c081592302b1c0c3af69823d3ac76220edc8b454c4b6af9143da34d4fe64d93` |
| `ga-ready-restore-report.json` | `a093a07da4226cb86ebf6b91408e9ce29ea6faa8eed2292a9e83cf73eaf68093` |
| `ga-itsm-ready-schema.json` | `044d064d303dbca3d507c271cd86051f531076488cb9982881934827699c246f` |
| `identity-snapshot/manifest.json` | `2fbbad6e0dcb6f88b3b0f0a70cddc56cd2ececb54305836192746bbade5f99b9` |
| `ga-kaf-validation.json` | `9679f236790992c391fce0c652f9a226c304cbeec861dbab4c35df68c4f03382` |
| `role-probes.json` | `edd663204b3b4eeeca20f6203506a9c57e9f730c2a3116e31c30e80361ef8782` |
| `final-checks.json` | `d7712aa7ef3069273e2bbc833203fbc157d20ce35ff48a44d3f83d711fd7e196` |
| `final-container-status.log` | `e5f3a0248914026814927608b07b058223e8e2db98c90265872c1600d2c9f332` |
| `final-itsm-status.log` | `a2350439bbf43ed4f63b46322b54695912733d791f54dace02815f4127ccd627` |
| `final-itsm-runtime.log` | `edd7f1e7a2c4c51dcf4de2a0d1927a68c8821d6076c466a838fec847b13269f3` |
| `final-itsm-roles.log` | `cd7b19129539caa4202d2fff3e9194f5849556eb15aca0f37e92606c9d8ca9ed` |
| `ga-kaf-live-tests.log` | `e05675e677b0dfc0614671fc154ba2e7b5877c32f357b1a4814d2d5a1da415fe` |
| `final-identity-validation.json` | `b1707518a12cbdce45f72bfdb0a819c68bf398f0a8375a5e0ba53e51c1ea61fb` |

Independent pre-write/source reviews: `/root/ga_cli_readonly_audit` (CLI, restore semantics, P037/role admission, identity preservation), `/root/ga_kaf_039_review` (039 code and KAF isolated provision/role scope). Final whole-G-A review: `/root/ga_final_review`, 2026-09-14 17:35 CST, approved for G-A with no Critical/Important findings; verified all indexed digests, 36-entry allowlist, P037 backup/restore references, all 13 COPY digests and post-preservation checks. Minor requests to record this review and index the final identity report are incorporated. No main merge or production approval is implied by these reviews.