# AMQ 0.49.0 sole-support and leverage assessment

AMQ 0.49.0 is the sole supported release for amq-squad. Both real-AMQ CI
matrices run pinned `v0.49.0` plus `latest`; `latest` is only a canary for an
unexpected post-final release. Older AMQ versions fail the doctor and
compatibility admission checks.

Historical capability boundaries remain named in code for archaeology:

| Capability | First AMQ release |
| --- | --- |
| Complete injected identity tuple | 0.42.1 |
| Reply refs | 0.43.1 |
| Exact wake retirement | 0.45.0 |
| Owner-bound coop wake lifecycle | 0.47.0 |
| Fixed shell-inert `coop exec` doorbell | 0.47.1 |
| Configured-mailbox repair | 0.48.0 |

They are not compatibility lanes or reasons to preserve below-floor branches.

## Message trace

AMQ 0.49.0 adds `amq trace <message-id> --root <path> --json`, a read-only join
over evidence already present in one queue root. It can correlate current
message copies, persisted addressing, DLQ entries, receipts, and thread refs.

Decision: use trace as an operator diagnostic, but do not consume it as
authoritative input to amq-squad's immutable command-evidence, delivery-receipt,
claim-once, retry, or gate decisions.

The upstream `amq/trace/v1` contract explicitly does not prove the original
directory-sync result from current file presence and reports notification
history as `no_evidence` until a durable notification-attempt ledger exists.
Those limits match amq-squad's committed-indeterminate and do-not-resend
boundaries. Treating trace as stronger authority would weaken them.

No local follow-up is filed for Phase A. Re-evaluate integration only when
upstream trace gains durable notification-attempt evidence or another new
artifact that can strengthen, rather than project, amq-squad's existing
receipts.

## Doctor discovered-mailbox remedies

AMQ 0.49.0 adds actionable remedy text for discovered-only malformed mailboxes:
register a valid handle and run explicit repair, or preserve messages and remove
an abandoned mailbox. Invalid entries receive a preserve-then-rename/remove
remedy.

Decision: keep ownership in upstream AMQ. `amq-squad doctor` continues to run
the read-only `amq doctor --ops --json` health check and does not duplicate
mailbox-inventory parsing or invoke `--fix-mailboxes`. Operators use `amq doctor
--json` for canonical remedy text and opt into `amq doctor --fix-mailboxes
--json` themselves. This preserves the v0.48 contract that repair is explicit,
non-destructive, and fail-closed.

No follow-up is required: the missing operator guidance was fixed upstream and
the amq-squad README now routes directly to it.

## Wake retirement

The pre-0.47 fixture workaround that ignored `wake retire` refusal is removed.
AMQ 0.49.0's `coop exec --wake-inject-via` helper is owner-bound: the coop
member now releases that exact wake through `wake recover-owner`, authenticated
by the inherited `AMQ_WAKE_OWNER` token and matching OS session. The fixture
asserts prompt exit and absence of the claim. The ownerless `wake retire` path
correctly refuses this owner-bound state; when the owner is already dead,
`recover-owner` remains the explicit fail-closed recovery path and does not
require a live-owner token.

For amq-squad-managed ownerless inject-via wakes, exact `wake retire` remains
the required shutdown contract whenever a launch record has persisted injector
identity. Refusal fails closed and never falls back to generic process
signaling. Raw wakes or historical records without inject-via identity retain
the bounded exact-PID signal path because AMQ cannot address those wakes by
injector identity.
