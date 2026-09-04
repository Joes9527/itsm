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

### Executed commands

```text
go test ./tests/e2e -run '^TestSSLVPNScenarioE2E$' -count=1
go test ./handlers/service_request -run 'SSLVPN.*(KAF|Delegation)|KAF.*SSLVPN' -count=1
go test ./service -run 'KafOutboxDispatcher|KafDelegation|BPMNKafCompletion' -count=1
go test ./config ./internal/bootstrap ./internal/workerhealth ./cmd/kaf_worker ./service -count=1
docker compose -f docker-compose.prod.yml config --no-interpolate
```

## Remaining release gates

| Gate | Required evidence | Owner/action |
|---|---|---|
| Runtime topology | API ready; two Workers ready; KAF gateway healthy; Worker port not externally reachable | Deployment operator |
| External database | ITSM/KAF logical-database and runtime-role denial checks | Database operator |
| KAF deployment | Container Nginx syntax test and KAF delegation test suite in CI/image | KAF deployment operator |
| Alerting and Langfuse governance | Deferred Backlog by product decision; not implemented or counted as release evidence | Product/platform owner |
| Controlled change | Non-member baseline, one Graph add, replay idempotency, recovery to non-member | Designated change owner |

## Go/No-Go decision

**No-Go until every remaining gate has recorded evidence.** In particular, an unknown delivery may be reconciled but must never be force-requeued, a failed cleanup blocks release closure, and absence of a required health/metric/authorization result blocks the controlled change.
