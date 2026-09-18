# Process binding deactivation

- Status: implemented in source; independent review and target deployment pending.
- Scope: approved narrow prerequisite for preserving and disabling legacy routing configurations. No data migration, replacement binding creation, reactivation, process termination or shared database write is included in this source change.

`POST /api/v1/process-bindings/:id/deactivate` accepts `reason` (nonblank, at most 4000 characters) and `expectedUpdatedAt` (the exact timestamp from the current binding GET). It retains the existing binding mutation role boundary: authenticated tenant actor, legacy BPMN role gate and `super_admin`. Tenant and actor are supplied by authentication, never by JSON.

The existing ProcessBindingService owns one transaction. A conditional update checks ID, tenant, active state and observed timestamp; it changes only `is_active` and `updated_at`. The existing AuditLog records actor, tenant, binding identity, prior active state, observed timestamp and reason in the same transaction. Audit failure rolls back deactivation. A concurrent edit returns conflict. An already inactive binding returns its current projection without another audit or timestamp mutation; this is state idempotence, not an operation receipt or an assertion that the caller performed the original change.

Legacy business type, definition key/version, subtype, priority, default, scope and policies remain unchanged. Create/update publication continues rejecting legacy business identities. No new schema or second audit mechanism is introduced. The command does not cancel existing processes or change definitions.

Implementation and verification sequence: real HTTP regression fails on missing route; add DTO/controller/service; verify preserved configuration, authority, missing inputs, stale timestamp, repeated deactivation and audit rollback; verify competing deactivation and an intervening edit on isolated PostgreSQL. Shared target deployment and configuration execution remain separate steps.
