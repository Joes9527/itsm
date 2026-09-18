# Candidate T4 evidence — 2026-09-14

Status: current core G2 passed; G3 observation in progress. Earlier sections retain their original checkpoints. This is evidence, not a second task list. Authoritative status: remaining-delivery plan §7.14.

Baseline CandidateSHA: `0544e159adf47a5fa18af175611a77ea09cab574`. Initial environment: [T3 handoff](2026-09-14-candidate-t3-handoff.md). Candidate configuration additions below postdate its initial revision and must be included when the final revision is frozen.

## Actual results

Evidence root: `~/.local/state/itsm-candidate-delivery/t4` (private, not committed).

| Evidence | Result and limits |
| --- | --- |
| core-run-4.log | 2/2 PASS. Real creation of generic, Incident, Problem, Change; replay returns 200 with same identity; version conflict preserves winning edit; comment and attachment byte readback; browser detail reload; theme preference persists; mobile page has no horizontal overflow. Theme switching is not full business coverage in both themes. |
| journeys-run-2.log | PASS. Incident/Problem lifecycle, verification before Problem resolution, close/reopen, end_user reassignment rejection with unchanged version. |
| journeys-run-1.log, second case only | PASS. Real Catalog→RequestedItem→BPMN acceptance→approval task→persisted approval decision. Does not prove final fulfillment or closure. First case failed for missing close reason; corrected in run-2. |
| change-run-2.log | PASS. Three Changes record failed, rolled_back and successful outcomes after assessment, separate CAB actor approval, scheduling and implementation. Review and close are not covered. |
| runtime-status.json | 15 Change callback effects completed; real workflow-start and domain events published. Five email notifications received only by isolated SMTP transport, five in-app notifications sent. Two Incident assignment events and three legacy incident assignment callbacks blocked; not a full green runtime report. |

Tests use real login/CSRF/API/browser requests and preserve their created records. No mocked route responses, no fabricated JWT, no source records edited, no task/history cleanup. Failed test attempts remain in created-records.json. Temporary harnesses and screenshots are not repository test assets.

## Configuration upgrade findings

The backup contains old routing vocabulary (`ticket`, `change`). New candidate bindings use canonical `generic` and `change_request`, retaining the original rows and definitions. IDs and requests are in canonical-bindings.json.

The old Change definition lacks the current action metadata. Candidate deployment added the repository's existing `change_normal_flow.bpmn` under a new key `candidate_change_normal_v2_20260914`, changing only the process ID, then added a higher-priority normal Change binding. It does not rewrite old instances. The evidence is change-template-admission.json and the current CandidateSHA source file. New instances passed all three implementation outcomes.

The legacy Incident emergency workflow still lacks explicit assignment inputs and contains unsupported later actions. This remains visible and blocked. It is not repaired by the independently reviewed Incident event payload fix now being tested. No optionality was inferred and no unsupported action was silently skipped.

## Historical preservation

The T3 pre-acceptance report compared all original identities and original fields across 152 tables without differences. After T4, original-row-preservation-124202.json correctly reports one difference: work_item_number_sequences. Its counter advanced from 8 to 24, with updated_at changed; 16 new candidate WorkItems and the maximum number suffix 24 account for the exact increment. Other original business rows and fields remained equal. The raw nonzero report is retained; do not claim that every operational row stayed unchanged during authorized record creation.

Redis preservation was verified before T4: 222 retained DUMP/absolute-expiry values match, one already expired key was not revived. New runtime keys and legitimate expiration require a separate post-acceptance comparison. Source backup and candidate historical Stream are not cleared.

## Review and limitations

Independent reviewer m1_core_review confirmed the live T3 network/role boundaries and the scoped PASS evidence above; it did not approve G2/M3. Actual PG17 runtime role is nonowner/non-super/non-BYPASSRLS. The separate system role has its documented narrow table/column capabilities; the privileged operator is used only for restore/migration and read-only evidence, never as proof of end-user authorization.

The new PG authentication store is **not wired to startup, login, refresh or logout**. Restart/recovery safety for that store remains unverified and is explicitly accepted as unfinished by the maintainer. Ordinary login checks do not close that limitation. Feishu is outside this release scope; SMTP/KAF fixture delivery is not enterprise integration or real external execution. No R038 or main merge was performed.
## 13:12 CST increment: current code and scoped acceptance

Current CandidateSHA is `04bc49178e6a1f1eda48b998b0f29058420df758`; current configuration revision is `85acc3ca893840e01d4b8024df66090c6efca9d96ed2608c0941b1405ace8775` (environment-revision-04bc4917-webhook.json). The earlier SHA/revision above identify the original runs, not the final configuration.

The Incident assignment consumer now accepts the producer's existing previousAssigneeId field and still checks strict decoding, durable payload and immutable command provenance. RED, final race GREEN and independent review are recorded under agent-a-windows/r7-assignment-*.log. The final test independently checks prior-owner tampering and unknown fields. API/Worker were rebuilt and deployed; migration binary rebuilt, no migration rerun. A first worker build used the nonexistent cmd/worker path; the corrected cmd/kaf_worker build passed. This was a command correction, not a code failure.

Additional actual PASS evidence:

- journeys-run-04bc.log: new-version Incident/Problem lifecycle and permission rejection; all five new Incident status messages published, including first assignment. The two old blocked messages remain diagnostic evidence and are not relabeled successful.
- theme-pages-run-2.log: five real record detail pages in both themes, persisted preference, no pageerror. An earlier one-off script failed to wait for hydration before clicking; corrected script passed without frontend code changes.
- change-close-run-2.log: three implementation outcomes retain their own result through PIR, review callback and close callback. The professional terminal state is completed, not generic closed; the initial wrong assertion was corrected. All three PIR/review/close records were checked; no empty-loop pass.
- fulfillment-run-1.log: a new Catalog/RequestedItem uses real approve_request callback (in_progress), human fulfillment task and complete_request callback (resolved); business extension and WorkItem persist. No external provisioning is claimed.
- boundaries-run-2.log: historical WorkItem update403 with unchanged detail; X-Tenant-ID and tenantId query do not override signed tenant1; asserted nonempty list with tenant1 rows. This is not a fabricated second-tenant session.
- sla-observed.json: the owned short-policy WorkItem25 breached; both actual sla.breached outbox events published. Its temporary routing binding was disabled after this one creation; normal defaults were not changed to a one-minute SLA.
- transport-verification.json: seven isolated SMTP captures and two signed local webhook captures with valid HMAC. With no declared webhook target, the consumer initially rejected both SLA events; after declaring the single candidate-internal target, normal retained-message retry produced two published webhook delivery intents. No ACK, pending or rejection history was manually cleared.
- history-reconciliation.json references raw original-row-preservation-130345.json: only original numbering counter fields advanced, 8→28 exactly matches20 added WorkItems and maximum suffix28. Other original business rows unchanged. Redis post-T4 report:222 keys retained, one naturally expired, no mismatches including historical Stream DUMP and absolute expiry.

Incident configuration now declares existing conditions.no_process=true for new manual lifecycle records. This is an explicitly communicated reversible assumption following functional launch priority; the maintainer preference question has not received an answer and must not be reported as explicit approval. Automatic emergency orchestration remains unenabled; old instances/callbacks remain untouched. Change and RequestedItem continue to use BPMN. This does not claim the legacy emergency template was repaired.

Independent final bounded review found no additional code blocker and verified the preceding acceptance evidence. R7/M3 remains pending only the actual escalation cycle of the current target configuration, then its history check. The timer is15 minutes; do not infer a completed cycle from uptime. R8 controlled restart and60-minute observation are still pending. The fixed code bundle candidate-04bc4917.bundle was verified against main prerequisite a25e108d; no main merge or push occurred.
## 13:32 CST increment: G2 closure and G3 start

Final CandidateSHA `0788a9bb196ab37a8389b3f366bed9877b2f72c3`, EnvironmentRevision `f058c8227e7f350387ecfa9f5296bc0a1fd852c1c392d1071619de27f1c24424` (environment-revision-0788a9bb.json). All three backend binaries were rebuilt; frontend tree equality was verified against the reused build. No migration was rerun for the HTTP-only change.

The actual04bc escalation cycle began and completed at05:22:21Z; escalation-cycle-04bc.log also contains genuine SLA cycles. The following read-only history check (original-row-preservation-132825.json) and reconciliation retained the same sole explained counter difference8→28/20 new WorkItems, with no original business-row changes. This closes the preceding pending-cycle item without inferring success from uptime.

Visual inspection of the Change page found a real remaining defect: persisted approved CAB decisions rendered as pending because the HTTP handler exposed internal Go field names (Status, ApproverName) instead of the existing camelCase DTO contract. Commit0788a9bb maps through dto.ChangeApproval and returns[] for no history. Agent-a-windows/r7-approval-contract-red.log reproduces the field assertion; r7-approval-green.log passed focused race tests. Actual browser/API approval-ui-red.log failed before deploy; approval-ui-green.log passed1/1 on0788, including approved timeline and reload, with change-approved-timeline.png. The first separate unit RED attempt had an unsuitable repository mock assumption and is not counted as the contract failure.

Independent m1_core_review found no blocking contract/tenant/empty-list regression and approved reuse of unchanged04bc business/periodic evidence with this targeted0788 real UI check. Thus current core G2/M3 passes with the disclosed manual Incident and accepted PG-auth limitations; this is not a claim of all-suite rerun or automatic emergency completion.

G3 artifacts are under private t5: controlled-restart.json proves the same API/Worker containers restarted13:30:03–13:30:05, sourcehealth200 throughout, and identical nine business-table digests across restart. Recovery-flow.log passed1/1: a newly created RequestedItem after restart completed actual approval and fulfillment callbacks. Records are retained separately under t5, not substituted for original T4 records. The low-frequency read-only observation began13:30:23 CST and must actually reach at least3600seconds; it has not yet completed. This ordinary runtime recovery does not prove the accepted-outstanding PG authentication wiring or disaster recovery.