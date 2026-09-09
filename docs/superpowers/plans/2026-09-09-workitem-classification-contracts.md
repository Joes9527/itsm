# WorkItem classification contract repair

Status: implemented

Goal: repair ticket/problem creation, incident/problem edit and the existing IncidentManagement modal after WorkItem classification authority cutover. Follow AGENTS.md and the unified WorkItem model design. Dependency: PR #13, already included in this branch from current origin/main. No schema migration, seeding, role grants, lifecycle rewrite or business-record repair.

1. Backend: problem creation and ticket creation accept shared `cti`; incident/problem update use `categoryId` (omitted: unchanged; 0: clear; positive: active tenant-owned node). Read DTOs expose authoritative categoryId. Persist only WorkItem category relation, resolve display names from metadata, and remove name-based edit writes. Cover duplicate names, omission, clearing, inactive/cross-tenant IDs and atomicity.
2. Frontend: extract shared tenant-aware classification field and helpers from incident creation. Creation sends CTI IDs; edit uses selected leaf categoryId with omission unless touched. Load existing selection from authoritative categoryId and tenant tree, not display labels. Repair ticket tree submission and remove legacy modal category inputs.
3. Verification: scoped Go packages/contracts/RBAC, frontend actual form submission tests, type check/build; independent review. Keep confirmed submission snapshots intact.
4. Delivery: independent PR stacked on PR #13, document contract. Local deployment only through isolated integration retaining existing menu/catalog fixes, with previous runtime/config backups and no auto migration/seed. Do not submit the user's live draft.

Validation: six scoped backend packages passed; 22 frontend tests, type check and production build passed. Independent review passed. Local integration preserved workflow/menu and catalog discovery changes; five integration-scope backend packages and the integrated production build passed. API health and login returned 200 after deployment. Authenticated browser submission remains unverified; no user draft was submitted.
