# WorkItem convergence V1 isolated runner

Run on Linux with Docker, Go, Node/npm, rsync, frontend dependencies and Playwright Chromium already installed:

    cd itsm-frontend
    python3 tests/e2e/fixtures/run-workitem-convergence.py --base-port 19490

The five consecutive ports are backend, frontend, Redis, MinIO and PostgreSQL. The runner refuses occupied ports, creates randomly named and labeled containers, creates the canonical empty base, verifies startup stops at P, takes and restores an actual isolated pre-P PostgreSQL backup, applies P and subsequent ordinary SQL through the compiled migration CLI, seeds, then runs the application with a non-owner, non-superuser, non-BYPASSRLS business role. The narrowly granted system role retains BYPASSRLS for the existing directory/outbox boundary. It builds the current backend and copies current frontend sources to a private temporary directory so no shared Next build output is reused.

All domain setup and mutations use actual HTTP APIs. A read-only PostgreSQL oracle checks Change assessment fields that are not exposed by ChangeResponse; it verifies the generated container label/port and both professional ID and WorkItem ID. Browser tests keep the two sessions independent and exercise explicit conflict refresh/confirmation. Approval fixtures use actual BPMN task decisions and separate Change ownership from approval candidates. Change creation must report awaiting_submit and freeze a nonempty definition digest without a process instance or competing start outbox; after professional submit there must be exactly one instance matching that snapshot. The Requested Item journey separately requires the real background workflow-start outbox to reach published with no remaining error.

The fixture refuses execution without the runner resource manifest. Do not set its environment variables against a shared application. Generated passwords are carried only through private files/environment/stdin and are never source-controlled. The runner removes its own application processes, containers and environment files on exit, including failures. Logs and failed browser traces remain under the printed mode-0700 evidence directory; traces may contain generated test credentials and must not be committed or shared without redaction. Remove that directory after retaining only reviewed, non-secret evidence.

The --grep argument accepts a Playwright test-name pattern for a focused rerun. Each invocation creates a fresh environment. The two heavier API journeys leave a 61-second rate-window interval, and the global API limiter remains enabled; avoid simultaneous or repeated ad-hoc suites against one environment. A test failure is not an accepted journey: use the exit status and tests.log, including skipped cases.

Coverage: three-domain reason/version/idempotency and two-browser conflict; requester authorization denial; Incident workaround to Problem to failed/rolled-back/successful approved Changes to explicit verification to resolve/close/reopen with configured SLA history; reassignment after assessment/approval preserves nonempty assessment, approval and task actor facts; generic and Requested Item assignment, public comments, attachment persistence and real BPMN approval.

This fixture does not establish production external email/connector delivery readiness or substitute for deployment-owned cutover.


Controlled migration admission uses a separate `v1inspect` login with no business-table access, writes, ownership or BYPASSRLS. Its only application-schema table privileges are SELECT on the migration ledger and private evidence attachment. The independently generated control configuration pins its role and deployment identity; the application opens a forced read-only inspection connection, checks its exact database/schema/server against the business connection, then closes it. Business `v1app` cannot read evidence. P and ordinary migration receipts remain intact throughout the journey. R remains pending manual; this runner's pre-P backup/restore is not the later post-R recovery exercise.
