# SSLVPN KAF Worker Production Readiness Evidence Report

> Status: Conditional No-Go — code and deployment definitions are verified; production-equivalent runtime and the controlled external-change rehearsal are not yet executed.
>
> Scope: SSLVPN Service Request → BPMN delegation → KAF → task-scoped ITSM completion. This report contains no payload, prompt, trace body, credential, recipient, or test-person identity.

## Verified locally

| Control | Evidence | Result |
|---|---|---|
| One KAF delivery owner | API no longer constructs or starts the KAF dispatcher; `kaf-worker` is a separate executable | Pass |
| Failure safety | Attempt marker, `blocked`, `delivery_unknown`, bounded retry and dead-letter tests | Pass |
| Worker isolation | Private health/metrics listener; no API router or host-published Worker port | Pass |
| Multi-replica definition | Compose Worker has no fixed container name; documented `--scale itsm-worker=2` | Pass (static) |
| Credential boundary | Runtime, migration, KAF webhook and KAF automation secrets use role-scoped secret-file mounts | Pass (static) |
| Database boundary | Production ITSM Compose uses an external logical database and distinct runtime/migration users | Pass (static) |
| KAF ingress | Gateway has an exact `/webhooks/itsm` route; KAF requires dedicated delegation URL/HMAC/token configuration | Pass (static); private HTTP ingress policy and TLS are deployment Backlog |
| SSLVPN chain | Targeted E2E, Service Request and service regressions | Pass |

## Local runtime inspection (2026-09-04)

The running local development stack was inspected without creating a Service
Request or invoking Microsoft Graph. The source repositories were first
fast-forwarded to ITSM `5b2dd2c6` and KAF `d07a178c`.

| Check | Observed evidence | Result |
|---|---|---|
| ITSM frontend | `GET http://localhost:3010/` and `GET /api/health` returned 200 with the expected HTML/JSON content | Pass |
| ITSM liveness | `GET http://localhost:8090/api/v1/health` and `/api/v1/healthz` returned 200 with `status=ok` | Pass |
| ITSM readiness | `GET http://localhost:8090/api/v1/readyz` returned 503; the ledger ends at schema migration `019_kaf_execution_integrity_rls` while `022_drop_professional_extension_shared_fields` is required | Fail |
| ITSM running artifact | The backend process was built and started before the latest Worker hard-cut commit was pulled | Fail |
| Delivery ownership | The running API had no `KAF_WEBHOOK_URL`, so its legacy dispatcher was disabled; no `kaf-worker` process or container was running | Fail: zero active delivery owners |
| Worker replicas | Docker/process inspection found zero Worker replicas; therefore two ready replicas and container-network-only health endpoints were not observable | Fail |
| KAF frontend/backend | `GET http://localhost:5173/` and `GET http://localhost:8000/health` returned 200 with expected content | Pass |
| KAF API documentation | `GET http://localhost:8000/docs` returned 404; current KAF source explicitly disables docs/OpenAPI, so this is expected and is not a health failure | Pass (expected disabled surface) |
| KAF delegation ingress | A deliberately invalid, unsigned delegation probe returned 503 `kaf_webhook_secret_not_configured` before parsing or persistence | Fail: delegation configuration absent |
| Static deployment contract | Production Compose renders an `itsm-worker` service without a fixed container name; the KAF exact-route contract tests pass | Pass (static only) |

The platform initialization ledger itself is complete at version `1.0.0`
(six of six required components). However, the repository migration status
command fails closed before it can apply the pending migrations. A complete
read-only ledger comparison found historical drift in
`007_add_change_execution_tables`, `009_enable_rls_tenant_isolation`, and
`015_process_instance_running_unique_guard`. The ledger also contains the
previously published `015_add_service_request_contact_fields`, which was later
renumbered to active migration `016` without being retained in the legacy
catalog. The migration stream requires an audited compatibility repair and a
forward application of the materially changed RLS/tenant behavior before
pending migrations can be applied; directly rewriting the database ledger or
running `-up` is unsafe.

No Worker was started because the local KAF HMAC/callback configuration was
absent and the ITSM database was not migration-ready. Starting a Worker would
not make the end-to-end path valid. Repairing the migration stream and applying
pending migrations are separate steps; the latter is a shared-database change
that requires explicit coordination.

### Executed commands

```text
go test ./tests/e2e -run '^TestSSLVPNScenarioE2E$' -count=1
go test ./handlers/service_request -run 'SSLVPN.*(KAF|Delegation)|KAF.*SSLVPN' -count=1
go test ./service -run 'KafOutboxDispatcher|KafDelegation|BPMNKafCompletion' -count=1
go test ./config ./internal/bootstrap ./internal/workerhealth ./cmd/kaf_worker ./service -count=1
docker compose -f docker-compose.prod.yml config --no-interpolate
go test ./config ./internal/bootstrap ./internal/workerhealth ./cmd/kaf_worker ./service ./handlers/delegated_execution ./pkg/seeder ./router -count=1
DEBUG=true ENV_FILE=/dev/null PYTHONPATH=src python -m pytest tests/test_kaf_delegation_contract.py -q
```

## Remaining release gates

| Gate | Required evidence | Owner/action |
|---|---|---|
| Runtime topology | API ready; two Workers ready; KAF gateway healthy; Worker port not externally reachable | Deployment operator |
| Local database initialization | Apply/record the required ITSM schema and baseline through the approved migration path, then obtain a 200 readiness response | Database operator |
| Local delegation configuration | Configure a dedicated shared HMAC secret and task-scoped KAF automation token, restart KAF, then start exactly two Workers | Deployment operator |
| External database | ITSM/KAF logical-database and runtime-role denial checks | Database operator |
| KAF deployment | Container Nginx syntax test and KAF delegation test suite in CI/image | KAF deployment operator |
| Alerting and Langfuse governance | Deferred Backlog by product decision; not implemented or counted as release evidence | Product/platform owner |
| Controlled change | Non-member baseline, one Graph add, replay idempotency, recovery to non-member | Designated change owner |

## Go/No-Go decision

**No-Go until every remaining gate has recorded evidence.** In particular, an unknown delivery may be reconciled but must never be force-requeued, a failed cleanup blocks release closure, and absence of a required health/metric/authorization result blocks the controlled change.
