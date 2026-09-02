# Unified Intake Diff Triage

**Source diff:** `git diff --name-status main...feat/kaf-delegation-transactional-delivery`
**Baseline:** `main` (merge-base with the feature branch)
**Total changed files:** 353
**Generated:** 2026-09-02, from worktree `/home/administrator/project/itsm/.claude/worktrees/unified-intake-p1-reconciliation`

**Note on Step 1's command:** the task brief said `cd /home/administrator/project/itsm` before running the diff. That directory is the shared main checkout; this session is isolated to the `unified-intake-p1-reconciliation` worktree, which shares the same `.git` object store, so `feat/kaf-delegation-transactional-delivery` is reachable without changing directory. The `cd` was skipped (attempting it is blocked for a worktree-isolated session in any case) and the diff was run from the worktree root with identical results.

Every one of the 353 lines below appears in exactly one of Category A, B, or C. 36 + 11 + 306 = 353.

---

## Category A: Intake-exclusive new files (port directly)

All 36 files match the brief's patterns: path starts with `itsm-backend/handlers/intake/`, or is `itsm-backend/ent/schema/external_identit*.go` / `itsm-backend/ent/schema/intake_*.go`. **Zero files matched** the `itsm-backend/migrations/02[0-2]_unified_intake|_work_item_authority|_external_identity` pattern — see note below.

```
A	itsm-backend/ent/schema/external_identity.go
A	itsm-backend/ent/schema/intake_request.go
A	itsm-backend/ent/schema/intake_resolution_snapshot.go
A	itsm-backend/handlers/intake/audit_repository.go
A	itsm-backend/handlers/intake/canonicalize.go
A	itsm-backend/handlers/intake/canonicalize_test.go
A	itsm-backend/handlers/intake/command.go
A	itsm-backend/handlers/intake/creator.go
A	itsm-backend/handlers/intake/creator_test.go
A	itsm-backend/handlers/intake/e2e_test.go
A	itsm-backend/handlers/intake/errors.go
A	itsm-backend/handlers/intake/handler.go
A	itsm-backend/handlers/intake/handler_test.go
A	itsm-backend/handlers/intake/idempotency_postgres_test.go
A	itsm-backend/handlers/intake/idempotency_repository.go
A	itsm-backend/handlers/intake/idempotency_repository_test.go
A	itsm-backend/handlers/intake/identity.go
A	itsm-backend/handlers/intake/identity_exchange.go
A	itsm-backend/handlers/intake/identity_exchange_test.go
A	itsm-backend/handlers/intake/identity_mapping_handler.go
A	itsm-backend/handlers/intake/identity_mapping_handler_test.go
A	itsm-backend/handlers/intake/identity_test.go
A	itsm-backend/handlers/intake/incident_creator.go
A	itsm-backend/handlers/intake/metrics.go
A	itsm-backend/handlers/intake/metrics_test.go
A	itsm-backend/handlers/intake/postgres_integration_test.go
A	itsm-backend/handlers/intake/resolver.go
A	itsm-backend/handlers/intake/resolver_test.go
A	itsm-backend/handlers/intake/service.go
A	itsm-backend/handlers/intake/service_request_creator.go
A	itsm-backend/handlers/intake/service_test.go
A	itsm-backend/handlers/intake/snapshot_repository.go
A	itsm-backend/handlers/intake/transaction_contributors_test.go
A	itsm-backend/handlers/intake/work_item_creator.go
A	itsm-backend/handlers/intake/workflow_intervention_handler.go
A	itsm-backend/handlers/intake/workflow_intervention_handler_test.go
```

**Migrations-pattern note:** this repository does not keep a top-level `itsm-backend/migrations/02[0-2]_*` directory of loose numbered files that a glob could match — post-schema migration bodies are registered as Go `case` blocks inside `itsm-backend/migration/migrations.go` (Category B, below) and the plain `.sql`/`_dev_reset.sql`/`_verify.sql` files under `itsm-backend/migrations/` are written fresh per-task rather than carried over file-for-file. Confirmed against the full 15-task plan (`docs/superpowers/plans/2026-09-02-unified-intake-p1-reconciliation.md`, Task 2): the source branch's migrations were numbered `020`/`021`/`022`, and Task 2 renumbers/splits them into `023_unified_intake_rls` and `025_external_identity_version` (with `024_service_catalog_target_class_authority` deferred to Task 14) by extracting SQL bodies out of `migration/migrations.go` with `git show`, not by copying files that don't exist in this diff. This is not a gap in the diff — it's an expected structural mismatch between the brief's illustrative pattern and how this repo actually stores migrations.

---

## Category B: Existing files with real conflicts (hand-reconcile, per plan)

All 6 explicitly-named files in the brief, plus the `itsm-backend/ent/schema/*.go` glob expanded to the files it actually matches (excluding the 3 new schema files already claimed by Category A above).

```
M	itsm-backend/controller/incident_controller.go
M	itsm-backend/ent/schema/incident.go
M	itsm-backend/ent/schema/outbox_event.go
M	itsm-backend/ent/schema/servicecatalog.go
M	itsm-backend/ent/schema/servicerequest.go
M	itsm-backend/ent/schema/ticket.go
M	itsm-backend/handlers/service_catalog/repository_impl.go
M	itsm-backend/handlers/service_request/entity.go
M	itsm-backend/handlers/service_request/service.go
M	itsm-backend/migration/migrations.go
M	itsm-backend/service/bpmn/incident_handler.go
```

All 6 explicitly-named files were verified present as `M` in the raw diff — none were missing or unchanged.

**Note (brief's heading text vs. actual plan):** the brief's Category B heading reads "hand-reconcile, see Tasks 5-16", but the full plan document (`docs/superpowers/plans/2026-09-02-unified-intake-p1-reconciliation.md`) only defines 15 tasks (Task 1 through Task 15). Preserved the heading verbatim per the brief; flagging the off-by-one in case it's a stale reference from an earlier plan draft, not something for this task to fix.

**Additional in-scope files found by cross-checking the full plan (not in the brief's Category B list, but confirmed as real hand-reconcile targets by the plan's own File Structure table / task `git add` commands):**

| File (from Category C below) | Task that actually touches it |
|---|---|
| `itsm-backend/service/incident_service.go` | Tasks 5, 6 (export `GenerateIncidentNumber`/`ResolveIncidentPriority`) |
| `itsm-backend/handlers/service_catalog/service.go`, `handler.go`, `handler_test.go`, `service_test.go`, `repository_impl_test.go` | Task 14 (target_class API contract) |
| `itsm-backend/dto/service_dto.go` | Task 14 |
| `itsm-backend/handlers/service_request/handler.go`, `handler_test.go`, `regression_test.go` | Task 13 |
| `itsm-backend/tests/integration/service_catalog_fields_test.go`, `itsm-backend/tests/e2e/sslvpn_scenario_test.go` | Task 13 (trailing-arg signature update only) |
| `itsm-backend/internal/bootstrap/app.go` | Tasks 9, 10, 11, 12, 13 (constructor wiring, multiple call sites) |
| `itsm-frontend/src/lib/api/incident-api.ts`, `service-catalog-api.ts` | Task 10, Task 14 |
| `itsm-frontend/src/lib/hooks/useServiceCatalog.ts` | Task 10 |
| `itsm-frontend/src/app/(main)/service-catalog/components/CreateServiceModal.tsx`, `.../edit/[id]/page.tsx` | Task 14 (covered by that task's `git add .../service-catalog` directory glob) |
| `itsm-backend/controller/incident_controller_test.go`, `incident_intake_adapter_test.go` | Task 11 |
| `itsm-backend/service/bpmn/incident_handler_test.go` | Task 12 |

These remain listed under Category C below (they matched none of the brief's literal Category A/B patterns), but they are **not** "unrelated historical divergence" — later tasks touch them directly, just via fresh TDD-written diffs rather than by porting this branch's version of the file. Left in place in C per the brief's literal matching rule; called out here so Task 1's report doesn't imply they're safe to ignore.

---

## Category C: Unrelated historical divergence (do not port)

All 306 remaining lines, i.e. every line not captured by Category A or B above.

```
M	.env.example
M	CHANGELOG.md
M	docker-compose.dev.yml
M	docker-compose.prod.yml
M	docker-compose.yml
M	docs/DEVELOPMENT_GUIDE.md
M	docs/api/API_REFERENCE.md
M	docs/architecture-product-assessment-2026-08-30.md
M	docs/archive/testing-reports/BPMN 整改遗留项 E2E 测试计划.md
M	docs/dev-commands-reference.md
M	docs/e2e-testing-guide.md
M	docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md
A	docs/reports/2026-08-31-unified-intake-implementation-report.md
M	docs/roadmap.md
M	docs/superpowers/plans/2026-08-30-kaf-delegation-execution-integrity.md
M	docs/superpowers/plans/2026-08-31-kaf-delegation-release-closeout.md
A	docs/superpowers/plans/2026-08-31-unified-intake.md
M	docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md
M	docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md
M	docs/superpowers/specs/2026-08-31-kaf-delegation-release-closeout-design.md
A	docs/superpowers/specs/2026-08-31-unified-intake-design.md
M	docs/testing/kaf-delegation-release-closeout-fixture.md
M	itsm-backend/cmd/backfill_change_work_item/main.go
M	itsm-backend/cmd/backfill_incident_comments/main.go
M	itsm-backend/cmd/backfill_incident_comments/main_test.go
D	itsm-backend/cmd/backfill_incident_work_item/main.go
D	itsm-backend/cmd/backfill_incident_work_item/main_test.go
M	itsm-backend/cmd/backfill_problem_work_item/main.go
M	itsm-backend/cmd/backfill_problem_work_item/main_test.go
D	itsm-backend/cmd/backfill_servicecatalog_target_class/main.go
D	itsm-backend/cmd/backfill_servicecatalog_target_class/main_test.go
M	itsm-backend/cmd/check_work_item_integrity/main.go
M	itsm-backend/cmd/check_work_item_integrity/main_test.go
M	itsm-backend/common/response.go
M	itsm-backend/config/config.go
A	itsm-backend/config/kaf_intake_config_test.go
A	itsm-backend/config/workflow_outbox_config_test.go
M	itsm-backend/controller/global_search_controller.go
M	itsm-backend/controller/global_search_controller_test.go
M	itsm-backend/controller/incident_controller_test.go
A	itsm-backend/controller/incident_intake_adapter_test.go
M	itsm-backend/controller/kaf_delegation_controller_test.go
M	itsm-backend/database/rls/rls.go
M	itsm-backend/database/rls/rls_integration_test.go
M	itsm-backend/database/rls/rls_test.go
M	itsm-backend/dto/mappers.go
M	itsm-backend/dto/service_dto.go
M	itsm-backend/ent/application_create.go
M	itsm-backend/ent/approvalchain_create.go
M	itsm-backend/ent/asset_create.go
M	itsm-backend/ent/assetlicense_create.go
M	itsm-backend/ent/auditlog_create.go
M	itsm-backend/ent/bootstraptoken_create.go
M	itsm-backend/ent/bpmnpermission_create.go
M	itsm-backend/ent/cabmember_create.go
M	itsm-backend/ent/change_create.go
M	itsm-backend/ent/changepir_create.go
M	itsm-backend/ent/ciattributedefinition_create.go
M	itsm-backend/ent/cirelationship_create.go
M	itsm-backend/ent/citag_create.go
M	itsm-backend/ent/citype_create.go
M	itsm-backend/ent/client.go
M	itsm-backend/ent/cloudaccount_create.go
M	itsm-backend/ent/cloudresource_create.go
M	itsm-backend/ent/cloudservice_create.go
M	itsm-backend/ent/cmdbexporttask_create.go
M	itsm-backend/ent/cmdbimporttask_create.go
M	itsm-backend/ent/cmdbsavedview_create.go
M	itsm-backend/ent/configurationitem_create.go
M	itsm-backend/ent/configurationitemhistory_create.go
M	itsm-backend/ent/connectorconfig_create.go
M	itsm-backend/ent/contract_create.go
M	itsm-backend/ent/conversation_create.go
M	itsm-backend/ent/department_create.go
M	itsm-backend/ent/discoveryjob_create.go
M	itsm-backend/ent/discoveryresult_create.go
M	itsm-backend/ent/discoverysource_create.go
M	itsm-backend/ent/domainconfig_create.go
M	itsm-backend/ent/endpointacl_create.go
M	itsm-backend/ent/engineerskill_create.go
M	itsm-backend/ent/ent.go
A	itsm-backend/ent/externalidentity.go
A	itsm-backend/ent/externalidentity/externalidentity.go
A	itsm-backend/ent/externalidentity/where.go
A	itsm-backend/ent/externalidentity_create.go
A	itsm-backend/ent/externalidentity_delete.go
A	itsm-backend/ent/externalidentity_query.go
A	itsm-backend/ent/externalidentity_update.go
M	itsm-backend/ent/feishuticketsync_create.go
M	itsm-backend/ent/fielddefinition_create.go
M	itsm-backend/ent/fieldvalue_create.go
M	itsm-backend/ent/generate.go
M	itsm-backend/ent/group_create.go
M	itsm-backend/ent/hook/hook.go
M	itsm-backend/ent/incident.go
M	itsm-backend/ent/incident/incident.go
M	itsm-backend/ent/incident/where.go
A	itsm-backend/ent/incident/work_item_predicates.go
M	itsm-backend/ent/incident_create.go
M	itsm-backend/ent/incident_query.go
M	itsm-backend/ent/incident_update.go
M	itsm-backend/ent/incidentalert_create.go
M	itsm-backend/ent/incidentescalationrule_create.go
M	itsm-backend/ent/incidentevent_create.go
M	itsm-backend/ent/incidentmetric_create.go
M	itsm-backend/ent/incidentrule_create.go
M	itsm-backend/ent/incidentruleexecution_create.go
A	itsm-backend/ent/intakerequest.go
A	itsm-backend/ent/intakerequest/intakerequest.go
A	itsm-backend/ent/intakerequest/where.go
A	itsm-backend/ent/intakerequest_create.go
A	itsm-backend/ent/intakerequest_delete.go
A	itsm-backend/ent/intakerequest_query.go
A	itsm-backend/ent/intakerequest_update.go
A	itsm-backend/ent/intakeresolutionsnapshot.go
A	itsm-backend/ent/intakeresolutionsnapshot/intakeresolutionsnapshot.go
A	itsm-backend/ent/intakeresolutionsnapshot/where.go
A	itsm-backend/ent/intakeresolutionsnapshot_create.go
A	itsm-backend/ent/intakeresolutionsnapshot_delete.go
A	itsm-backend/ent/intakeresolutionsnapshot_query.go
A	itsm-backend/ent/intakeresolutionsnapshot_update.go
M	itsm-backend/ent/itemversion_create.go
M	itsm-backend/ent/kaftaskactionledger_create.go
M	itsm-backend/ent/kaftaskcompletionreceipt_create.go
M	itsm-backend/ent/knowledgearticle_create.go
M	itsm-backend/ent/knowledgearticlelike_create.go
M	itsm-backend/ent/knowledgearticleparticipant_create.go
M	itsm-backend/ent/knowledgearticlesession_create.go
M	itsm-backend/ent/knowledgearticleversion_create.go
M	itsm-backend/ent/knownerror_create.go
M	itsm-backend/ent/marketplaceitem_create.go
M	itsm-backend/ent/menu_create.go
M	itsm-backend/ent/message_create.go
M	itsm-backend/ent/microservice_create.go
M	itsm-backend/ent/migrate/schema.go
M	itsm-backend/ent/mspallocation_create.go
M	itsm-backend/ent/mutation.go
M	itsm-backend/ent/notification_create.go
M	itsm-backend/ent/notificationpreference_create.go
M	itsm-backend/ent/outboxevent.go
M	itsm-backend/ent/outboxevent_create.go
M	itsm-backend/ent/passwordresettoken_create.go
M	itsm-backend/ent/permission_create.go
M	itsm-backend/ent/permissiondefinition_create.go
M	itsm-backend/ent/predicate/predicate.go
M	itsm-backend/ent/problem_create.go
M	itsm-backend/ent/processapprovaldecision_create.go
M	itsm-backend/ent/processauditlog_create.go
M	itsm-backend/ent/processbinding_create.go
M	itsm-backend/ent/processdefinition_create.go
M	itsm-backend/ent/processdeployment_create.go
M	itsm-backend/ent/processexecutionhistory_create.go
M	itsm-backend/ent/processinstance_create.go
M	itsm-backend/ent/processtask_create.go
M	itsm-backend/ent/processvariable_create.go
M	itsm-backend/ent/processversionchangelog_create.go
M	itsm-backend/ent/project_create.go
M	itsm-backend/ent/prompttemplate_create.go
M	itsm-backend/ent/provisioningtask_create.go
M	itsm-backend/ent/relationshiptype_create.go
M	itsm-backend/ent/release_create.go
M	itsm-backend/ent/role_create.go
M	itsm-backend/ent/rolepermission_create.go
M	itsm-backend/ent/rootcauseanalysis_create.go
M	itsm-backend/ent/runtime.go
M	itsm-backend/ent/servicecatalog.go
M	itsm-backend/ent/servicecatalog/servicecatalog.go
M	itsm-backend/ent/servicecatalog/where.go
M	itsm-backend/ent/servicecatalog_create.go
M	itsm-backend/ent/servicecatalog_update.go
M	itsm-backend/ent/servicerequest.go
M	itsm-backend/ent/servicerequest/servicerequest.go
M	itsm-backend/ent/servicerequest/where.go
M	itsm-backend/ent/servicerequest_create.go
M	itsm-backend/ent/servicerequest_query.go
M	itsm-backend/ent/servicerequest_update.go
M	itsm-backend/ent/slaalerthistory_create.go
M	itsm-backend/ent/slaalertrule_create.go
M	itsm-backend/ent/sladefinition_create.go
M	itsm-backend/ent/slametric_create.go
M	itsm-backend/ent/slaviolation_create.go
M	itsm-backend/ent/standardchange_create.go
M	itsm-backend/ent/survey_create.go
M	itsm-backend/ent/surveyresponse_create.go
M	itsm-backend/ent/systemconfig_create.go
M	itsm-backend/ent/tag_create.go
M	itsm-backend/ent/team_create.go
M	itsm-backend/ent/tenant_create.go
M	itsm-backend/ent/tenantinstallation_create.go
M	itsm-backend/ent/ticket.go
M	itsm-backend/ent/ticket/ticket.go
M	itsm-backend/ent/ticket/where.go
M	itsm-backend/ent/ticket_create.go
M	itsm-backend/ent/ticket_query.go
M	itsm-backend/ent/ticket_update.go
M	itsm-backend/ent/ticketapproval_create.go
M	itsm-backend/ent/ticketassignmentrule_create.go
M	itsm-backend/ent/ticketattachment_create.go
M	itsm-backend/ent/ticketautomationrule_create.go
M	itsm-backend/ent/ticketcategory_create.go
M	itsm-backend/ent/ticketcc_create.go
M	itsm-backend/ent/ticketcomment_create.go
M	itsm-backend/ent/ticketnotification_create.go
M	itsm-backend/ent/tickettag_create.go
M	itsm-backend/ent/tickettemplate_create.go
M	itsm-backend/ent/tickettype_create.go
M	itsm-backend/ent/ticketview_create.go
M	itsm-backend/ent/ticketworkflowrecord_create.go
M	itsm-backend/ent/toolinvocation_create.go
M	itsm-backend/ent/tx.go
M	itsm-backend/ent/user_create.go
M	itsm-backend/ent/vendor_create.go
M	itsm-backend/ent/workflow_create.go
M	itsm-backend/ent/workflowinstance_create.go
M	itsm-backend/ent/workflowtask_create.go
M	itsm-backend/ent/workflowversion_create.go
M	itsm-backend/ent/workitemrelation_create.go
M	itsm-backend/go.mod
M	itsm-backend/handlers/change/service.go
M	itsm-backend/handlers/problem/conversion.go
M	itsm-backend/handlers/problem/conversion_test.go
M	itsm-backend/handlers/problem/repository_impl.go
M	itsm-backend/handlers/problem/service_test.go
M	itsm-backend/handlers/service_catalog/entity.go
M	itsm-backend/handlers/service_catalog/handler.go
M	itsm-backend/handlers/service_catalog/handler_test.go
M	itsm-backend/handlers/service_catalog/repository_impl_test.go
M	itsm-backend/handlers/service_catalog/service.go
M	itsm-backend/handlers/service_catalog/service_test.go
M	itsm-backend/handlers/service_request/handler.go
M	itsm-backend/handlers/service_request/handler_test.go
A	itsm-backend/handlers/service_request/intake_adapter_test.go
M	itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go
M	itsm-backend/handlers/service_request/regression_test.go
M	itsm-backend/handlers/service_request/repository_impl.go
M	itsm-backend/handlers/service_request/repository_impl_test.go
M	itsm-backend/handlers/service_request/service_test.go
M	itsm-backend/integration/extended_integration_test.go
M	itsm-backend/internal/bootstrap/app.go
M	itsm-backend/internal/bootstrap/kaf_outbox_lifecycle_test.go
M	itsm-backend/internal/bootstrap/post_schema_migrations_test.go
M	itsm-backend/middleware/auth.go
A	itsm-backend/middleware/intake_auth.go
A	itsm-backend/middleware/intake_auth_test.go
M	itsm-backend/middleware/security.go
M	itsm-backend/migration/migrator_test.go
M	itsm-backend/pkg/seeder/seeder.go
M	itsm-backend/pkg/seeder/seeder_test.go
A	itsm-backend/router/intake_identity_routes_test.go
M	itsm-backend/router/router.go
M	itsm-backend/router/router_test.go
M	itsm-backend/service/bpmn/incident_handler_test.go
M	itsm-backend/service/bpmn/service_request_handler.go
M	itsm-backend/service/bpmn/service_request_handler_test.go
M	itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn
M	itsm-backend/service/bpmn_approval_bridge_service_test.go
M	itsm-backend/service/bpmn_platform_tenant_test.go
M	itsm-backend/service/bpmn_process_binding_service.go
A	itsm-backend/service/bpmn_process_binding_service_test.go
M	itsm-backend/service/bpmn_process_engine.go
M	itsm-backend/service/bpmn_process_engine_ext_test.go
M	itsm-backend/service/bpmn_process_engine_test.go
M	itsm-backend/service/ci_relationship_service.go
M	itsm-backend/service/ci_relationship_service_test.go
M	itsm-backend/service/dashboard_service.go
A	itsm-backend/service/database_conflict.go
A	itsm-backend/service/database_conflict_test.go
M	itsm-backend/service/embed_pipeline.go
M	itsm-backend/service/field_value_service.go
M	itsm-backend/service/field_value_service_test.go
M	itsm-backend/service/incident_alerting_service.go
M	itsm-backend/service/incident_authorization.go
M	itsm-backend/service/incident_authorization_test.go
M	itsm-backend/service/incident_automation_service_test.go
M	itsm-backend/service/incident_escalation_service.go
M	itsm-backend/service/incident_monitoring_service.go
M	itsm-backend/service/incident_rule_engine.go
M	itsm-backend/service/incident_service.go
M	itsm-backend/service/incident_service_test.go
A	itsm-backend/service/incident_test_builder_test.go
M	itsm-backend/service/kaf_delegation_service.go
M	itsm-backend/service/outbox_event_repository.go
M	itsm-backend/service/outbox_event_repository_test.go
M	itsm-backend/service/provisioning_service.go
M	itsm-backend/service/provisioning_service_test.go
M	itsm-backend/service/root_cause_analysis_service.go
M	itsm-backend/service/tenant_aware_repository.go
M	itsm-backend/service/ticket_workflow_service.go
A	itsm-backend/service/workflow_start_outbox_dispatcher.go
A	itsm-backend/service/workflow_start_outbox_dispatcher_test.go
M	itsm-backend/tests/e2e/sslvpn_scenario_test.go
M	itsm-backend/tests/fixtures/sslvpn_fixtures.go
M	itsm-backend/tests/fixtures/sslvpn_fixtures_test.go
M	itsm-backend/tests/integration/service_catalog_fields_test.go
A	itsm-backend/tests/testutil/work_item.go
M	itsm-frontend/src/app/(main)/admin/service-catalogs/page.tsx
M	itsm-frontend/src/app/(main)/service-catalog/components/CreateServiceModal.tsx
M	itsm-frontend/src/app/(main)/service-catalog/edit/[id]/page.tsx
A	itsm-frontend/src/lib/api/__tests__/idempotency-key.test.ts
M	itsm-frontend/src/lib/api/__tests__/incident-api.test.ts
M	itsm-frontend/src/lib/api/__tests__/service-catalog-api.test.ts
A	itsm-frontend/src/lib/api/idempotency-key.ts
M	itsm-frontend/src/lib/api/incident-api.ts
M	itsm-frontend/src/lib/api/service-catalog-api.ts
M	itsm-frontend/src/lib/hooks/useServiceCatalog.ts
M	itsm-frontend/src/types/service-catalog.ts
```

### Notes on Category C clusters

**1. Generated Ent code (170 files) — mechanical, not "unrelated" in origin, but not hand-copied either.**
Every `itsm-backend/ent/*_create.go` for pre-existing entities, plus `client.go`, `mutation.go`, `runtime.go`, `tx.go`, `ent.go`, `hook/hook.go`, `predicate/predicate.go`, `migrate/schema.go`, and the new generated packages for `externalidentity`/`intakerequest`/`intakeresolutionsnapshot`, all changed because `itsm-backend/ent/generate.go` gained a codegen feature flag on the source branch:
```diff
-//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate ./schema
+//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./schema
```
This single flag change is what regenerates `OnConflict`/upsert support into every entity's `_create.go` — confirmed by diffing a representative file (`ent/application_create.go`: adds `conflict []sql.ConflictOption` field and `OnConflict(...)` method, unrelated to `application`'s own schema). `entgo.io/ent` itself did not change version (`go.mod` still pins `v0.14.6`); only `github.com/DATA-DOG/go-sqlmock` was newly added as a test dependency. The full plan's Task 3 Step 2 (`go generate ./ent` after porting Category A/B schema files) is exactly how this cluster gets reproduced on the reconciled branch — none of these 170 files should be hand-copied from the feature branch (their content also reflects the source branch's now-superseded schema/history, not the reconciled one).

**2. Intake/idempotency-named files that will be re-derived, not ported.**
`itsm-backend/config/kaf_intake_config_test.go`, `itsm-backend/controller/incident_intake_adapter_test.go`, `itsm-backend/middleware/intake_auth.go` (+ test), `itsm-backend/router/intake_identity_routes_test.go`, `itsm-backend/handlers/service_request/intake_adapter_test.go`, and `itsm-frontend/src/lib/api/idempotency-key.ts` (+ test) all have intake/idempotency-suggestive names but don't match the brief's Category A regex. Cross-checked against the full plan: none of these are ported verbatim. Task 10 in particular writes its **own** frontend key generator at a different path (`itsm-frontend/src/lib/utils/idempotencyKey.ts`), not by reusing this branch's `lib/api/idempotency-key.ts` — confirmed by that task's file list and `git add` command. Correctly Category C ("do not port") even though topically related.

**3. `itsm-backend/handlers/change/service.go` — small but real conflict coupled to Category B.**
Only a 2-line diff, but it's a direct consequence of the `ent/schema/incident.go` shared-field removal already in Category B: `incident.TenantID(...)` → `incident.OwnedByTenant(...)`, `incident.StatusNotIn(...)` → `incident.WorkItemStatusNotIn(...)`. Whoever ports/regenerates `ent/schema/incident.go` should confirm this call site still compiles — it isn't itself in the brief's Category B list, and it isn't touched by any task's file list in the full plan either, which is worth a second look when Task 5+ regenerates the Incident predicates.

**4. `cmd/backfill_*` churn — pre-existing WorkItem-parity cleanup, not intake.**
`backfill_incident_work_item` and `backfill_servicecatalog_target_class` are deleted on the feature branch; `backfill_change_work_item`, `backfill_incident_comments`, `backfill_problem_work_item`, `check_work_item_integrity` are modified. These track the already-separate WorkItem-parity backfill effort. One discrepancy worth flagging for Task 14: the feature branch **deletes** `cmd/backfill_servicecatalog_target_class`, but Task 14 Step 6 in the full plan says to "confirm retirement eligibility" of that same command on `main` (where it still exists) before deleting it — the two branches reached the same end state (delete it) via different reasoning, not a real conflict to hand-reconcile, but Task 14 should do its own `rg` check rather than assume the feature branch's deletion rationale applies unchanged.

**5. Frontend service-catalog admin files not in any task's list.**
`itsm-frontend/src/app/(main)/admin/service-catalogs/page.tsx` and `itsm-frontend/src/types/service-catalog.ts` are modified on the feature branch but not named by any task's Files/Interfaces section or `git add` command in the full plan (Task 14's `git add` targets the `.../service-catalog/` directory glob, which covers `CreateServiceModal.tsx` and `edit/[id]/page.tsx` — see the Category B addendum table above — but not `admin/service-catalogs/page.tsx`, a different path). Worth a second look during Task 14 in case the admin list page also needs a `targetClass` column/control; not something to resolve in this triage task.

**6. Everything else — docs, config, and out-of-plan backend churn.**
`.env.example`, `CHANGELOG.md`, `docker-compose*.yml`, all `docs/**` (including the source branch's own SDD planning docs for "unified intake" and "kaf-delegation-release-closeout" — superseded by this reconciliation's own plan/spec), `database/rls/*`, `dto/mappers.go`, `pkg/seeder/*`, `router/router.go`, `middleware/auth.go`, `middleware/security.go`, `service/bpmn_process_engine*.go`, `service/ci_relationship_service*.go`, `service/dashboard_service.go`, `service/kaf_delegation_service.go`, `service/outbox_event_repository*.go`, `service/provisioning_service*.go`, `service/tenant_aware_repository.go`, `service/workflow_start_outbox_dispatcher*.go`, `service/database_conflict*.go`, the `tests/fixtures/sslvpn_*` / `tests/e2e/sslvpn_scenario_test.go` SSLVPN fixture set (only its two call-site argument additions matter to Task 13, per the addendum table above — the bulk of its content is unrelated), and the frontend `__tests__` files not named in Task 10 are all prior "KAF delegation" feature work, WorkItem/RLS/outbox hardening, or genuinely stale process docs from the source branch's own history — none of it is named anywhere in the 15-task reconciliation plan's File Structure table or task-level `git add` commands. Matches the brief's own prediction for Category C ("stale test scripts, docs, and any file not touched by Category A or B").

---

## Self-Review

- Every one of the 353 raw diff lines was assigned to exactly one category: 36 (A) + 11 (B) + 306 (C) = 353. Verified programmatically (Python classification script matching the brief's literal patterns), then the "Category C" bucket was read in full and manually clustered/annotated above.
- All 6 files explicitly named in the brief's Category B list were confirmed present as `M` in the raw diff (none fabricated, none missing). The `ent/schema/*.go` glob bullet was confirmed to match 8 real changed files, 3 of which are already claimed by Category A's more specific patterns (`external_identity.go`, `intake_request.go`, `intake_resolution_snapshot.go`) and are listed there instead, per the brief's own precedence (a file matching the narrower Category A pattern is Category A, not also re-listed under the glob).
- The brief's `itsm-backend/migrations/02[0-2]_*` Category A pattern matched zero files — verified this is a structural fact about how this repo registers migrations (in `migration/migrations.go`, already Category B), not a missed file; explained under Category A.
- Went beyond the brief's literal template to (a) identify and explain the 170-file generated-Ent-code cluster caused by a single `ent/generate.go` codegen flag change, and (b) cross-reference the full 15-task plan document to flag ~15 files in Category C that later tasks do touch (via fresh TDD-written code, not by porting the branch's version) so downstream readers don't mistake "Category C: do not port" for "Category C: irrelevant." This required reading the full plan (`docs/superpowers/plans/2026-09-02-unified-intake-p1-reconciliation.md`, 2869 lines) beyond just this task's brief, since the brief's own Category B list is illustrative/partial relative to the plan's actual per-task File Structure table.

## Concerns

- The brief's Category B list, taken literally, under-represents the plan's real hand-reconciliation surface (see the addendum table under Category B). This triage report flags the gap, but does not resolve it — later tasks' own Files sections in the full plan remain authoritative for their scope.
- `itsm-frontend/src/app/(main)/admin/service-catalogs/page.tsx` and `itsm-frontend/src/types/service-catalog.ts` change on the feature branch but appear in no task's file list — possible small gap in Task 14's scope, flagged for that task's owner to verify rather than assumed here.
