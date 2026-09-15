# GA workbench client contract adaptation

## Fixed sources and scope

- Frontend base: `c33479922e2f41c09e7d66ab7af957bc031de72c` (`ui-workbench-runtime`), frontend tree identical to deployed `80349336de3dbe5f7be6c5cc57b3f1783a8ebb6b`.
- Existing deployed build: `wlcY0limNxk71JkwpGrDC`; deployment evidence: `/home/administrator/.local/state/itsm-kaf-baseline-20260908/evidence/ui-workbench-deployment.json`.
- Backend contract source: `94b30926`. This change preserves the current workbench; it does not replace frontend files with that backend branch's UI.
- Branch: `codex/fix/ga-workbench-client-contract`.

## Behavior

- Process task queries use canonical record classes while retaining existing pagination, identity checks, permission denial, task claim/complete, and session isolation behavior.
- Ticket edit/escalation API uses required observed version and caller-owned operation ID. `ticket-edit.ts` is reused verbatim from the backend contract branch's existing frontend helper.
- Ticket detail edits/AI acceptance retain confirmed commands across uncertain failures and fetch detail after a command receipt. Explicit version conflicts discard the rejected intent and refresh before reconfirmation.
- Incident acknowledge/start/assign/resolve/close/reopen API clients send command metadata. Existing detail resolve/assign/reopen/close callers and list batch actions use the same confirmed-intent helper. Closing requires a user-entered reason. Partial list failures retain selection and uncertain command identities.
- Ticket batch edit callers and the additional IncidentManagement assignment consumer are updated for their changed API signatures. Session identity isolates retained command intents.
- Existing creation clients, conversion creation payload, service-request API, Change and Problem actions are unchanged. No backend, service process, or database was modified.

## Verification

The original focused API/task baseline passed 112 tests. New contract tests first failed on missing ticket edit metadata, incident acknowledgment metadata, assignment body shape, and canonical process task identity. Final focused suite after independent review fixes: 8 suites / 156 tests passed, covering API payloads, task actions, current workbench regression cases, receipt refresh, and uncertain retry behavior in detail/list flows.

Run from `itsm-frontend`:

```sh
node node_modules/jest/bin/jest.js --runInBand --coverage=false --silent --reporters=default --runTestsByPath src/lib/api/__tests__/ga-command-contract.test.ts src/lib/api/__tests__/ticket-api.test.ts src/lib/api/__tests__/incident-api.test.ts src/components/ticket/__tests__/TicketProcessTasks.test.tsx src/components/ticket/__tests__/TicketDetail.test.tsx src/components/ticket/__tests__/TicketBatchOperations.test.tsx src/components/incident/__tests__/IncidentDetail.test.tsx 'src/app/(main)/incidents/__tests__/command-actions.test.tsx'
node node_modules/typescript/bin/tsc --noEmit --pretty false
```

Typecheck reports the same 3 baseline errors in `src/components/layout/sidebar/__tests__/MenuItems.test.tsx:9–11` (missing `icon`); no new type errors. Baseline was independently checked with `tsc --noEmit --incremental false`. Logs: `/home/administrator/.local/state/itsm-task2-remediation-20260914/frontend-ga-contract/`.

These are offline component/API contract checks, not browser-to-backend E2E evidence.

## Explicit limits

- `TicketKanban` is reachable from the ticket page, but its `handleStatusChange` is declared without any JSX/drag-and-drop caller. The visible edit/view menu navigates to `/tickets/:id`, covered by TicketDetail. The unconsumed `useTicketsQuery` update mutation and unused Kanban status callback remain outside this change; no professional state transitions are inferred for them.
- Existing Change/Problem retired API actions are not restored or admitted by this patch.
- Cross-origin creation still uses `Idempotency-Key`; backend CORS allow-header correction is coordinated separately. Creation behavior is not changed here.

## Independent review corrections

Two reproduced issues were corrected in the follow-up: static incident batch confirmations are destroyed when their owning page unmounts, and commands verify the captured actor/tenant session both on confirmation and at the existing HTTP submission boundary; ticket batch successes discard their operation intent individually so a later edit cannot replay an already completed command after a partially failed batch. Failed/uncertain attempts continue to retain their original identity.

An initial review claim that generic ticket edits ignored status was withdrawn after checking fixed backend DTO and owning service. `TicketEditFields.Status` is supported; `ticket_service.go` validates transitions and writes it, while rejecting professional record mutations. No status functionality is removed or reinterpreted.
