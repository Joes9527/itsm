# Task 4 Report: Current and Historical Process Tasks

## Result

- Kept the existing authorized `listUserTasks` request and pagination contract, and retained every validated task returned by that request.
- Derived current and historical groups in the presentation layer. `completed` and `cancelled` tasks appear under a default-collapsed “历史任务” disclosure; unknown statuses remain current and keep the existing unknown-status message and backend-provided actions.
- Made current tasks a semantic disclosure. A first successful read expands it only when at least one task has a backend-provided `claim` or `complete` action. Read-only tasks start compact. Refreshes retain the user's choice, while the existing identity-key remount resets it for ticket, tenant, account, or permission changes.
- Kept current and historical tasks on the same read/error/permission state and preserved separate responsible-user and actual-actor fields.
- Replaced panel-specific white/slate styling with `bg-surface`, `bg-raised`, `text-foreground`, `text-muted`, and `border-border` theme tokens.

## TDD evidence

Command:

```text
npx jest --runInBand --coverage=false --runTestsByPath src/components/ticket/__tests__/TicketProcessTasks.test.tsx
```

- Red: 4 new behavior tests failed because the current/history disclosures did not exist and terminal records were filtered from the presentation.
- Green: 34/34 tests passed after implementing grouping and disclosure state. Existing assertions for compact task details now open the corresponding current/history disclosure first.

## Verification

```text
npm run type-check
```

Passed, including the theme-token check and `tsc --noEmit`.

```text
npx eslint src/components/ticket/TicketProcessTasks.tsx src/components/ticket/__tests__/TicketProcessTasks.test.tsx
```

Passed with no output.

```text
npx jest --runInBand --coverage=false --runTestsByPath src/components/ticket/__tests__/TicketProcessTasks.test.tsx
```

Passed: 1 suite, 34 tests, 0 failures.

## Concerns and boundaries

- The focused Jest run continues to print the pre-existing Ant Design `Modal.maskClosable` deprecation warning. Task 4 does not change modal or command coordination behavior.
- Browser viewport/theme verification was not run for this bounded component task. Theme behavior is covered by replacing the hard-coded light palette and by the repository token check.
- No API, service, database, command-after-write coordination, dependency, or shared-environment changes were made.
