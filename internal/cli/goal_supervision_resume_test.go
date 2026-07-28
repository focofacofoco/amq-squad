package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// dev-2's three PR5 falsifiers, plus the mismatched-NamespaceID row.
//
// Each is written as a FALSIFYING INPUT rather than a confirming one: an input under which the
// property is false if the code has the defect, not an input under which the code happens to be
// right. That distinction is the one this milestone keeps re-learning, most recently as an r9
// rejection test that asserted a struct field and survived deletion of the guard it named.

// FALSIFIER 1: PauseGeneration is CONSUMED, never recomputed.
//
// PR4 derives the pause generation from captured LaunchID + Goal.BindingDigest + Goal.AttemptID +
// Goal.Mode. If PR5 recomputes it -- from a current snapshot, or from a drifted formula -- the two
// owners can disagree, and a claim written under a generation nobody else computes is unmatchable
// on read, therefore treated as absent, therefore a second delivery.
//
// THE FALSIFYING INPUT: a pause generation that NO derivation could have produced. Every real
// derivation yields sha256 hex; this one is a sentence. If the constructor recomputes, the record
// cannot come back carrying it. A hex-shaped fixture would be indistinguishable from a recomputed
// value and would prove nothing -- which is why the value is deliberately not hex.
func TestSupervisionResumeConsumesThePauseGenerationItIsGiven(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	// Not hex, not 64 chars, not derivable by any formula in this package.
	const authoritative = "pause-generation-that-no-local-formula-could-produce"

	project := t.TempDir()
	namespaceID := squadnamespace.ID(profile, session)

	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                resumeGoalTransitionRecord{Project: project, Profile: profile, Session: session},
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         namespaceID,
		AttemptID:           attemptID,
		PauseGeneration:     authoritative,
		PreclaimFingerprint: "fingerprint-1",
	})
	if err != nil {
		t.Fatalf("a complete supervision input must be accepted: %v", err)
	}

	if record.PauseGeneration != authoritative {
		t.Errorf("PauseGeneration = %q, want the AUTHORITATIVE %q.\nPR4 owns this derivation. A "+
			"value that differs was recomputed here, and two owners of one identity can disagree.",
			record.PauseGeneration, authoritative)
	}

	// The identity must be keyed on the authoritative value too, not merely stored alongside a
	// key derived from something else. Storing the right value while keying on a recomputed one
	// is the SAME defect with the evidence field papering over it.
	wantKey, keyErr := supervisionClaimKey(namespaceID, authoritative, attemptID)
	if keyErr != nil {
		t.Fatalf("canonical claim key: %v", keyErr)
	}
	if record.TransitionID != wantKey {
		t.Errorf("TransitionID = %q, want %q -- the claim must be KEYED on the authoritative pause "+
			"generation, not merely carry it as a field", record.TransitionID, wantKey)
	}
	if got := filepath.Base(path); got != currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, wantKey) {
		t.Errorf("filename = %q; a claim written under a key nobody else computes is invisible to "+
			"the scanner that would find it", got)
	}
}

// FALSIFIER 2: Eligible=true with AutomaticResumeAllowed=false must REFUSE.
//
// These answer different questions and conflating them is how an operator who chose notify-only
// gets an automatic /goal resume. Eligible asks "is this actor resumable"; AutomaticResumeAllowed
// asks "is automatic action permitted here".
//
// THE FALSIFYING INPUT is precisely the combination that a single-flag check would let through:
// everything eligibility-related is GREEN, and only the policy flag is false. An assessment that
// was also ineligible would refuse for the other reason and prove nothing about this gate.
func TestEligibleButPolicyForbiddenRefusesWithoutReservingOrDelivering(t *testing.T) {
	project := t.TempDir()
	assessment := GoalSupervisionAssessment{
		Fresh:                  true,
		Eligible:               true, // GREEN
		AutomaticResumeAllowed: false,
		Fingerprint:            "fingerprint-1",
		Binding: GoalSupervisionBinding{
			Project: project, Profile: "squad", Session: "v2-25-0",
			NamespaceID:     squadnamespace.ID("squad", "v2-25-0"),
			PauseGeneration: "pause-gen-1",
			Goal:            GoalSupervisionGoalIdentity{AttemptID: "attempt-abc", BindingDigest: "digest-def"},
		},
	}

	delivered := 0
	err := executeSupervisionResume(assessment, func(string) error { delivered++; return nil })

	if err == nil {
		t.Fatal("a profile that forbids automatic resume must refuse even when the actor is ELIGIBLE: " +
			"eligibility is about the actor, policy is about whether we may act")
	}
	if delivered != 0 {
		t.Errorf("refused but STILL delivered %d time(s): the operator chose manual or notify-only", delivered)
	}
	// NO SIDE EFFECT. A refusal that left a reservation behind would wedge the pause for the
	// manual resume the operator actually wanted -- the refusal would create the mess it exists
	// to avoid. Checked by observing the directory rather than by trusting the return.
	dir := goalAttemptDir(project, "squad", "v2-25-0")
	entries, readErr := os.ReadDir(dir)
	if readErr == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), currentRecoveryTransitionPrefix) ||
				strings.HasPrefix(e.Name(), legacyRecoveryTransitionPrefix) {
				t.Errorf("a policy refusal wrote a reservation (%s); it must leave no claim behind", e.Name())
			}
		}
	}
	// The refusal must name the policy clause, or the operator cannot tell it from ineligibility
	// and will go looking for evidence problems that do not exist.
	if !strings.Contains(err.Error(), "automatic resume") {
		t.Errorf("refusal must name the POLICY reason, not a generic ineligibility: %v", err)
	}
}

// FALSIFIER 3: filename-kind and record-kind must AGREE, or the reservation BLOCKS.
//
// A renamed file would otherwise silently change what a reservation means. Disagreement is
// ambiguous evidence, and ambiguous evidence refuses -- it is not a tie to be broken by
// preferring one source.
//
// THE FALSIFYING INPUT is the pairing a name-trusting scan cannot see: a current-derivation
// filename that says native-goal-resume over a body that says redeliver. A scan that reads only
// the name reports a clean native claim; a scan that reads only the body reports a clean
// redelivery claim; only comparing them sees that neither is trustworthy.
func TestReservationWhoseFilenameAndBodyDisagreeAboutKindBlocks(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("a", 64)

	// Filename says native-goal-resume.
	name := currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, key)
	// Body says redeliver.
	body := `{"recovery_kind":"redeliver","transition_id":"` + key + `"}`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	blocker := reservationBlocker(path)
	if blocker == nil {
		t.Fatal("a reservation whose filename and body disagree about its KIND must BLOCK: a rename " +
			"would otherwise silently change what a claim means, and neither reading can be trusted")
	}
	if !strings.Contains(blocker.Reason, "kind disagrees") {
		t.Errorf("the blocker must name the disagreement so an operator can resolve it; got %q", blocker.Reason)
	}
	// Every refusal carries a runnable next step. A dead-end refusal is what #498 was filed about.
	if strings.TrimSpace(blocker.recovery()) == "" {
		t.Error("a blocker with no recovery command is the stall this milestone exists to remove")
	}
}

// FALSIFIER 4 (mine, not dev-2's): a NamespaceID that is not the canonical id for the
// profile/session must be REFUSED at construction.
//
// This is the r6 lesson at the container level: I closed the NAME and left the DIRECTORY. A
// correct hash written under a foreign namespace is present on disk and invisible to every
// scanner that would look for it -- the claim exists and blocks nothing.
//
// THE FALSIFYING INPUT is a WELL-FORMED namespace id belonging to a DIFFERENT profile. A garbage
// string would be caught by any shape check and would prove only that blanks are rejected; this
// one is exactly as valid-looking as the right answer, and differs only in being someone else's.
func TestSupervisionResumeRefusesAForeignNamespaceID(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	foreign := squadnamespace.ID("other-profile", session)
	canonical := squadnamespace.ID(profile, session)
	if foreign == canonical {
		t.Fatal("fixture is void: the foreign namespace id equals the canonical one, so this test " +
			"cannot distinguish acceptance from refusal")
	}

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                resumeGoalTransitionRecord{Project: project, Profile: profile, Session: session},
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         foreign,
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
	})
	if err == nil {
		t.Fatal("a namespace id that is not canonical for this profile/session must be refused: the " +
			"claim would be written where no scanner looks, so it would exist and block nothing")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("the refusal must name the namespace mismatch: %v", err)
	}
}
