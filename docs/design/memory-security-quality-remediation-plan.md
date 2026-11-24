# PicoClaw Memory System Security and Quality Remediation Implementation Plan

> Document status: implementation in progress (grilling review passed, target version 0.7.20)
> Code verification baseline: `0431a574` (consistent with the Go implementation at `95520122`)
> Database reference snapshot: `/home/zemi/下载/memory`, data as of 2026-11-22, Schema v4, revision 3840
> Document generation date: 2026-11-25
> Intended audience: development, review, testing, and release personnel
> Important boundary: On 2026-11-25 the owner explicitly authorized implementation and release of version 0.7.20 under this plan. The reference database remains read-only; migration, Replay, and Cleanup of real runtime databases must still be explicitly executed by the user.

## 0. Executive Summary

### 0.1 Conclusion

Keep the overall architecture of the current SQLite + Schema v4 + `MemoryService` + independent forget ledger. Do not tear down and rewrite, do not add Schema v5 by default, and do not introduce vector databases, external summary models, or a proactive message scheduler.

This round delivers in the following order:

1. **G0: freeze baseline and reproduce.** First write failing tests that prove the current defects, and preserve existing normal paths.
2. **G1: close write and approval bypasses.** Fix tombstone bypass, model self-approval, internal governance field exposure, missing actor, and secret classification gaps.
3. **G2: close read and egress bypasses.** Fix Seahorse cross-session reads, background summarization bypassing egress, untrusted chat identity, and debug body logging.
4. **G3: fix migration, ledger, and recovery foundation.** Back up before any maintenance write; make new-database initialization atomic; make ledger identity and pending state reconcilable.
5. **G4: complete cross-store forgetting.** Unify derived-data cleanup for normal Forget and Replay, JSONL/Seahorse cleanup, partial/unavailable feedback, and concurrency control.
6. **G5: fix retrieval, domain, and budget correctness.** Permissions and TTL take effect before LIMIT; unsafe domain capabilities reject first; budget semantics are consistent and overflow is observable.
7. **G6: unify API, CLI, UI, and configuration.** Dangerous operations preview by default; settings updates no longer implicitly clear security fields; status feedback does not overstate success.
8. **G7: establish real data quality measurement.** This round delivers only anonymized recall evaluation and FTS/deduplication measurement; long-text chunking and temporal extraction move to subsequent independent work.

G0–G4 are security and data-integrity blockers; G5–G6 are correctness and operability release gates; only the measurement part of G7 can run in parallel after core defects are closed. Semantic retrieval, automatic long-text splitting, external LLM extraction/summarization, and proactive recall are not part of this round's core delivery.

### 0.2 Recommended approach is fixed

This plan directly adopts the following recommended trade-offs and does not repeat the design discussion item by item:

- Model sensitive actions use **direct rejection**: the model retains only controlled add/search/inspect, restricted non-core correct, legacy load_capsule, and forget preview; confirm, archive/restore, all update_state/relationship writes, and any force/pinned/boundary change return owner-only, and the model is not allowed to create executable plans.
- The owner change plan uses the existing `memory_change_plans.payload` to store a short-lived envelope with version, domain, request ID, whitelist parameters, and approval context; no new columns for approval metadata.
- Use the existing `sources.evidence_root` to store a **host-derived, versioned source root evidence description**; one source row corresponds to one unique origin event/root, and no new column is added for the full evidence HMAC.
- Use the existing `actor_ref`, `request_id`, `reason_code`, and `policy_revision` for audit; do not build a new audit system.
- Historical forget produces read-only reports only; content hashes without domain are not used for automatic deletion or bulk ledger backfill.
- Multi-domain relationship, domain injection, and multi-domain capsule fail closed this round; true multi-domain support requires a separate RFC and must not be implemented by concatenating untrusted context.
- Existing databases that are already corrupted and missing business, obligation, or audit state tables must fail closed and be guided to restore a backup; they must not automatically create empty tables to fake a successful repair.
- When old data cannot prove ownership, provenance, or deletion coverage, keep `legacy_unassigned`, `unknown`, `partial`, or `outcome_unknown`; do not fabricate facts.
- An empty `allowed_chat_recipients` continues to be compatibility-permissive this round with a clear warning; strict default-deny is a separate configuration migration effort and must not silently swap old configuration semantics during security fixes.
- `budget_tokens=0` does not mean memory is disabled; keep the existing configuration normalization and remove the egress budget short-circuit. Only explicit `enabled=false` disables long-term memory.
- `enabled=false` does not stop accepted forget cleanup; whether conversation summaries are sent externally is judged by separate purpose and recipient policies.
- Core boundary/pinned rules are not silently truncated; ordinary memories are trimmed first, and core rule overflow must be visible and have a hard cap.

### 0.3 Non-goals

This round does not:

- Restore the v1→v2→v3 old migration chain, or auto-migrate business Schema in `Open`.
- Create new service/repository layers, a generic task platform, or new microservices.
- Reintroduce MEMORY.md or daily notes into runtime.
- Enable commitments, proactive recall, or external processing just to "use existing tables".
- In this round, split per-Agent independent Seahorse Engines, open full multi-domain support, implement automatic long-text chunking, or extend temporal extraction.
- Auto-claim legacy data, auto-merge similar long texts, or bulk-modify real memories.
- Claim that application-layer approval can stop a model with same-UID file access or arbitrary exec capability from directly reading/writing files.
- Change version numbers, tag, trigger Release, or deploy without an explicit user release request.

## 1. Verified Baseline and Evidence

### 1.1 Capabilities to retain in the current implementation

- Accepts only explicit v3→v4 migration; rejects old and future versions.
- Normal protected reads/writes have a pending-migration fail-closed path.
- Same-domain exact deduplication, Correct versioning, summary invalidation, the Forget main-database transaction, and the independent ledger framework already exist.
- Main retrieval FTS/LIKE permission filtering executes before LIMIT, with regression tests.
- Main chat, streaming, single Provider, and fallback candidates already have egress checks.
- Forget/Replay fail closed when the ledger is missing or corrupted.
- The reference main database and ledger both pass integrity checks; no foreign key anomalies or active exact duplicate groups were found.

### 1.2 The reference database only reveals facts about that snapshot

| Metric | Snapshot result | Conclusion that can be drawn | Conclusion that cannot be drawn |
|---|---:|---|---|
| entries | 117: 110 active, 7 superseded | Data scale is small | Cannot represent the current production scale |
| Median body length | 1346 bytes | Mostly long-document-style memories | Cannot conclude from this that all long texts should be split |
| Body length over 1KB | 79/110 | Need summarization, expansion, and budget governance | Cannot prove retrieval is necessarily slow |
| event_at empty | 110/110 active | Temporal recall lacks data | Cannot use created_at as event time |
| use_count=0 | 96/110 | Usage observation is insufficient or recall is concentrated | Cannot directly conclude that all 96 entries are garbage |
| domain | All `legacy_unassigned` | The multi-domain path has not been validated by real data | Cannot claim multi-domain isolation has been accepted |
| sources | 117 rows, one source per memory | Lacks multi-source corroboration | Cannot fabricate additional sources from this |
| message_id/origin_event/model/prompt | All empty | The evidence chain cannot be fully traced | Cannot auto-fabricate |
| actor_ref | All 274 operations empty | Operations cannot be attributed to a trusted subject | Cannot back-infer historical approvers |
| Historical forget | 177 times, affecting 459 entries | Pre-ledger historical deletion obligations exist | Cannot assume all can be recovered from the existing ledger |
| Current ledger | 2 applied | Covers only the latest two main-database phases | Not equal to external copy cleanup being complete |
| cleanup jobs | 2 pending, started 2026-11-22 | A cleanup backlog really exists | Cannot claim session copies are deleted without execution |
| Exact duplicate groups | 0 | No large-scale exact deduplication needed | Does not exclude semantic duplicates |

### 1.3 Verification performed

The following was executed and passed with the same code baseline:

```sh
go test -tags 'goolm stdjson' \
  ./pkg/memstore ./pkg/agent ./pkg/tools ./pkg/tools/shared \
  ./pkg/identity ./pkg/session ./pkg/memory ./pkg/seahorse \
  ./web/backend/api ./cmd/picoclaw/internal/memory \
  -count=1 -timeout=5m

CGO_ENABLED=1 go test -race -tags 'goolm stdjson' \
  ./pkg/memstore -count=1 -timeout=5m
```

At that time the CLI memory subcommands had no test files. The above results only show that existing cases pass; they do not cover the adversarial, cross-store, and recovery scenarios listed in this plan.

## 2. Priority, Delivery Gates, and Definition of Done

### 2.1 Priority

| Level | Meaning | Release rule |
|---|---|---|
| P0 | Privacy, permissions, permanent forgetting, unrecoverable data corruption | While open, must not be released as "governance complete" |
| P1 | Visibility correctness, TTL, domain, budget, configuration, and status truthfulness | While the core paths are open, multi-domain/multi-Agent expanded use must not be enabled |
| P2 | Quality, performance, semantics, documentation, and experience enhancements | Does not block P0/P1 security fixes, and must not take their test resources |

### 2.2 Definition of done for each work package

A work package is complete only when all of the following conditions are met:

- First commit a failing test that reproduces the original problem, then commit the minimal fix.
- All entry points follow the same permission and governance semantics; fixing only the UI or a single Store method is not enough.
- New writes do not produce untraceable sensitive copies.
- Failure, timeout, crash, and retry do not report partial as success.
- Tests assert the final database state, external copies, and side effects, not only return values.
- No new dependencies; if one is truly needed, obtain separate environment change approval.
- Documentation, API types, CLI output, and UI text match the measured behavior.
- When a verification was not run, explicitly mark it "not run"; static review must not substitute for it.

### 2.3 Release blockers this round

Stop the release if any of the following occurs:

- Forgotten body text can be re-learned or resurrected through any public entry point.
- A model can self-confirm, forcibly change pinned core, or change core status in a private chat.
- Seahorse can read messages that do not belong to the currently authorized conversation.
- Any Provider call bypasses the applicable egress policy.
- A maintenance path modifies the database before creating a backup, or continues modifying after a backup failure.
- Forget/Replay reports unverifiable cleanup as complete.
- Old configuration loses its original security settings because of field deletion or a partial PUT.

## 3. Target Invariants

1. All body changes go through versioned Correct; public DTOs do not accept internal governance bypass fields.
2. User confirmation, core modification, status promotion, and permanent forgetting are all bound to a host actor, approval event, and full request ID; entry operations bind expected revision, and relationship does not fabricate a non-existent revision.
3. Model parameters can only narrow permissions; they cannot expand domain, audience, recipient, or conversation scope.
4. Every Provider call carrying long-term memory or its derived content is checked by purpose and recipient; policy denial is non-retryable and does not silently redirect, and ordinary clean chats are not mistakenly affected.
5. All memory visibility, TTL, domain, realm, audience, and layer conditions take effect before candidate LIMIT; administrative and historical queries must explicitly choose different semantics.
6. When a group chat identity cannot be confirmed, it is not treated as a private chat; the administrative surface and CLI use an explicit administrator context.
7. Accepted forgetting obligations persist in the independent ledger; neither main-database rollback nor `memory.enabled=false` eliminates or pauses that obligation.
8. Normal Forget and Replay use identical summary deletion/redaction, derived-data handling, and cleanup enqueue logic.
9. Cleanup is based on trusted source location and root evidence verification; when it cannot be verified, it is `unavailable/partial`; when a crash makes it impossible to distinguish already-deleted from never-existed, it is `outcome_unknown`.
10. Every maintenance write follows pre-check, write-stop or maintenance mutual exclusion, main-database and ledger backup, transactional modification, and final re-check.
11. Configuration compatibility does not rely on deleting published fields; security lists are not implicitly cleared by partial updates.
12. Logs, metrics, and test reports do not record memory body text, raw queries, or secrets.
13. Core rules are not silently truncated; ordinary memories cannot use the minimum retrieval budget to break the total target.
14. `unknown` old data is not automatically rewritten to confirmed, owned, verified, or done.

## 4. Key Design Decisions

### ADR-01: All public writes use whitelist DTOs

Web/API Add, Correct, Update, archive/restore, review/resolve, forget, relationship, persona, and import all use explicit whitelist DTOs and reject unknown fields via `DisallowUnknownFields` or an equivalent mechanism. You cannot fix only the three entries endpoints while leaving other handlers calling the raw Store directly.

`Force`, `ForceStatus`, `BypassForgetTombstone`, trusted confirmation flags, actor, domain, approval events, `EvidenceRoot`, `OriginEventID`, `SessionKey`, `MessageID`, and other identity/root evidence fields are derived by the host; clients cannot forge them. The management API is the owner surface, but it must also obey target domain, revision, and approval semantics.

### ADR-02: Model sensitive actions are directly rejected; owner plans are hardened separately

Model tools use an explicit allowlist: add/search/inspect, restricted non-core correct, legacy load_capsule, and forget preview. correct allows only non-pinned, non-boundary corrections within the same domain/audience/layer with no Force, and re-checks the tombstone; load_capsule is allowed only when the trusted channel and legacy/domain validation pass.

When `Purpose=="chat"`, confirm, archive/restore, all update_state/relationship writes, and any force/pinned/boundary change uniformly return owner-only; this round does not let the model create executable change plans. Model tool writes use `SourceInferred`; `SourceExplicit` is used only when the evidence is truly an explicit user statement.

Explicit owner confirmation in the dashboard/CLI may continue to use a controlled plan or direct approval path, but must pass the same DTO, actor, and domain validation; entry operations additionally validate expected revision.

Rationale: direct rejection closes P0 with the smallest change, avoiding the new attack surface of payload injection, cross-domain approval, and crash recovery that would be introduced by "the model can request".

### ADR-03: The owner change plan uses typed parameters and an independent request ID

Plans can only be created by owner entry points; each action uses independent typed params, and arbitrary `AddInput` or `RelationshipUpdate` must not be placed into the payload as-is. The payload uses a versioned envelope:

```json
{
  "schema": 1,
  "protected": {
    "request_id": "full-uuid",
    "domain_id": "legacy_unassigned",
    "initiator": "dashboard|cli",
    "params": {}
  },
  "approval": null
}
```

`params_digest` covers the normalized protected envelope; on approval, recompute it and validate action, domain, target, and expiry, while entry plans additionally validate expected revision. The same atomic update during claim writes `approved_by` and the approval actor/time/context; the full request ID runs through the ledger intent, operation, and cleanup job. The plan ID no longer doubles as a cross-table idempotency key, but the plan↔request association is retained through the operation reason_code, ensuring auditability after the terminal state clears the payload.

Entry operations must bind expected revision. Current relationship has no revision, so this round does not promise revision CAS for relationship plans; sensitive relationship uses legacy owner direct approval with explicit preconditions, or remains unsupported.

Statuses are divided into `executed/expired/denied/failed_terminal`, which clear the payload, and `executing/failed_retryable/outcome_unknown/blocked`, which retain recovery information. Only executed, expired, denied, or a terminal failure confirmed to have no uncleaned side effects may clear the payload; executing/retryable/unknown/blocked retain whitelist parameters and the approval record. blocked must not execute automatically; it may only be explicitly requeued by the owner after the conflict is fixed and a new preview is generated. When the result is uncertain after a side effect occurs, it must not be marked failed directly.

### ADR-04: evidence_root stores the host-derived source root

`evidence_root` uses compact JSON and contains at least `v`, `algo`, `key_id`, `type`, `canonicalization`, and `digest`. The root type together with the canonicalization version determines the input bytes; unknown types, versions, or corrupted JSON are always unavailable, and must not fall back to the 120-rune display text.

The root can only be generated by the host at the trusted inbound/tool boundary and covers the complete source unit, not the evidence fragment. If the evidence is only part of a long message, the root still corresponds to the complete Content, tool arguments, or Seahorse part; cleanup locates candidates first and then recomputes and compares over the same complete unit.

One source row corresponds to one unique `origin_event_id` and one root. Multiple messages, Content/Reasoning/tool-argument roots of the same entry must have different origin events and must not be silently collapsed by the existing sources unique key and `INSERT OR IGNORE`. Only a repeated write of the same origin event may be idempotently ignored.

SourceLocator and manifest are upgraded in sync to carry root type, version, and digest; the old locator protocol only performs strictly verifiable compatible matching. JSONL has no dependable internal message ID, so it uses session + root scanning; Seahorse local message IDs and platform message_id must not be mixed.

The MVP covers only current real sources and known copies: JSONL Content, tool-use arguments, tool result Content, and Seahorse content/text/tool-use parts. Reasoning, media, and other root types are enabled only after actual source integration; shared media files are not deleted by default when a message is forgotten.

### ADR-05: Historical forget is read-only reporting, with no automatic deletion

The historical `memory_operations.content_hash` is an unsalted content hash without domain; it cannot be converted into a ledger HMAC, nor can it prove the current ownership of a deletion obligation. Historical reconciliation therefore outputs only target ID, time, matchability state, domain ambiguity, and locator completeness; it **does not automatically write a journal and does not delete by content hash**.

Only when the target entry still exists in an authorized restored copy, the operation time is valid, explicit legacy maintenance mode is active, and the owner confirms again may it be executed as a **new maintenance deletion action** and generate a new actor/request/ledger record. These actions must not be passed off as the original historical approval.

Current Replay and modern ledger intents must take a different path keyed by manifest entry IDs + domain, and must not be mixed with historical content-hash reports. The 177 historical forget operations that no longer exist in the current main database do not generate noisy journal entries; when even the operation is missing, recovery is explicitly impossible.

### ADR-06: Existing corrupted databases are not automatically filled with empty state tables

A brand-new empty database is judged fresh only when **no known memory business object exists**, and creates the complete v4 in a single transaction; `loadRevision` must not separately write schema_version=4 before initialization. As long as any object remains but meta/entries are missing, it is treated as corrupted and the base table creation script is prohibited.

An existing v3 missing v4 tables is pending migration, not corruption. An existing v4 missing `entries`, `sources`, `personas`, relationship base tables, `memory_operations`, `memory_jobs`, `memory_change_plans`, `memory_origin_events`, `memory_domains`, or the independent ledger is treated as business/obligation/audit state that cannot be rebuilt, returns `non_repairable`, and is guided to restore a backup.

Only indexes, triggers, FTS, and explicitly rebuildable probe objects may enter explicit repair. Empty table creation must not be used to delete historical tombstones, pending plans, or cleanup obligations.

### ADR-07: Cleanup reuses active instances and atomic session operations

In-gateway cleanup reuses each Agent's active JSONLStore and Seahorse Engine; it does not create separate storage instances without shared locks. JSONL provides a session-level atomic mutate inside the existing store: flush, read, verify, rewrite, and return an error within the same shard lock; external GetHistory followed by SetHistory is prohibited, and the error-swallowing SessionManager.SetHistory is not used.

CLI does not provide online concurrent cleanup. Offline maintenance uses workspace-level atomic maintenance lock creation, a gateway PID double-check, and a reverse check at gateway startup; the existing PID file serves only as a liveness signal and does not masquerade as a mutex. Execution is refused when write-stop cannot be proven or the lock state is abnormal; `--force` must not bypass this.

### ADR-08: Multi-domain capabilities fail closed this round

The current `relationship_state(key PRIMARY KEY)` cannot express same-named keys in different domains, and retrieval, capsule, and asynchronous contexts have no complete trusted domain propagation chain. This round only guarantees:

- All Store/Service reads, writes, and deletes explicitly validate domain; cross-domain supersede, Replay, Capsule, and relationship overwrite are prohibited.
- Normal runtime keeps explicit legacy compatibility; non-legacy requests lacking a trusted domain return unsupported rather than falling back to the whole database.
- Non-legacy relationship writes return an explicit unsupported error; deletion prioritizes source_event_id, and only copies that are legacy, unmarked, and whose predicate exactly matches nickname/birthday/anniversary are allowed a restricted fallback; other cases are retained and reported unavailable.
- Management-surface global export is explicitly marked owner-only; ordinary models cannot use administrative interfaces to expand permissions.

Complete domain pushdown, multi-domain relationship, capsule, and per-Agent context require a separate RFC. This round does not claim that multi-domain is open or accepted.

### ADR-09: Budget uses two-layer semantics: target budget and core overflow

- Keep the existing `budget_tokens=0` default normalization in configuration loading and the settings API; remove the egress budget short-circuit and update the original test expectations, without building a second normalization implementation.
- Ordinary profile, episodic, relationship, and retrieval are trimmed within one total target budget; multiple percentages are not stacked into hidden overspend.
- Remove the forced minimum injection of 200 for retrieval; when fixed blocks have exhausted the budget, retrieval is 0.
- pinned/boundary are not silently truncated or stripped of subsequent rules. When they exceed the target budget, return an explicit overflow/cut metric and suppress ordinary retrieval.
- G0 freezes the core total and single-item absolute cap using real-length fixtures; exceeding the cap rejects assembly, gives an actionable error, and results in 0 Provider calls.
- Token estimation is explicitly a conservative estimate and is not claimed to equal each Provider's real tokenizer.

### ADR-10: Compatibility first; do not swap configuration semantics

Empty recipients remain compatibility-permissive this round but are marked `compatibility_allow_all` on the status page. Strict allow-list/deny-all requires a new explicit mode and old-configuration regression tests and must be delivered separately. `enabled=false` is the explicit way to disable long-term memory.

### ADR-11: Egress is judged by purpose; cleanup is not controlled by the learning switch

Ordinary main chat without long-term memory keeps the original Provider behavior; calls carrying long-term memory, `/btw` side questions, legacy/Seahorse conversation summaries, subagent forwarding, external extraction, or embedding are checked by purpose and actual recipient. Conversation summaries, even when they contain no long-term memory entries, are conversation-derived data processing and cannot bypass configured recipients because of `memory.enabled=false` or budget=0.

Policy denial preserves `errors.Is(ErrMemoryEgressDenied)` and terminates that call chain; it does not silently redirect to fallback because of the denial. Ordinary retryable errors still follow the original fallback mechanism, with each actual candidate re-checked.

Forget cleanup is an accepted obligation and is independent of the long-term memory learning switch. `memory.enabled=false` only stops long-term memory assembly and learning; it cannot stop reconciliation and cleanup of pending ledger/jobs.

## 5. Work Package Overview and Dependencies

| Work package | Content | Priority | Depends on | Parallelizable |
|---|---|---|---|---|
| G0 | Baseline, failing tests, temporary copies, and measurement | P0 | None | Do not modify the same tests in parallel with other coding tasks |
| G1 | Writes, approval, actor, secrets, and DTO | P0 | G0 | Some read-only isolation tests in G2 |
| G2 | Seahorse session isolation, context identity, egress, logging | P0 | G0 | Parallel with G1, avoiding shared file conflicts |
| G3 | Migration, backup, ledger identity, and recovery reconciliation | P0 | G0 | Parallel with G1/G2 |
| G4 | Forget/Replay, root evidence, JSONL/Seahorse cleanup | P0 | G1, G3 | JSONL and Seahorse subtasks can run in parallel |
| G5 | Retrieval, TTL, domain restrictions, budget | P1 | G1; the capsule part depends on the G4 domain contract | Two independent subtasks can run in parallel |
| G6 | API, CLI, UI, configuration, and status feedback | P1 | G1–G5 contracts | Frontend and CLI can run in parallel |
| G7 | Anonymized quality evaluation and FTS/deduplication measurement; long text/temporal separately chartered | P2 | G5; real-copy verification depends on G4 | Quality and performance measurement can run in parallel |

Dependencies are not an absolute restriction on commit order, but merge and acceptance must satisfy them. Keep each work package as small commits, addressing only one defect family at a time.

## 6. G0: Freeze Baseline, Reproduce, and Measure

### 6.1 Scope

- Freeze the current HEAD, upstream, worktree diff, and Go/Node toolchains.
- All database experiments use synthetic fixtures or authorized copies in newly created temporary directories.
- Do not start a real gateway and do not modify the original files in `/home/zemi/下载/memory`.
- Establish a defect number, test mapping, and release gate checklist.

### 6.2 Failing tests that must appear first

| ID | Reproduction | Current expected behavior |
|---|---|---|
| R-01 | Call Correct with the same body after Forget | Currently can bypass the tombstone |
| R-02 | API AddInput carries internal bypass fields | Currently accepted |
| R-03 | Private-chat model confirm/force/archive/restore | Currently executes directly |
| R-04 | Seahorse grep/expand across two sessions | Currently can read across sessions |
| R-05 | legacy/Seahorse summary Provider call | Currently no egress |
| R-06 | Cleanup of source messages longer than 120 runes | Currently cannot match |
| R-07 | The same evidence appears in two messages | Currently may delete only one |
| R-08 | Summaries and cleanup jobs after Replay | Currently not handled uniformly |
| R-09 | v4 repair backup order when objects are missing | Currently may modify before backing up |
| R-10 | Crash during new-database initialization | Currently may leave an unrepairable v4 |
| R-11 | public sentinel exists after more than 40/30 invisible fixed blocks | Currently may be pushed out |
| R-12 | Expired entries in Search/1-hop/capsule | Currently may still be returned |
| R-13 | Partial settings PUT | Currently may clear security fields |
| R-14 | partial/blocked cleanup CLI | Currently may exit 0 |
| R-15 | Two processes first-create the same ledger identity | Currently has an overwrite race |
| R-16 | `/btw` and subagent Provider calls | Currently uncovered send points exist |
| R-17 | Pending cleanup when `memory.enabled=false` | Currently the worker returns directly |
| R-18 | Owner plan payload carries Force/domain/unknown fields | Currently lacks complete whitelist and digest validation |
| R-19 | archive/review/relationship/persona/import carry unknown fields | Currently raw Store/ordinary JSON decoding can bypass the DTO closure |
| R-20 | The same entry comes from two different message roots | Currently the sources unique key may collapse subsequent sources |
| R-21 | Backend deletion succeeds but crashes before CompleteJob | Currently retry may report deleted as unavailable |
| R-22 | Settings omit recipients/V4 fields, `memory:null`, explicit empty array | Currently may clear configuration or misjudge it as configured |
| R-23 | Unknown ChatKind and group-chat asynchronous follow-up | Currently may learn as private |
| R-24 | Fallback primary candidate denied by policy | Must stop the policy-denied chain with 0 Provider calls, no silent redirect |
| R-25 | Seahorse Compact/Ingest concurrent with message cleanup | Currently the lock does not cover all paths |

### 6.3 Anonymized measurement of the real snapshot

Output only counts, lengths, statuses, and times; do not output body text. Save the following baseline for later comparison:

- active/status/domain/layer/audience distribution;
- body length quantiles, and completeness rate of source locator fields;
- use_count distribution, but do not equate it with relevance;
- job/state/attempts/age and ledger seq;
- SQLite pages, FTS usage, and freelist;
- high-confidence secret patterns output hit counts only, not context.

### 6.4 Acceptance and rollback

Acceptance: the above tests fail as expected before the fix, and the failure reasons match the target defects; tests use temporary directories and a controllable clock.

Rollback: delete the newly added tests and measurement scripts; do not modify real data to make initial-draft tests pass.

## 7. G1: Write, Approval, and Audit Closed Loop

### G1-01: Close tombstone and cross-domain supersede bypasses

**Scope of changes:** `pkg/memstore/store.go`, `governance.go`, `service.go`, `types.go`, and corresponding tests.

**Implementation:**

1. Correct performs a tombstone check on the new body equivalent to Add.
2. Explicit `SupersedesID` validates target domain, status, and caller permissions.
3. `Update` no longer allows body changes; all body changes go through Correct.
4. The reassign_audience plan accepts only the metadata whitelist.
5. `blocked_forget` returns 409 in the API with an explicit action, not a fake success.
6. If an owner needs to restore forgotten body text, design an unforget with an approval event separately; do not open it by default this round.

**Acceptance:**

- Add, Correct, API, model tools, import, and plan execution can none of them resurrect forgotten body text.
- Domain A cannot supersede, correct, or delete entries in domain B.
- Old version history is still preserved, and legitimate Correct behavior is unchanged.

### G1-02: Direct model rejection and the owner plan state machine

**Scope of changes:** `pkg/tools/memory.go`, `pkg/memstore/service.go`, `governance.go`, `store.go`.

**Implementation:**

- Model actions use an explicit allowlist: add/search/inspect, restricted non-core correct, legacy load_capsule, and forget preview. correct is limited to non-pinned, non-boundary, same domain/audience/layer, no Force, and passing the tombstone check; load_capsule must pass trusted channel and domain validation.
- When `Purpose=="chat"`, confirm, archive/restore, all update_state/relationship writes, and any force/pinned/boundary change directly return owner-only; this round does not let the model create execution plans.
- The Store layer retains internal capabilities, but MemoryService rejects approval actions in non-admin contexts.
- pending_review cannot be directly restored to active; pinned core status changes require owner approval.
- `ReviewRequired` really takes effect for ordinary model writes.
- Owner plans use action-specific typed params and `DisallowUnknownFields`; arbitrary AddInput, Force, domain, or evidence root injection is prohibited.
- Both creation and approval validate protected digest, domain, target, action, and expiry; entry plans additionally validate expected revision; the approver must have permission to manage that domain.
- During claim, atomically store approved_by, approval actor/time/context, and the full request ID; the request ID runs through operation, ledger, and cleanup job.
- `executing/outcome_unknown` queries side effects by request ID: if a side effect exists, fill in the terminal state; only allow retry when there is no side effect and the entry revision is unchanged; keep blocked on conflict, and only the owner may explicitly requeue after generating a new preview.
- Only executed/expired/denied/failed_terminal clear the payload; retryable/unknown retain the whitelist parameters and approval record needed for recovery.
- Ledger RecordIntent queries by request_id inside `BEGIN IMMEDIATE` and returns the same existing intent; idempotency does not depend on adding a new unique index.

**Acceptance:**

- Neither private-chat nor group-chat models can self-approve or create execution plans.
- Payload tampering, unknown fields, cross-domain, and digest mismatch are all rejected.
- Concurrent approval of the same plan produces at most one side effect; a repeated request ID does not produce multiple intents.
- Inject crashes at three points—claim, underlying commit, and final status write—and recovery neither executes twice nor reuses an old revision.
- After plan expiration, execution is impossible; the terminal payload does not retain body text.

### G1-03: Public API uses whitelist DTOs

**Scope of changes:** `web/backend/api/memory.go`, frontend `memory-api.ts`, API tests.

**Implementation:**

- Define whitelist request structures for Add/Correct/Update, archive/restore, review/resolve, forget, relationship, persona, and import, and reject unknown fields.
- All the above write entry points go through MemoryService's owner context; handlers must not bypass the service to call the raw Store directly.
- domain, actor, confirmation, force, evidence root, origin event, session/message identity, and internal status are derived by the host.
- The management API is explicitly the owner surface; it does not automatically gain administrative permission from an ordinary model request context.
- Write audit metadata only for sensitive operations such as export, forget, and plan approval; inspect/capsule do not add persistent writes for read auditing.

**Acceptance:** All write endpoints return 400 for internal or unknown fields; the old frontend remains behavior-compatible when using only public fields; no handler can bypass service approval.

### G1-04: Trusted actor and approval events

**Implementation:**

- Generate the actor from the dashboard session, CLI owner, or gateway request context; do not use model-reported identity.
- The current dashboard is a shared-password owner surface with no distinguishable human principal. Use `dashboard|cli|gateway@<installation-ledger-uuid>` to represent "the entry actor on this installation instance"; do not falsely claim it is a verified personal identity. Real principals are integrated later with multi-user support.
- Confirm/Correct/Forget/Replay/SetStatus/relationship all write actor_ref, full request_id, reason_code, and policy_revision.
- The existing scope_json of ledger intents stores the plan/approval/actor/domain/expected_revision binding; a pending intent without an approval binding can only be reported, not automatically replayed.
- The operation log is associated with ledger/cleanup through the full request ID; the approver is no longer uniformly hard-coded as `dashboard`.

**Acceptance:** New operations have a non-empty actor consistent with the host entry point and installation identity; historical empty actors remain unknown and are not bulk-backfilled; intents without an approval binding are not executed automatically.

### G1-05: Secrets, sensitive information, and untrusted instruction boundary

**Implementation:**

- Extend high-confidence secret detection: password, passwd, bearer, common token and private key formats.
- All writes, including update_state, relationship, automatic extraction, and Correct, use unified classification and rejection.
- Automatic extraction for health, address, etc. only raises the protection level; it does not lower the level already given by the caller.
- relationship update_state sets a fixed length limit and rejects over-limit input instead of silently truncating.
- Model tools cannot write boundary; memory body text is marked as untrusted data even when located in the system section and does not grant tool or network permissions.

**Acceptance:** Chinese and English secrets, tool arguments, relationship, and long input all have negative cases; ordinary similar text is not over-blocked.

### G1 Rollback

Roll back DTO, tool action, or classifier changes by independent commit. Rollback must not re-open known bypasses as the default compatibility mechanism; if release must be deferred, keep dangerous actions disabled. Before rollback, pending/executing plans must be handled: complete idempotent reconciliation or expire them and clear the payload; an old binary must not continue executing plans under the new protocol.

## 8. G2: Read Isolation, Context Identity, and Egress

### G2-01: Seahorse session and Agent isolation

**Scope of changes:** `pkg/seahorse/tool_grep.go`, `tool_expand.go`, `short_retrieval.go`, `store.go`, `pkg/agent/context_seahorse.go`.

**Implementation:**

1. Obtain AgentID and ToolSessionKey from the trusted tool context, and resolve conversation through the existing Engine/Store; reject when a trusted identity is missing or the mapping is ambiguous.
2. GrepInput/SearchInput add a trusted ConversationID; the default `all_conversations=false` must actually add the filter.
3. A model passing `all_conversations=true` returns an error directly; owner cross-conversation capability only goes through an independent administrative surface, and admin context is not forged for the model.
4. ExpandMessages adds the authorized conversation to the storage query; it must not take results and then trust a global ID provided by the model.
5. This round keeps the single-Engine architecture and enforces isolation via AgentID + sessionKey + conversation; when sessionKey conflicts or Agent ownership cannot be proven, fail closed. Per-Agent independent Engines and routed bootstrap rework are evaluated separately.

**Acceptance:** Cross tests with two sessions, two Agents, and group chat vs. private chat cannot read unauthorized content; model all_conversations is rejected; normal current-session grep/expand does not regress.

### G2-02: ChatKind, realm, and asynchronous sources

**Implementation:**

- When a chat channel exists but ChatKind is missing/unknown, chatKindAllowsPrivate, MemoryContext.AllowsPrivate, audienceSQLFor, and sharedChatKind uniformly fail closed.
- Asynchronous follow-up inherits at least the original ChatType, Channel, ChatID, and SessionKey; when the current bus has no trusted domain/source field, that follow-up is prohibited from automatic extraction or writing to long-term memory, and inheritance must not be fabricated.
- An explicit realm parameter is still constrained by group/public realm policy.
- Administrative queries and ordinary domain-aware queries keep separate entry points and cannot borrow from each other.

**Acceptance:** Group-chat tool output is not treated as private-chat evidence; unknown channel context does not read private; asynchronous messages missing domain/source are only degraded, not learned; explicit fictional/work does not expand group-chat permissions.

### G2-03: Unified egress for all Provider calls

**Implementation:**

- Extract the existing `checkMemoryEgress` into an internal helper that receives configuration and purpose, without changing the Provider public interface; Seahorse CompleteFn resolves the Agent/session Provider/model for the current call and must not keep using the default Agent's old recipient.
- Send points cover at least main calls, fallback, retries, streaming, legacy/Seahorse summarization, `/btw` (`turn_coord.go`), and the subagent legacy tool loop.
- Ordinary main chat without long-term memory keeps the original behavior; calls carrying long-term memory or its derived content, conversation summaries, and subagent forwarding that cannot be proven clean are checked by purpose.
- Conversation summaries, even when they contain no long-term memory entries, are checked against recipients under the conversation-derived-data purpose; neither `memory.enabled=false` nor budget=0 may skip this.
- Policy denial returns non-retryable `ErrMemoryEgressDenied` and terminates that call chain, without silently redirecting to fallback; background summarization uses an explicit local degradation and does not reuse an old egress snapshot.
- Ordinary retryable errors may still follow the original fallback mechanism, but each actual candidate is re-checked.

**Acceptance:** Each send point is tested separately with allowed/denied recipients; when denied, the Provider call count for that candidate is strictly 0, and fallback calls after policy denial are also 0; `errors.Is` is preserved; cover enabled=false, budget=0, empty allow-list, `/btw`, and subagent.

### G2-04: Remove body text from logs

**Implementation:** Remove the full `messages_json`, tool result body, and memory text from DEBUG; keep request ID, entry ID, counts, recipient, reason code, and fingerprint.

**Acceptance:** Execute all log levels for a request containing a unique sentinel body; the sentinel does not appear in the logs; the fingerprint cannot be reversed to recover the body.

### G2 Rollback

If the unified helper causes compatibility problems, fall back to explicit checks at each call site, but do not directly restore unchecked background summarization. When rolling back the Seahorse isolation fix, disable the related tools first rather than restoring full-database reads.

## 9. G3: Migration, Backup, Ledger, and Recovery

### G3-01: Integrity plan and atomic initialization

**Implementation:**

- The pre-check covers basic tables, v4 tables, required columns, unique indexes, and FTS triggers, and first uses meta.schema_version to distinguish "legitimate v3 pending migration" from "v4 corruption".
- The fresh determination scans all known memory objects; initialization is allowed only when no business object exists at all. Any remaining object combined with missing meta/entries is treated as corruption.
- The non-rebuildable list includes business tables, memory_operations, memory_jobs, memory_change_plans, memory_origin_events, memory_domains, and the independent ledger; if missing, return non_repairable.
- The new database's base Schema, v4 objects, default domain, and version number complete in one transaction, with schema_version written last; loadRevision no longer writes the version in advance.
- `migrateSchemaLocked`, `ApplyMigration`, and `RepairData` all enter the same pre-check and backup protocol; after each maintenance, re-run the integrity plan, and do not clear pending unless complete.

**Acceptance:** Per-table/per-index missing tests; when meta/entries are deleted but other objects remain, it must not be judged fresh; after fault injection at each DDL point during initialization, the next Open is either fully usable or explicitly recoverable, with no fake v4.

### G3-02: Back up before any maintenance write

**Implementation:**

1. Perform a read-only pre-check and enter the maintenance write lock; if write-stop cannot be proven, do not continue.
2. Generate the main database `VACUUM INTO` and the independent ledger consistency snapshot in sequence, recording main revision, ledger UUID/keyID/max seq/pending count.
3. Verify that the backup opens, its integrity, ledger identity, and seq; mark a backup containing pending intents as requiring reconciliation after restore, not as a static completed state.
4. Execute the single-transaction modification.
5. Perform a final re-check and return the backup manifest; after releasing the maintenance lock, new obligations continue to be carried by the latest ledger.

When changes are needed, a new backup must be created; an idempotent no-change run returns `no_changes` and does not falsely claim a new backup. `last_migration_backup` must not be reused merely because the field is non-empty while the file may no longer exist. v4 repair must also back up first; ApplyMigration/RepairData must not call migrateSchema first and add the backup afterwards.

**Acceptance:** On backup failure, zero database changes; the main-database revision and ledger max seq/pending are written to the manifest; the second idempotent execution has zero data changes; UI/CLI display the real backup/no_changes.

### G3-03: Ledger identity, freshness, and pending reconciliation

**Implementation:**

- Identity creation uses `BEGIN IMMEDIATE`, a second read inside the transaction, conditional insert, and post-commit re-check.
- RecordIntent also queries by full request_id inside an immediate transaction: the same intent returns the existing row, and reusing a request_id with different content returns a conflict; a new unique index is not a prerequisite this round.
- VerifyLedgerBackup keeps identity matching semantics and additionally outputs backup_max_seq, active_max_seq, max_applied_seq, pending_count, and latest_digest; stale only indicates a watermark difference and is not equivalent to identity corruption.
- Recovery selects the latest valid ledger with the same identity; it never rolls back the latest ledger, and a backup ledger must not overwrite a higher seq of the active ledger.
- The intent's scope_json must bind plan/actor/approval/domain/expected_revision; historical pending entries missing the binding can only be reported.
- Pending intent reconciliation runs in a background maintenance worker and does not automatically change business state in Open:
  - when the main database has an operation with the same request_id, only retry MarkApplied; do not delete again;
  - when there is no operation and the approval binding is complete, follow manifest entry IDs + domain through the unified pipeline; a revision conflict becomes blocked;
  - when it cannot be determined, keep pending/blocked and report; blocked→pending requires an explicit requeue.
- The cleanup job ID, manifest, and intent request ID must be identical; the existence of a job alone must not be taken as proof that the main-database side effect completed.

**Acceptance:** Two processes opening for the first time do not produce two identities; a repeated request ID does not produce duplicate intents; MarkApplied failure, main transaction failure, intents without approval, revision conflict, and crash points all have recovery tests.

### G3-04: Historical forget reconciliation and recovery matrix

**Implementation:**

- Provide an explicit dry-run reconciliation entry point that outputs target_entry_id, time, matchability state, domain unknown/ambiguous, and locator completeness; it does not automatically write the ledger or delete.
- Historical operations are scanned read-only. Only when the target entry still exists in a restored copy, explicit legacy maintenance is active, and the owner confirms again is it handled as a new maintenance deletion action.
- Modern Replay is separated from historical reporting: Replay deletes by each intent's manifest entry IDs + domain, and no longer scans the whole database by digest.
- Read all applicable manifests; do not use the latest 500 entries as a silent cap; process in pages and report the total.

**Recovery matrix:**

| Scenario | Expected |
|---|---|
| Old main database + latest valid ledger | Recorded forget executions re-run, and derived cleanup is re-enqueued |
| New main database + old ledger | Keep the main-database state and warn that the ledger is stale; do not overwrite the latest ledger |
| Main database + ledger with a different identity | Fail closed |
| Main database + corrupted ledger | Forget/Replay fail closed |
| Historical forget has operation but no ledger | Only generate a candidate report; a maintenance action newly approved by the owner is recorded separately; if external cleanup cannot be proven, partial |
| Historical forget is missing even the operation | Explicitly cannot recover the obligation; do not claim completeness |
| The same body exists in two domains | Modern Replay deletes only the intent-specified domain/entry; historical hashes are not used for automatic deletion |
| More than 500 intents | Process all in pages, with no silent omission |

### G3 Rollback

Before code rollback, the latest main database and ledger must be preserved. Any binary rollback first verifies the Schema and deletion protocol that binary understands; do not promise that an old binary can directly operate on new data.

## 10. G4: Cross-Store Forgetting and Cleanup Closed Loop

### G4-01: Unified Forget/Replay pipeline

**Implementation:**

- Extract shared internal transaction steps: target validation, entry deletion, relationship handling, summary handling, operation, cleanup enqueue, revision.
- The independent ledger writes pending first; MarkApplied occurs only after the main-database transaction commits. The two SQLite databases have no common atomic transaction, so MarkApplied must not be written as part of the main-database transaction.
- Replay uses the same steps and locates by each intent's manifest entry IDs + domain; it no longer matches the whole database by digest, nor uses the Recent(500) silent cap.
- All actual Forget/Replay operations validate expected revision; when the target has changed, conflict and require re-approval.
- relationship prioritizes deleting provable copies by source_event_id; only when the entry is legacy, unmarked, and the predicate exactly matches nickname/birthday/anniversary is a restricted fallback allowed; other manual states, cross-domain same-key values, or values whose ownership cannot be proven are retained and reported unavailable.
- All failures preserve the independent ledger obligation and do not claim all copies complete because the main database reports AlreadyGone.

**Acceptance:** Normal Forget and recovery Replay produce the same final side effects; repeated request IDs are idempotent; same text across domains is not mistakenly deleted; a changed target revision is not deleted by an old approval.

### G4-02: Summary and derived-data handling

**Implementation:**

- For memstore summary versions containing the target entry, clear or rewrite `summary` and `claims`, preserving invalidated metadata that does not contain body text.
- Delete the corresponding summary evidence, evidence span, and dependencies.
- When the containment relationship of historical summaries cannot be precisely proven, conservatively delete/redact derivable active summaries in the same cluster and count the unverifiable part as partial.
- Before rebuilding a summary from remaining members, re-check domain, generation, and member revision.
- Within the SQLite transaction, perform only deletion/redaction and persist the rebuild task; any LLM rebuild runs outside the transaction, and a failed rebuild must not restore old body text.

**Acceptance:** Database queries no longer return forgotten body text; ListSummaries/API cannot read invalidated body text; rebuilt summaries do not contain deleted claims; no Provider call occurs inside the transaction.

### G4-03: Source root evidence and locator

**Implementation:**

1. Generate the origin event at trusted inbound, automatic extraction, and tool-call boundaries; public DTOs do not accept client-provided root, origin, session, or message identities.
2. Each source unit uses a unique origin event. When one entry comes from multiple messages or multiple root types, each source row stores a different origin/root; only a retry of the same origin is idempotently ignored.
3. Compute the root digest of the complete JSONL Content, tool-use arguments, tool result Content, or Seahorse content/text/tool-use part according to the actual source type, and write it into the versioned evidence_root.
4. SourceLocator and manifest carry root type, canonicalization, key ID, and digest in sync; `sourceLocatorsLocked` no longer generates new roots from the 120-rune evidence_text.
5. When old records have no root evidence, execute only strictly verifiable old-protocol matching; unknown versions, corrupted roots, and unprovable fragments are all reported unavailable.
6. A sourced write rejects and explains when root evidence is required but ledger identity is unavailable: this means the related automatic-extraction writes are paused, an explicit failure mode that must not be written as partial success. A manual record without a source may be saved but marked unlocatable.
7. JSONL uses session + root scanning; new Seahorse ingest records a session→conversation/local message mapping, and for existing data without a mapping, scan only within a provable conversation scope, otherwise unavailable.

**Acceptance:** Fragment evidence within a long message, complete long messages, multiple sources for the same entry, tool arguments, tool results, and Seahorse parts all have tests; sources are not silently collapsed by the unique key; root body text does not appear in the database or logs.

### G4-04: JSONL cleaner

**Implementation:**

- Provide session-level atomic mutate inside the existing `pkg/memory.JSONLStore`: flush, read, verify, rewrite, and return an error within the same shard lock; the Gateway obtains this capability from each Agent's active backend.
- External GetHistory→SetHistory and the error-swallowing SessionManager.SetHistory are prohibited; backends that cannot provide atomic capability remain blocked.
- Delete all verified duplicate matches, not just the first one per digest.
- The MVP supports Content, structured tool-use arguments, and tool result Content; it does not attempt unreliable HMAC on arbitrary JSON substrings, nor fabricate JSONL internal message IDs.
- Each locator returns removed/already_absent/unavailable/outcome_unknown and a reason code, without outputting body text.
- Persist per-backend, per-locator receipts in the existing job payload. When a backend has committed but the receipt was not persisted, a retry cannot prove "already deleted" and must report outcome_unknown, rather than directly already_absent or a permanent fake completion.

**Acceptance:** Duplicate messages in the same session, same text across sessions, long text, tool arguments, tool results, concurrent append, crash before/after receipt, and late writes all have tests; non-matching messages must not be lost, and "not found" must not be treated directly as deleted.

### G4-05: Seahorse cleaner

**Implementation:**

- Add transactional deletion methods in `pkg/seahorse` that verify authorized conversation, message/part, and root evidence; platform message_id must not masquerade as a Seahorse local ID.
- Ingest, Compact, CompactUntilUnder, ClearSession, asynchronous condensed compaction, and the new cleanup share the same per-conversation lock, with generation/tombstone to prevent late write-back.
- Handle messages, parts, summary_messages, context_items, and affected summaries in one transaction, reordering necessary ordinals.
- Current summaries have no invalidated field: use deletion or content redaction, and do not write a fake invalidated state. Persist the rebuild task in the same transaction or mark subsequent compaction for lazy rebuild.
- LLM rebuild runs only outside the transaction; on failure, keep the old summary deleted/redacted and report partial; original text must not remain.
- Do not call whole-session ClearSession in place of precise forgetting, unless the owner explicitly chooses whole-session deletion.

**Acceptance:** After forgetting, short_grep, short_expand, context assembly, and summaries cannot return the target content; other messages in the same session are retained; concurrency of Ingest/Compact/Clear/cleanup and rebuild failure all have tests.

### G4-06: Background jobs, retries, and feedback

**Implementation:**

- invalid manifest, cleaner error, missing locator, and partial match all add Unavailable/Details or a structured reason.
- The job payload stores per-backend/per-locator receipts, so that when JSONL is complete but Seahorse fails, only the failed backend is retried; the job overall is not a simple done/blocked binary state.
- A blocked job provides dry-run diagnostics and an explicit requeue; retry is allowed only after the locator is fixed or the owner explicitly accepts partial.
- ForgetPreview adds locator count, unlocatable count, cleanup job ID, and request ID.
- API/UI distinguish `accepted`, `partial`, `blocked`, `outcome_unknown`, `done`, `unavailable`; done covers only declared backends.
- CLI returns non-zero on partial/blocked/outcome_unknown; removed=0 does not by itself mean failure or success.
- The cleanup worker is decoupled from `memory.enabled`; accepted obligations continue to execute after long-term memory is disabled.

**Acceptance:** The 2 pending jobs in the reference database shape can be displayed, diagnosed, and retried on a temporary copy; single-backend success/single-backend failure can be recovered independently; when there is no evidence or the receipt is unclear, never show all complete; enabled=false does not stop cleanup.

### G4 Rollback

Before the cleaner goes live, run it in dry-run and compare the set of locators/roots to be deleted; for Seahorse also compare local message IDs, while JSONL must not fabricate message IDs. Before real execution, establish a consistency recovery point for the main database, ledger, JSONL, and Seahorse. If cleanup is wrong, stop the worker and rebuild the affected backends from the recovery point; do not overwrite the latest ledger with an old ledger.

## 11. G5: Retrieval, Domain, TTL, and Budget

### G5-01: Unified visibility conditions

**Implementation:**

- profile, boundary, Search, FTS, LIKE, temporal channel, 1-hop, and capsule use shared SQL condition construction; List and fixed blocks no longer accept only broad parameters that cannot express audience/TTL.
- status/domain/realm/audience/layer/TTL all take effect before LIMIT. Ordinary retrieval excludes expired entries by default; administrative/historical queries can read them only with explicit include_expired.
- Explicitly add filtering contexts for BuildFixedContextFor, BuildRetrievalContextFor, and LoadCapsuleFor, or provide safe legacy wrappers; do not change only SQL without fixing callers.
- FTS/LIKE no longer hard-code active; administrative queries filter by the requested status.
- 1-hop inherits the main retrieval layer and TTL constraints, and shares the ID set, quota, and final budget.

**Acceptance:** A large number of invisible candidates do not push out the public sentinel; pending/archived administrative retrieval does not miss old entries; expired entries are invisible on all ordinary paths, and explicit administrative historical queries have clear semantics.

### G5-02: Domain and capsule

**Implementation:**

- This round, ordinary runtime keeps an explicit legacy wrapper; non-legacy fixed/retrieval/capsule/relationship requests lacking a trusted domain fail closed, and domain is not filled in from model parameters.
- An empty domain does not escalate to administrator; the ordinary request default of legacy is used only for compatibility with unclaimed data.
- Store/Service reject cross-domain reads, overwrites, and deletions at the cluster and member, entry, and relationship layers.
- Before multi-domain relationship is reworked, reject non-compatible writes; management-surface global export is explicitly marked owner-only.
- Complete trusted domain propagation from bus→agent→tool→retrieval and opening multi-domain require a separate RFC and are not implicit deliverables of this round.

**Acceptance:** Domain A/B scenarios with the same text, same key, and same cluster ID cannot read, overwrite, or delete each other; untrusted non-legacy requests fail explicitly; legacy behavior remains usable.

### G5-03: Budget and long input

**Implementation:**

- Keep the budget normalization from configuration loading and the settings API, and make `WithLongTermMemory` reuse the same semantics; remove the egress budget short-circuit and the original erroneous test expectations.
- Use one total target budget; do not let fixed blocks at 45%, boundary at 30%, and base each consume their full share, creating additive overspend; when fixed blocks exhaust the budget, retrieval=0.
- Ordinary long text is billed by the final rendered text, including separators and 1-hop.
- Core rules must not silently drop subsequent entries; produce an explicit overflow/cut, and when the G0-frozen core total or single-item absolute cap is exceeded, return an error and do not call the Provider.
- relationship value and tool search results have caps; administrative enumeration with an empty query does not enter ordinary model tools.
- `EstimateSystemTokens` uses the actual assembly result and no longer directly adds the full BudgetTokens to fake real usage.

**Acceptance:** budget=0, a single 7.6KB boundary, multiple long blocks, 1-hop, and mixed Chinese/English fixtures; assert the final rendered estimate, overflow reason, and whether the Provider was called.

### G5 Rollback

New retrieval channels or long-text expansion can be temporarily disabled via configuration, but permission/TTL checks cannot be disabled. Budget fix rollback must not restore the "0=inject but skip egress" combination.

## 12. G6: API, CLI, UI, and Configuration

### G6-01: Dangerous operation contracts

- API forget defaults to dry-run; real execution by the dashboard owner requires explicit `confirm=true`, expected_revision, a host actor, and the existing CSRF/administrative authentication; it does not force another plan layer for every owner deletion.
- Model forget can always only preview; owner plans are used for actions requiring deferred approval and cannot be injected with parameters by the model.
- CLI forget continues to preview by default; `--yes` represents owner approval and writes actor/request/revision audit.
- Replay adds dry-run/yes, previewing the cleanup impact on entries, relationships, summaries, and each backend.
- AlreadyGone, blocked_forget, accepted, partial, outcome_unknown, and done use different statuses; do not lump them all under HTTP 200.

### G6-02: Settings updates and configuration compatibility

- The settings endpoint uses field-existence merge; the PUT path may be retained for old frontend compatibility, but decoding must distinguish absent, explicit false, and explicit empty array; simply renaming struct PUT to PATCH is not enough.
- All published fields, including `allowed_chat_recipients`, private exclusions, enabled, extract_model, summary/LLM extraction/semantic/proactive/tool forwarding/sensitivity capture, keep their original values when omitted.
- Complete the frontend MemorySettings type in sync; also verify whether the `/api/config` full update indirectly clears memory security fields.
- Keep all published JSON fields; deprecated fields use Deprecated + clear/warn + compatibility tests.
- `ledger_path` is explicitly marked unsupported/deprecated this round; do not wire it without an identity migration protocol, and do not provide a fake switch.
- Both `"memory":{"db_path":...}` and `"memory":null` have explicit compatibility semantics; partial configurations retain secure defaults and are not silently disabled by raw substring checks.
- V4 switches that are not wired up show their real status in the UI; do not provide fake switches.

### G6-03: Status page and CLI operability

- Distinguish ledger pending intent from cleanup job backlog.
- Display job state, attempts, next_run_at, age, structured errors, and requeue entry points.
- Split `NeedsDataMigration` into schema pending and independent data-quality hints; an empty predicate no longer causes a permanent migration hint.
- Text accurately describes backup, no_changes, partial, and cleanup coverage.
- audit/render/capsule limits, channels, budgets, and exclusion conditions are clearly displayed; do not pass off silent truncation as the full set.

### G6-04: Tests

- Add table-driven command tests for `cmd/picoclaw/internal/memory`.
- API adds tests for forget, correct, pinned, plans, ledger/replay, jobs, cleanup, blocked add, settings retention, and second migration.
- This round does not force the frontend to introduce a new test framework; lint/build and manual UI smoke are required. If a framework is introduced, approve it separately as an environment change.

### G6 Rollback

API contract changes retain explicit errors and migration notes; do not restore default hard deletion to be compatible with old scripts. After default dry-run, old scripts may silently become preview-only; `executed=false` must be prominently surfaced in responses, the CLI, and the migration guide, not merely relying on HTTP 200. The frontend may roll back the presentation layer, but the backend must still return real partial.

## 13. G7: Real Data Quality Measurement and Follow-up Candidates

This round G7 commits only to the measurement and reporting in G7-03/G7-04. G7-01/G7-02 are follow-up independent candidates, are not part of this round's definition of done, and do not block the G0–G6 security remediation.

### G7-01: Follow-up candidate—long-document summarization and expansion

If separately chartered later, prefer reusing the existing clusters, cluster members, summary versions, and summary evidence: chunk long text by structure, inject a short summary by default, expand complete chunks on demand, and preserve member revision, generation, and evidence. The external summary model is disabled by default; do not automatically bulk-split the existing 110 active entries, and blind-test on a small sample first.

### G7-02: Follow-up candidate—time and source quality

Re-evaluate event time, relative dates, ranges, and time zone extraction later. created_at is not equal to event_at; keep unknown when it cannot be determined. New writes record only genuinely existing origin, model, prompt, actor, and root evidence; when local rules have no external model, leave them empty rather than filling fake values for "field completeness". Old records do not generate fake times, sources, or confirmations.

### G7-03: Recall quality evaluation

Establish an owner-authorized, restricted-retention evaluation set:

- Cover real/fictional/work, private chat/group chat, short query/long query, temporal query, garbage query, and negative conflicts.
- Each case records expected entry ID, forbidden entry ID, domain, audience, and whether it should be expanded.
- Private body text and queries are not committed to the repository; the repository contains only synthetic fixtures.
- Report hit rate, forbidden hits, final tokens, expansion rate, latency, and partial; do not use non-empty results or Add counts as quality.

The 96 zero-use records serve only as observational clues; do not automatically delete or archive them.

### G7-04: FTS and deduplication measurement

- Compare trigram, LIKE fallback, and candidate tokenizers on the same query set.
- Record real inventory size, index size, latency, hits, and forbidden hits.
- When freelist=0, do not treat VACUUM as an index-shrinking solution.
- Near-duplicate merging adds negative/conflict cases; do not perform large-scale automatic merging without evidence.
- Semantic retrieval is evaluated only after blind tests prove benefit; prefer local solutions and re-check deletion synchronization.

### G7 Acceptance

The quality report must include positive cases, negative cases, permissions, timeliness, and cost. Any "accuracy improvement" must state the sample size, data version, and whether the real inventory was used.

## 14. API and Status Contract Summary

### 14.1 Forget result

Recommended result structure; fields may be adjusted to the existing style during implementation:

```json
{
  "status": "accepted|partial|blocked|outcome_unknown|done|already_gone",
  "request_id": "...",
  "entry_id": "...",
  "expected_revision": 3,
  "ledger_seq": 12,
  "cleanup_job_id": "forget:...",
  "locators": 2,
  "unavailable": 0,
  "backends": {
    "jsonl": "done|partial|blocked|outcome_unknown|not_applicable",
    "seahorse": "done|partial|blocked|outcome_unknown|not_applicable"
  },
  "details": []
}
```

`accepted` only means the main-database phase accepted; `done` is used only when all declared backends are complete; `outcome_unknown` means a crash occurred between the backend and the receipt, making it impossible to prove the target was deleted or never existed. Backups, exports, shared media, or unknown copies that cannot be covered must be explicitly listed in the boundary, and done must not imply that the physical world is completely cleared.

### 14.2 CleanupOutcome

Contains at least:

- backend;
- removed, already_absent, unavailable, outcome_unknown, sessions/conversations;
- blocked, retryable;
- request/job ID and per-locator persistent receipt;
- structured reason code;
- details that contain no body text.

### 14.3 Operation audit

Every new operation can be associated at least with: actor, full request, plan/approval, target, domain, reason, policy revision, ledger seq, and cleanup job. Entry operations also associate expected revision; relationship uses genuinely existing preconditions and does not fabricate a revision. Audit stores only metadata, not secret body text.

## 15. Configuration and Compatibility Matrix

| Scenario | Required behavior |
|---|---|
| Old configuration contains deprecated private_channels | Still loadable, cleared, and warned once |
| Partial configuration with only memory.db_path | Preserve enabled and secure defaults; do not silently disable |
| memory:null | Explicitly handled as compatibility defaults with regression tests; not misjudged by raw substring |
| settings omit recipients/V4 fields | Keep original values; only explicit false/empty array performs a change |
| recipients explicitly empty and in compatibility mode | Keep compatibility-permissive, clearly displayed on the status page |
| enabled=false | Stop long-term memory assembly/learning; summary egress is judged independently; accepted cleanup continues |
| sourced write requires root but ledger is unavailable | Pause related writes with an explicit alarm; do not generate rootless sources |
| budget_tokens=0 | Keep the normalized default value; do not skip egress |
| Unwired V4 switches | UI/API show unsupported or do not expose them; cannot pretend they take effect |
| New binary reads old v3 | Continue to fail closed and require explicit migration |
| Existing database missing basic business tables | Fail closed and guide recovery; do not auto-fill empty tables |
| Missing derived tables/indexes | Repair after explicit backup, with final re-check |

Every configuration field change adds a regression test that "config.json written by the previous version still loads".

## 16. Verification Plan

### 16.1 Unit and contract tests

Run according to the scope of changes:

```sh
go test -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=120s
go test -tags 'goolm stdjson' ./pkg/agent ./pkg/tools ./pkg/tools/shared -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./pkg/memory ./pkg/session ./pkg/seahorse -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./web/backend/api ./cmd/picoclaw/internal/memory -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./pkg/config -count=1 -timeout=5m
```

### 16.2 Concurrency and fault injection

```sh
CGO_ENABLED=1 go test -race -tags 'goolm stdjson' \
  ./pkg/memstore ./pkg/memory ./pkg/seahorse ./pkg/agent \
  -count=1 -timeout=10m
```

Must cover:

- two-process ledger identity creation and repeated request IDs;
- same-session append and cleanup, crash before/after receipt;
- Seahorse Ingest/Compact/Clear/condensed compaction and cleanup;
- plan payload injection, domain/digest/revision validation, claim and duplicate approval;
- MarkApplied failure after Forget main-database commit, intents without approval, and revision conflict;
- egress matrix for `/btw`, subagent, summarization, streaming, retries, and fallback;
- pending cleanup continues to execute when `memory.enabled=false`;
- configuration absent/false/empty array, `memory:null`, and old-configuration round-trip;
- backup failure, disk full, job lease expiry, and retry exhaustion.

### 16.3 Frontend

In `web/frontend`:

```sh
pnpm run lint
pnpm run build
pnpm run format
```

When backend embedded resources need updating, run `pnpm run build:backend` separately and confirm whether the generated assets should be committed. A normal build is not the same as updating backend dist.

### 16.4 Real recovery drill

Execute only on a copy in a newly created temporary directory:

1. Record the main database and ledger hashes, revision, seq, entry/job counts.
2. Create recovery points for the main database and ledger.
3. Drill normal Forget, main-database rollback, Replay, partial, and repeated Replay in sequence.
4. Check the main database, JSONL, Seahorse, summaries, and job status.
5. Verify that unknown sources still report unavailable.
6. Compare final counts and target ID sets, not just the removed number.
7. The original reference directory has the same hash before and after the drill.

### 16.5 Documentation and release verification

For documentation changes, run:

```sh
git --no-pager diff --check
make lint-docs
```

If the user separately requests a release, then follow the repository release process to execute the full build, frontend checks, Chinese release notes, CHANGELOG, annotated tag, and manual workflow. This plan itself does not trigger these steps.

## 17. Observability and Privacy

Record only:

- request/job/plan/entry IDs;
- domain, backend, status, reason code;
- locator/removed/unavailable counts;
- job age, attempts, lease, and latency;
- recipient provider/model and policy result;
- irreversible versioned form of content fingerprint (when truly needed).

Prohibited from recording:

- memory body text, raw chats, complete tool arguments;
- raw queries, summary body text, and evidence span;
- HMAC key, token, password, and configuration secrets;
- detailed context that can be directly used to reconstruct private content.

Dashboard metrics must not use `done` as the sole health criterion; they must also show unavailable, blocked, and the age of the oldest pending.

## 18. Overall Rollback Plan

### 18.1 Code rollback

- Commit each defect family independently, avoiding one giant governance commit.
- When rolling back security switches, disable dangerous capabilities by default; do not re-open bypasses.
- Evaluate callers first for public signature changes in Provider, Seahorse, retrieval, etc.; do not delete published configuration fields for rollback.

### 18.2 Data rollback

- The main database and ledger are two independent recovery objects and have no common atomic snapshot.
- Restoring an old main database must use the latest valid ledger and replay obligations.
- Do not overwrite the current ledger with an old ledger from a backup.
- Recovery points before JSONL/Seahorse cleanup may only be used for authorized drills or explicit recovery operations.
- If recovery would reintroduce forgotten content, stop writes and execute Replay/Cleanup before opening the service.

### 18.3 Release rollback

This plan does not authorize release. If a release happens later, record the compatibility matrix of binary, Schema, configuration, ledger protocol, and cleanup protocol; do not merely roll back the binary.

## 19. Risk Register

| Risk | Probability/Impact | Mitigation and gate |
|---|---|---|
| Fixes cover only the Store; model/API still have bypasses | High/High | Four-layer adversarial tests; public DTO; service approval |
| Long evidence switched to root HMAC still cannot locate the fragment source | Medium/High | Save the complete source root and stable message identity at inbound; old data partial |
| Cleanup mistakenly deletes same-text messages in a different domain/session | Medium/High | backend + domain + session + origin + root; Seahorse additionally validates local IDs |
| Cleanup races with append/compaction | Medium/High | Active instance, session lock, generation/tombstone, race and crash tests |
| Backend already deleted but receipt not written, causing fake completion/double deletion | Medium/High | Per-locator receipt; outcome_unknown when unprovable |
| Owner plan becomes an internal field injection channel | Medium/High | owner-only, typed params, unknown field rejection, digest/domain/revision double-check |
| Creating empty tables to fix missing tables creates a data illusion | Low/High | Existing databases missing business tables directly require recovery |
| Two-process ledger identity overwrite | Medium/High | BEGIN IMMEDIATE and two-process tests |
| Multi-domain relationship fix becomes unauthorized Schema expansion | Medium/High | Fail closed this round; file a separate RFC |
| Core boundary truncation changes meaning | Medium/High | No silent truncation; explicit overflow/hard error |
| Configuration fix causes old configuration to fail startup | Medium/High | Old-configuration regression tests; fields only added, never deleted |
| API default preview breaks old scripts | High/Medium | Explicit contract and migration notes; safety takes precedence over dangerous compatibility |
| Long-text chunking reduces writing quality | Medium/Medium | Small-sample blind test; do not bulk-modify the old database by default; expandable |
| FTS adjustment reduces Chinese recall | Medium/Medium | Compare on the same dataset before switching; keep a rollback-capable implementation |
| Security plan publicly discloses unfixed defect details too early | Medium/High | Local documentation only before fixes; do not push to public upstream |

## 20. Documentation, Commit, and Release Constraints

When implementing behavior changes, synchronize at least:

- `docs/guides/memory.md` and `memory.zh.md`;
- migration-related changes synchronize `docs/reference/memory-schema-v4.md` and `.zh.md`;
- the real state of API/CLI/UI, dangerous-operation defaults, and partial semantics;
- configuration field compatibility and old-configuration tests;
- if the user explicitly requests a release, then update `CHANGELOG.md` and `docs/releases/<tag>.md`.

This plan's newly added document contains classifications of unfixed defects; **it is not recommended to push it to public upstream before the corresponding fixes are public**. This export is local only: no commit, no tag, no Release trigger.

The historical V4 design document is only used to trace requirements. Do not restore the old migration chain or write plan status as current implementation status merely because the old document contains M4/M5/M6 or "implemented".

## 21. Final Acceptance Checklist

### Security and governance

- [ ] Forgotten content cannot be resurrected through Add, Correct, API, tools, import, or plans.
- [ ] The model cannot self-approve, create execution plans, forcibly modify core status, or expand domain and audience.
- [ ] All new operations have trusted actor, full request, and approval associations.
- [ ] Seahorse and JSONL session/Agent isolation pass cross-negative cases, and model all_conversations is rejected.
- [ ] Main chat, /btw, subagent, summarization, streaming, retries, and fallback pass purpose/recipient checks, and logs contain no body text.

### Forgetting and recovery

- [ ] Normal Forget and Replay have identical derived-data handling.
- [ ] Long messages, duplicate messages, tool arguments, and Seahorse parts have real cleanup tests.
- [ ] Unverifiable copies always report partial/unavailable; when the receipt is unclear, report outcome_unknown.
- [ ] `memory.enabled=false` does not stop accepted cleanup obligations.
- [ ] After main-database rollback, the latest ledger can replay obligations; an old ledger does not overwrite the new ledger.
- [ ] When maintenance backup fails, zero modifications; initialization and repair fault injection pass.

### Retrieval and configuration

- [ ] All path permissions and TTL precede LIMIT.
- [ ] Cross-domain same text, same key, and same cluster do not read, overwrite, or delete each other; untrusted non-legacy requests fail closed.
- [ ] budget=0, long boundary, long relationship, and 1-hop do not break budget and egress.
- [ ] Old configuration loads, and partial settings updates do not clear security fields.

### Management and quality

- [ ] API/CLI/UI distinguish accepted, partial, blocked, outcome_unknown, done, and already_gone.
- [ ] CLI memory subcommands have dangerous-operation and exit-code tests.
- [ ] Frontend lint/build pass, and real UI smoke covers migration, forgetting, and job status.
- [ ] Quality evaluation includes negative cases, forbidden hits, tokens, and latency; long-text/temporal implementations are not passed off as this round's delivery.
- [ ] Documentation does not write plans, configuration fields, or main-database applied as fully implemented/fully forgotten.

## 22. Recommended Kickoff Order

Start only three mutually independent work lines in the first batch:

1. G1-01/G1-02: tombstone and model approval, with adversarial tests.
2. G2-01/G2-03: Seahorse isolation and background summarization egress.
3. G3-01/G3-02/G3-03: atomic initialization, backup first, and ledger identity.

The second batch completes G4, then opens G5/G6. G7 measures only with synthetic or authorized copies and does not block security fixes.

Before starting, each implementation task must answer:

- Which user behavior changes?
- Which write entry points and actual send points are touched?
- Does it produce new sensitive copies?
- How is it forgotten, retried, and recovered?
- Which final-state assertion proves completion?
- Which operations require separate explicit authorization from the owner?

## 23. Grilling Review and Final Audit Record

### 23.1 Review method and boundary

Four rounds of design-tree review were executed using the user-confirmed `grilling` skill, and two independent read-only agents separately verified the delivery feasibility of forgetting/recovery and security/retrieval. Under the user's prior authorization to "fill in the recommended options", Q1–Q20 all adopted the recommended answers; this does not mean the user reconfirmed each item, nor does it constitute authorization for code, database, or release work.

During the review, no gateway was started, no real database was accessed or modified, and no business code was modified. The status of this document is a **final implementation plan**, not an implementation completion report or security certification.

### 23.2 Four-round decision results

| Question | Adopted recommendation |
|---|---|
| Q1 | Core remediation stays on Schema v4; no v5 |
| Q2 | Main database, each backend, and unknown copies are reported separately; done does not overstate scope |
| Q3 | Restore backup when an existing database is missing business state tables; do not auto-fill empty tables |
| Q4 | Dangerous APIs default to preview; internal/unknown fields are rejected |
| Q5 | Empty recipients keep compatibility-permissive behavior with explicit notice; strict mode is delivered separately |
| Q6 | budget=0 normalizes to default; only enabled=false disables long-term memory |
| Q7 | Core rules are not silently truncated; overflow is bounded and observable |
| Q8 | No new frontend test framework this round |
| Q9 | Real data operations still require separate authorization; drills use temporary copies only |
| Q10 | Unfixed-defect plans remain local until public fixes |
| Q11 | Save approval context at claim; uncertain results are not marked failed directly |
| Q12 | Pending is reconciled by a maintenance worker; Open does not automatically change business state |
| Q13 | JSONL read→verify→rewrite completes in a single-session critical section |
| Q14 | Summary body/claims are cleared and rebuilt outside the transaction |
| Q15 | Ordinary clean chat and derived-data egress are handled by separate purposes |
| Q16 | Model sensitive actions are directly rejected; plans serve only the owner |
| Q17 | Multi-domain fails closed this round; complete domain pushdown requires a separate RFC |
| Q18 | Seahorse keeps a single Engine and enforces Agent/session/conversation isolation |
| Q19 | Historical forget is report-only; no automatic deletion or journal backfill |
| Q20 | Long-text/temporal implementation moves out of this round; quality and FTS measurement remain |

### 23.3 Substantive revisions made after independent verification

1. Changed "model creates plans" to "model is directly rejected", reducing new attack surface.
2. Changed owner plans to typed params, an independent full request ID, saving approval context at claim, and terminal-state-based payload clearing.
3. Extended the DTO closure to all write endpoints and required unknown fields to return 400.
4. Clarified the actor as an installation-instance entry identity, without fabricating a currently non-existent human principal.
5. Changed evidence_root to a host-derived versioned root protocol; one origin event has one root, and SourceLocator/manifest are upgraded in sync.
6. Historical forget is strictly report-only; modern Replay switches to manifest entry IDs + domain, removing whole-database digest deletion and the Recent(500) cap.
7. Completed the fresh determination and non-rebuildable object list; separated v3 pending migration from v4 corruption.
8. Changed JSONL to atomic mutate inside the active store; offline CLI uses a maintenance lock and PID double-check.
9. Seahorse summaries use deletion/redaction; all reads/writes/compaction/cleanup share the conversation lock, and LLM rebuilds only outside the transaction.
10. Persist per-backend/per-locator receipts; add outcome_unknown when a crash makes it unprovable.
11. Add `/btw` and subagent send points, and the hard constraint that cleanup obligations still execute when enabled=false.
12. Multi-domain, per-Agent Engine, long-text chunking, and temporal extraction are no longer implicit deliverables of this round.

### 23.4 Final consistency audit

| Audit item | Result |
|---|---|
| Still requires Schema v5 by default or new dependencies | No |
| Retains model self-approval or model plan injection paths | No; plans are owner-only with typed params |
| Automatically deletes by domain-less historical hashes | No; history is read-only reporting |
| Confuses JSONL platform message_id with internal IDs | No; JSONL uses session + root scanning |
| Confuses Seahorse local IDs with platform IDs | No; explicit mapping is established |
| Writes the two SQLite databases as a common atomic transaction | No; ledger pending→main-database transaction→MarkApplied |
| Calls LLM rebuild inside a database transaction | No; delete/redact inside the transaction, rebuild outside |
| Treats "not found after deletion" directly as success | No; persistent receipt + outcome_unknown |
| Stops cleanup because of memory.enabled=false | No; obligations execute independently |
| Omits `/btw`, subagent, or background summarization egress | No; included in the send-point matrix |
| Claims multi-domain, long-text, or temporal features are implemented/delivered this round | No; explicitly follow-up RFC/candidates |
| Retains configuration fields and distinguishes absent/false/empty array | Yes |
| Passes off plan status as implementation status | No |

### 23.5 Final review conclusion

**Document review conclusion: passed; usable as input for G0 freezing and engineering breakdown.**

"Passed" only means: goals, scope, dependencies, key protocols, failure semantics, tests, rollback, and release gates form an executable plan, and the self-contradictions found in the initial draft have been corrected. It does not mean:

- Any P0/P1 defect has been fixed;
- Existing tests already cover the new protocols;
- The real database can be migrated, replayed, or cleaned directly without confirmation;
- Multi-domain relationship, complete domain injection, or long-text summarization has been delivered;
- Core algorithm/authentication changes can be implemented while skipping the owner's governance red-line authorization.

The next gate at implementation time is: follow G0 to first commit failing tests and a precise change list; when core governance, database maintenance, public API signatures, or destructive operations are involved, obtain explicit approval separately per repository rules.

### 23.6 Independent blocking final review

The independent final review initially returned BLOCK, pointing out that the model action allowlist was unclear, the owner plan's blocked state was undefined, and "terminal state" was misused; it also recommended restricting entry revision, unifying the legacy predicate fallback and the historical forget operation count. After all five items were revised, the same final-review agent re-checked and returned **PASS**, confirming that no new direct contradictions were found.

This PASS is the final document consistency conclusion; it does not replace post-implementation code tests, fault injection, real-copy recovery drills, or security acceptance.
