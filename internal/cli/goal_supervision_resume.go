package cli

import (
	"fmt"
	"strings"
)

// PR5 / #498: the claim-once native /goal resume. This file owns the CLAIM MACHINERY and no
// store: every durable write goes through the one recovery-transition CAS owner in
// resume_goal.go, per the ruling that a parallel store is the two-deciders defect at the exact
// spot whose failure mode is DOUBLE DELIVERY of an audited resume.
//
// Each eligibility clause below quotes #498 verbatim beside its check, so review is
// contract-to-code rather than re-deriving the design. Where the check is "consult the
// assessment", that is deliberate and ruled: assessment.Eligible is AUTHORITATIVE and this
// executor does not maintain a second eligibility policy. Two places computing one policy is
// how #573's preview and admission came to disagree.

// supervisionResumeRefusal explains a refusal in the operator's terms, always with a recovery
// command. A refusal without a next action is a dead end the operator cannot clear.
type supervisionResumeRefusal struct {
	Clause   string // the #498 contract clause that was not satisfied
	Detail   string
	Recovery string
}

func (r supervisionResumeRefusal) Error() string {
	msg := "native goal resume refused: " + r.Detail
	if r.Clause != "" {
		msg += " [#498: " + r.Clause + "]"
	}
	if r.Recovery != "" {
		msg += "\nRecovery: " + r.Recovery
	}
	return msg
}

// supervisionResumeStep is the ruled sequence, named so the audit trail and the tests refer to
// the same steps rather than to line numbers.
type supervisionResumeStep string

const (
	stepFinalAssessment supervisionResumeStep = "final-assessment"
	stepExclusionReads  supervisionResumeStep = "exclusion-reads"
	stepReserve         supervisionResumeStep = "reserve"
	stepBind            supervisionResumeStep = "bind"
	stepCrossKindRescan supervisionResumeStep = "cross-kind-rescan"
	stepDeliver         supervisionResumeStep = "deliver"
	stepConsume         supervisionResumeStep = "consume"
)

// executeSupervisionResume runs the confirmed sequence. The ordering is the contract, not a
// preference, and each deviation has a named failure mode:
//
//		final assessment -> exclusion reads -> RESERVE -> bind -> CROSS-KIND RESCAN -> deliver -> consume
//
//	  - reserve BEFORE deliver, so a crash between them reads as INDETERMINATE rather than
//	    undelivered. #498: "no earlier resume claim for this pause generation has been delivered
//	    or remains indeterminate". Reserving after delivery would make a crash look fresh and
//	    permit a second audited resume.
//	  - no reassessment after the reserve, because this actor's own claim write changes claim
//	    evidence and therefore the fingerprint; a post-claim recheck would fail every time and
//	    read as a bug in the assessment surface rather than as the expected self-write.
//	  - the cross-kind rescan is NOT a reassessment. It reads the ledger only -- never the
//	    fingerprint, never eligibility -- so the no-post-claim-reassessment rule is untouched.
func executeSupervisionResume(assessment GoalSupervisionAssessment, deliver func(string) error) error {
	// STEP 1. #498: "Automatic native resume is allowed only when all of the following remain
	// true in ONE FRESH ASSESSMENT". Freshness and eligibility are the assessment's to decide.
	if !assessment.Fresh {
		return supervisionResumeRefusal{
			Clause: "one fresh assessment",
			Detail: fmt.Sprintf("assessment observed at %s is no longer fresh (valid until %s)", assessment.ObservedAt, assessment.FreshUntil),
			// Not an error the operator fixes; it is a retry with a current observation.
			Recovery: "re-run the supervision sweep; a stale assessment is never delivered from",
		}
	}
	// #498: "Unknown or contradictory evidence is ineligible, not a best-effort resume."
	// Consumed, not re-derived: the assessment enforces unknown/stale/missing/ambiguous.
	if !assessment.Eligible {
		return supervisionResumeRefusal{
			Clause:   "unknown or contradictory evidence is ineligible, not a best-effort resume",
			Detail:   "assessment reports ineligible: " + summarizeEligibilityReasons(assessment.Reasons),
			Recovery: assessment.Actions.Inspect.Command,
		}
	}

	// STEP 1b, the POLICY BOUNDARY -- and this was absent from the first scaffold entirely.
	// #498 operator-policy clause: manual and notify-only profiles must not silently enter the
	// automatic executor. Eligible answers "is this actor resumable"; it does NOT answer "is
	// automatic action permitted here". Conflating them is how an operator who chose
	// notify-only gets an automatic /goal resume. Checked BEFORE any reserve, and it does not
	// weaken Eligible as the sole eligibility conjunction.
	if !assessment.AutomaticResumeAllowed {
		return supervisionResumeRefusal{
			Clause:   "operator policy: automatic resume permitted",
			Detail:   "this profile does not permit automatic native resume (manual or notify-only)",
			Recovery: assessment.Actions.Inspect.Command,
		}
	}

	// STEP 2, the exclusion reads. All of them precede any write, and every one fails CLOSED.
	// #498: "no earlier resume claim for this pause generation has been delivered or remains
	// indeterminate".
	//
	// The claim key CONSUMES assessment.Binding.PauseGeneration. It does NOT recompute it.
	// PR4 alone derives it from captured LaunchID + Goal.BindingDigest + Goal.AttemptID +
	// Goal.Mode, and a second derivation owner reading a different or current snapshot -- or
	// drifting from the approved formula -- is the one-decider failure this whole milestone
	// keeps producing. My first scaffold computed the hash here, which would have made this
	// file a second owner of an identity PR4 owns.
	key, keyErr := supervisionClaimKey(assessment.Binding.NamespaceID, assessment.Binding.PauseGeneration, assessment.Binding.Goal.AttemptID)
	if keyErr != nil {
		// A blank component would silently collapse distinct pauses onto one key, so the key
		// builder refuses rather than hashing an incomplete identity. Surfaced, not swallowed.
		return supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "cannot derive the claim key for this pause: " + keyErr.Error(),
			Recovery: assessment.Actions.Inspect.Command,
		}
	}
	// BOTH derivations are scanned, which is the whole point of the kind-agnostic ledger. The
	// legacy key is redelivery's own identity for this attempt: if a redelivery reservation
	// exists for the same attempt, it is an authoritative existing claim on this pause even
	// though it lives under a different filename derivation, and a scan that looked only at the
	// current key would step straight over it into a second delivery.
	dir := goalAttemptDir(assessment.Binding.Project, assessment.Binding.Profile, assessment.Binding.Session)
	legacyKey := resumeGoalTransitionID(assessment.Binding.Goal.AttemptID, assessment.Binding.Goal.BindingDigest)
	existing, err := scanRecoveryTransitionsForPause(dir, key, legacyKey)
	if err != nil {
		// A ledger that cannot be read is not an empty ledger. This is the
		// absence-as-evidence class that both #577 and PR4 were caught by.
		return supervisionResumeRefusal{
			Clause:   "no earlier resume claim ... delivered or remains indeterminate",
			Detail:   "cannot read the recovery transition ledger for this pause: " + err.Error(),
			Recovery: "inspect the transition directory; an unreadable ledger is never treated as vacant",
		}
	}
	if blocker := existing.blocking(); blocker != nil {
		return supervisionResumeRefusal{
			Clause:   "no earlier resume claim ... delivered or remains indeterminate",
			Detail:   blocker.describe(),
			Recovery: blocker.recovery(),
		}
	}

	// STEP 3. RESERVE through the single CAS owner. The record AND its path both come from the
	// one constructor -- this executor derives neither, so there is no second path owner and
	// nothing for the AST pin to catch. publishGoalJSON's link(2) is the first-writer primitive;
	// a lost race is a refusal, never an overwrite. (It is NOT O_EXCL; I had assumed that and was
	// wrong, and an enforcement test written against the assumption detected nothing.)
	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: assessment.Binding.Project,
			Profile: assessment.Binding.Profile,
			Session: assessment.Binding.Session,
			Role:    assessment.Binding.LeadRole,
			Handle:  assessment.Binding.LeadHandle,
		},
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         assessment.Binding.NamespaceID,
		AttemptID:           assessment.Binding.Goal.AttemptID,
		PauseGeneration:     assessment.Binding.PauseGeneration,
		PreclaimFingerprint: assessment.Fingerprint,
	})
	if err != nil {
		return supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "cannot construct the resume claim: " + err.Error(),
			Recovery: assessment.Actions.Inspect.Command,
		}
	}
	reservation, err := reserveRecoveryTransition(record, path)
	if err != nil {
		return supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "could not reserve the resume claim: " + err.Error(),
			Recovery: "another actor holds or held the claim for this pause; inspect the ledger",
		}
	}

	// STEP 4. BIND the launch generation, so a generation change after reservation is DETECTED
	// rather than assumed stable -- the same protection redelivery already takes.
	//
	// The generation is CONSUMED from the assessment, not re-read here. PR4 captured
	// LaunchRecordDigest/LaunchRecordModTime as part of the observation this claim is being made
	// against; re-reading them would bind the claim to a DIFFERENT moment than the one that
	// authorised it, and would quietly make this file a second observer of a generation PR4 owns.
	if err := bindRecoveryTransitionGeneration(reservation,
		assessment.Binding.LaunchRecordDigest, assessment.Binding.LaunchRecordModTime); err != nil {
		return supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "launch generation could not be bound after reservation: " + err.Error(),
			Recovery: "the reservation is left unconsumed; re-run the sweep after the launch generation settles",
		}
	}

	// STEP 5, the cross-kind rescan. Step 2 plus O_EXCL arbitrate only actors racing to the
	// SAME path. Two actors of different kinds -- or old-vs-new derivation -- race to DIFFERENT
	// paths: both pass step 2, both win their own O_EXCL, both deliver. Kind is in the filename,
	// so the filesystem CANNOT arbitrate across kinds; this read is the cross-kind mutex.
	//
	// Both racers refusing is the CORRECT outcome. The contract prefers
	// indeterminate-with-recovery over double delivery, and the window is small.
	after, err := scanRecoveryTransitionsForPause(dir, key, legacyKey)
	if err != nil {
		return supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "cannot re-scan the ledger after reserving: " + err.Error(),
			Recovery: "the reservation is left unconsumed and must be inspected before any manual resume",
		}
	}
	// Excluded by EXACT PATH EQUALITY and nothing looser: our own reservation is the one thing in
	// the ledger that must not block us, and any weaker match (same key, same kind, prefix) could
	// exclude a competitor too.
	if competitor := after.competingWith(reservation.Path); competitor != nil {
		return supervisionResumeRefusal{
			Clause:   "no earlier resume claim ... delivered or remains indeterminate",
			Detail:   "another recovery transition for this pause appeared concurrently: " + competitor.describe(),
			Recovery: competitor.recovery(),
		}
	}

	// STEP 6. ONE audited delivery. Exactly one call, on the confirmation-safe path.
	if err := deliver(assessment.Actions.Resume.Command); err != nil {
		// The reservation stays UNCONSUMED on purpose: delivery may or may not have landed, so
		// the pause is indeterminate and step 2 will refuse the next attempt rather than
		// risking a second audited resume.
		return supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "delivery failed after the claim was recorded; this pause is now INDETERMINATE: " + err.Error(),
			Recovery: "inspect the pane, then clear or consume the reservation deliberately; automatic resume will not retry",
		}
	}

	// STEP 7. CONSUME. Only now is the claim complete.
	return consumeRecoveryTransition(reservation.Record.TransitionID, reservation.Record.NewAttemptID, reservation.Path)
}

// summarizeEligibilityReasons renders the assessment's own deterministic reasons.
//
// It does NOT re-derive them: #498 rules Eligible authoritative and PR4 owns the conjunction.
// This exists so a refusal can quote WHY without the executor forming a second opinion about
// eligibility -- the distinction that #573's preview/admission split was about.
func summarizeEligibilityReasons(reasons []GoalSupervisionEligibilityReason) string {
	if len(reasons) == 0 {
		return "assessment reported no deterministic reason"
	}
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, r.Code)
	}
	return strings.Join(parts, "; ")
}
