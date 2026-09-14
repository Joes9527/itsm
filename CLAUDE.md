# CLAUDE.md

## Required shared instructions

Read [AGENTS.md](AGENTS.md) before making repository changes. It is the authoritative product, architecture, security, and WorkItem contract for all coding agents. This file is a synchronized entry-point summary, not a second set of architecture or agent-specific engineering rules.

Also read [agent engineering governance](docs/agent-engineering-governance.md) and the documents required for the task in AGENTS.md's reading map. API/DTO, TypeScript/Ant Design, and file naming rules are shared in [engineering conventions](docs/engineering-conventions.md). Commands, testing procedures, migrations, and Docker operations are in [the development guide](docs/DEVELOPMENT_GUIDE.md) and [command reference](docs/dev-commands-reference.md). For maintained Windows/WSL instances, read [local environment](docs/development-environment.md) before operating services or data.

When architecture/domain constraints change, update AGENTS.md first and this summary in the same change. Update detailed engineering/operational rules in their shared source and link them; do not copy command tables, port defaults, release snapshots, or historical verification claims here.

## Architecture and design summary

- Build enterprise-grade, open-source, AI-Native ITSM for the China market, with private deployment and SaaS/MSP on one core model. Follow current roadmap and verification evidence, not a copied release status.
- Backend services own domain rules, authorization, tenant boundaries, workflow, and audit. Frontend, AI, KAF, connectors, plugins, and CLI use those boundaries. Follow each domain's existing legacy or vertical slice; do not add parallel endpoints or call another domain's repository implementation.
- Keep controllers thin and dependencies downward. Each field/concept has one authoritative owner. BPMN owns approval/fulfillment orchestration; professional services own their transitions. Preserve CMDB concerns and permission-filtered, attributed/versioned knowledge retrieval.
- Use configuration for real product variation and code/types for stable invariants. Add abstractions only for real responsibilities or boundaries. Split by cohesion and transaction ownership; avoid unrelated refactoring.
- Services own transactions. Reuse WorkItem intake/domain creators and existing outbox delivery. Define idempotency, replay conflicts, and concurrency protection. Commit, delivery, execution, verification, workflow termination, and professional completion are distinct; unknown results cannot be reported as success.
- Evolve public/persistence contracts through explicit migration and compatibility decisions with verification and retirement/remediation boundaries. Distinguish target architecture, code, and deployed/accepted behavior.
- Actor/tenant/privileged scope comes from trusted authentication and authorization. Tenant execution and restricted system capabilities remain separate. Enforce scope at services, associations, database, and jobs as well as HTTP. Mask secrets and audit high-risk actions with actor/source metadata.
- Unknown dispatch fails closed across workflows, connectors, skills, AI tools, and consumers. Optional steps must be declared in advance and their skips audited and observable.
- AI proposes; code applies policy and side effects. Use the existing gateway and versioned, auditable structured output; do not add parallel keyword classifiers or silent success fallbacks. KAF follows the [verified completion contract](docs/contracts/kaf-verified-access-completion.md).

## WorkItem summary

- WorkItem uses `tickets`; Ticket is the product name, not another lifecycle. Each professional extension has exactly one WorkItem created in the same transaction. Shared fields live only on WorkItem; extensions hold professional fields.
- `recordClass` is separate from subtype and immutable after extension creation. The full class vocabulary and channel-support caveat are in AGENTS.md; do not introduce a Release class by inference.
- Cross-domain relationships create target records and explicit relations, preserving source identity/history; they are not type conversions. Known Error and Catalog Item are not WorkItems. Catalog definitions, operational categories, and internal templates have separate responsibilities.
- Incident, Problem, Change, and Service Request services own their professional lifecycles; do not consolidate them into a giant `switch recordClass` state machine.
