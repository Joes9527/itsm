# Candidate T5 runtime evidence — 2026-09-14

Status: G3 passed in the declared candidate scope after final preservation and independent review. R9 maintainer acceptance is pending; M4 is not closed. The sole progress source is the remaining-delivery plan on codex/docs/candidate-a-remaining-delivery, latest checkpoint7.16.

CandidateSHA: `0788a9bb196ab37a8389b3f366bed9877b2f72c3`.
EnvironmentRevision: `f058c8227e7f350387ecfa9f5296bc0a1fd852c1c392d1071619de27f1c24424`.
Access: <http://localhost:3301>; API <http://localhost:18080>.
Private evidence root: `~/.local/state/itsm-candidate-delivery/t5`.

## Controlled application recovery

`controlled-restart.json`: API and Worker stopped gracefully and restarted with identical container IDs, binaries, configuration and resource mounts at13:30:03–13:30:05 CST. Nine business-table row counts and full-row digests were equal immediately across restart. Source service health was200 before, while the candidate was stopped, and after recovery. Candidate health returned200. No shared database/Redis/MinIO was stopped; no data service restart or physical disaster recovery is claimed.

`recovery-flow.log`:1/1 real request passed after restart. A new service catalog definition and RequestedItem executed its actual approval and fulfillment callbacks, persisted the owning records and reached resolved. Test records remain in `created-records.json`; no old task state or history was removed. This proves application/consumer recovery for that flow, not all possible restart failure modes.

## Observation protocol

`observation-samples.jsonl` is append-only evidence from13:30:23 CST. The observer uses monotonic elapsed time and cannot emit its final report before at least3600seconds. At approximately one-minute intervals it reads candidate API health, candidate login page, source health, eight container states and newly emitted API/Worker error/fatal lines. Logs are represented by hashes in the report to avoid leaking runtime material.

At completion, `observation-report.json` records actual duration and failures; `observation-verification.json` additionally checks sample continuity and identical container IDs/start times/restart counts throughout, not merely the last health response. These files must exist and pass before G3 is approved. Preparation time and the earlier04bc timer checks are not counted toward this interval.

Historical SQL and Redis checks are separate read-only comparisons against the immutable11:55 backup. The original numbering counter legitimately changes with authorized new WorkItems; preserve the raw nonzero comparison and independently reconcile its exact increment instead of calling it zero-difference. Ordinary new SLA/notification progression is not history clearing.

## Delivery limits

The new PostgreSQL authentication store is **not connected to startup/login/refresh/logout and its restart/recovery safety remains unverified**, as explicitly accepted by the maintainer. Current runtime uses the old Redis/memory path. The recovery evidence above does not close that limitation.

Incident is configured for its declared manual lifecycle under the communicated reversible launch-priority assumption; legacy automatic emergency execution remains unavailable and its blocked evidence retained. Change/RequestedItem use BPMN. Feishu is excluded; SMTP/Webhook capture is local and KAF is transport-only. No enterprise write, R038, main merge/push or production cutover was performed. Maintainer acceptance remains a distinct R9 action after G3.
## Unexpected host interruption and admitted recovery —14:13 CST

At approximately14:07 WSL restarted. The maintainer stated no active host adjustment and explicitly authorized recovery. All8 candidate containers exited255 and the source API/Workers stopped. Root cause remains undetermined; recovery does not prove that the host issue is fixed.

The first observation is **interrupted, not passed**:34 passing samples, last elapsed2037.58seconds. Original samples are retained; observation-interruption.json records the interruption and digest. It has no completed G3 report. The fresh independent observation is under `observation-2/`, beginning14:13, with its own samples/report/verification. Durations are not concatenated.

`host-recovery.json` records restoration of the exact saved source process commands/environment and startup of the same admitted8 candidate containers/data volumes. No old failed/stopped version was started; no dump restoration, migration or history reset occurred. Source/candidatehealth200. The original code/config EnvironmentRevision stays fixed; this host event separately records changed boot/start times.

Read-only recovery checks passed:152 original tables have no historical business-row difference, with the sole counter8→29 matching21 new WorkItems;21 recorded creation identities and2 attachment records survive. Redis222 DUMP/absolute expiries and historical Stream match,1 natural expiry. `network-probe-after-host-recovery.json` confirms allowed own resources and blocked source/external targets. `recovery-after-host.log` passed1/1 actual RequestedItem approval/fulfillment after host recovery, retaining another new record. Final preservation must include this additional record. No claim of PG authentication recovery is made.
## Completed second observation —15:19 CST

`observation-2/observation-report.json`:14:13:08–15:18:14 CST, monotonic elapsed3601.71seconds,60 samples,0 failures. `observation-2/observation-verification.json` validates continuity, all health responses200, no new API/Worker error logs, and identical8 container IDs/start times/restart counts throughout this second interval. First interrupted samples are not included. Source/candidate wall-clock timestamps and monotonic elapsed are both recorded, not conflated.

`fixed-environment-verification.json` rechecks the exact CandidateSHA, all three build hashes, runtime configuration hashes, and container IDs/images/network modes against the frozen revision. Host-recovery start times are the documented delta, with no further restart during this interval. Actual repeated SLA and escalation completion logs are in observation-2/runtime-cycles.log.

Final SQL raw report original-row-preservation-151840.json has the sole explained operational difference:counter8→30 with22 new WorkItems and maximum suffix30. Original business rows across152 tables remain equal; raw nonzero results are retained. Timestamped reconciliation files preserve each checkpoint. The frozen revision's prior reconciliation is available as t4/history-reconciliation-132825.json; reconciliation-artifact-index.json records its exact original frozen hash and recovery from previously captured output after the latest-report path was updated.

`created-record-preservation.json`:all22 recorded new WorkItem identities/numbers/classes and2 attachment records remain. `attachment-recovery.log`:1/1 passes; both previously uploaded files return exact original bytes after host recovery. These assertions do not freeze legitimate mutable business fields or imply that every product path was tested.
Final Redis check `redis-history-final-g3.json` passed:222 retained keys match DUMP/absolute expiry,1 natural expiry, no mismatched hashes, including historical Stream. Actual second-window logs contain12 SLA scan starts and4 escalation completions. No old queue or stream was cleared to obtain these results.
Independent m1_core_review directly verified the completed evidence and found no additional blocker to closing scoped G3. R9 remains maintainer-owned. The12 SLA log entries prove scan starts; the4 escalation entries prove processing completion. Host root cause, PG authentication wiring/recovery, automatic Incident emergency execution and real KAF execution are not relabeled fixed or passed.