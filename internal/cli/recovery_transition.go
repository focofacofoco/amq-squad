package cli

// PR5 / #498 ledger extension. Additive to resume_goal.go's existing recovery-transition
// machinery: ONE store, ONE CAS owner, ONE parser. goal_attempt.go stays read-only and there is
// no second claim store and no adapter, per the ruling that a parallel store is the
// two-deciders defect at the exact spot whose failure mode is double delivery of an audited
// /goal resume.
//
// READ THIS FIRST IF THE PREFIXES LOOK REDUNDANT. There are two derivations on purpose:
//   - LEGACY: ".resume-redelivery-<sha256(attemptID \x00 bindingDigest)>.json", the identity
//     resume_goal.go has always written. It stays READABLE for recovery and loses its writer
//     path; a hit at that derivation is an AUTHORITATIVE existing claim.
//   - CURRENT: ".recovery-<kind>-<sha256(namespaceID \x00 pauseGeneration \x00 attemptID)>.json",
//     the ruled stable key. PauseGeneration is CONSUMED from the assessment, never recomputed
//     here -- PR4 owns that derivation and a second owner reading a different snapshot is the
//     failure this milestone kept producing.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// recoveryTransitionKind distinguishes what a reservation is FOR. It lives in BOTH the filename
// prefix and the record body, and the parser requires them to agree.
//
// Why both: the prefix makes the kind-agnostic pause scan cheap and the directory human-
// readable, while the body makes it durable evidence rather than a naming convention. Requiring
// AGREEMENT is what stops a renamed file from silently changing what a reservation means --
// disagreement is ambiguous evidence, and ambiguous evidence refuses.
type recoveryTransitionKind string

const (
	recoveryTransitionKindRedeliver        recoveryTransitionKind = "redeliver"
	recoveryTransitionKindNativeGoalResume recoveryTransitionKind = "native-goal-resume"
)

// legacyRecoveryTransitionPrefix is the pre-PR5 filename prefix. Readable, never written.
const legacyRecoveryTransitionPrefix = ".resume-redelivery-"

// currentRecoveryTransitionPrefix is the kind-carrying prefix for the ruled stable key.
const currentRecoveryTransitionPrefix = ".recovery-"

// supervisionClaimKey builds the ruled durable identity: NamespaceID + PauseGeneration +
// AttemptID.
//
// Every component is CONSUMED, none derived here. PauseGeneration in particular arrives from
// GoalSupervisionAssessment.Binding.PauseGeneration, which PR4 computes from captured LaunchID +
// Goal.BindingDigest + Goal.AttemptID + Goal.Mode. Recomputing it in this file would make PR5 a
// second derivation owner, and the defect that produces is not a field-sensitivity issue -- it
// is one identity with two owners that can disagree.
//
// PreclaimFingerprint is deliberately NOT part of this key. It rotates when claim evidence
// changes, and writing the claim IS a change to claim evidence, so a fingerprint-keyed claim
// would rotate its own identity at the moment of being recorded and become unmatchable. It is
// stored as staleness evidence instead.
func supervisionClaimKey(namespaceID, pauseGeneration, attemptID string) (string, error) {
	namespaceID = strings.TrimSpace(namespaceID)
	pauseGeneration = strings.TrimSpace(pauseGeneration)
	attemptID = strings.TrimSpace(attemptID)
	// Each component is required. A blank one would silently collapse distinct pauses onto one
	// key, which is the double-delivery failure wearing a hash.
	if namespaceID == "" || pauseGeneration == "" || attemptID == "" {
		return "", fmt.Errorf("recovery claim key requires namespace, pause generation and attempt id (got %q/%q/%q)",
			namespaceID, pauseGeneration, attemptID)
	}
	sum := sha256.Sum256([]byte(namespaceID + "\x00" + pauseGeneration + "\x00" + attemptID))
	return hex.EncodeToString(sum[:]), nil
}

// currentRecoveryTransitionName renders the kind-carrying filename for a claim key.
func currentRecoveryTransitionName(kind recoveryTransitionKind, claimKey string) string {
	return currentRecoveryTransitionPrefix + string(kind) + "-" + claimKey + ".json"
}

// parsedRecoveryTransitionName is what the ONE parser returns.
type parsedRecoveryTransitionName struct {
	Kind     recoveryTransitionKind
	ClaimKey string
	Legacy   bool
}

// parseRecoveryTransitionName is the SINGLE parser for both derivations.
//
// It rejects rather than guesses. Unknown, missing, malformed or mismatched kind refuses,
// because a reservation whose purpose cannot be determined is exactly the ambiguous evidence
// that must not be treated as absent. The caller then compares Kind against the record body's
// kind; this function cannot do that comparison itself because it only sees the name.
func parseRecoveryTransitionName(name string) (parsedRecoveryTransitionName, bool) {
	switch {
	case strings.HasSuffix(name, ".consumed.json"), strings.HasSuffix(name, ".bound.json"):
		// Companion artifacts, not reservations. Named explicitly so a future companion
		// suffix cannot be silently misread as a reservation.
		return parsedRecoveryTransitionName{}, false
	case !strings.HasSuffix(name, ".json"):
		return parsedRecoveryTransitionName{}, false
	}

	if rest, ok := trimPrefixExact(name, legacyRecoveryTransitionPrefix); ok {
		key := strings.TrimSuffix(rest, ".json")
		if !isRecoveryClaimKey(key) {
			return parsedRecoveryTransitionName{}, false
		}
		// Legacy names carry NO kind. They are redelivery reservations by construction, which
		// is recorded here rather than inferred at each call site.
		return parsedRecoveryTransitionName{Kind: recoveryTransitionKindRedeliver, ClaimKey: key, Legacy: true}, true
	}

	rest, ok := trimPrefixExact(name, currentRecoveryTransitionPrefix)
	if !ok {
		return parsedRecoveryTransitionName{}, false
	}
	body := strings.TrimSuffix(rest, ".json")
	// Split from the RIGHT: the claim key is a fixed-length hex tail, and kinds contain
	// hyphens ("native-goal-resume"), so splitting from the left would mangle them.
	cut := strings.LastIndex(body, "-")
	if cut <= 0 {
		return parsedRecoveryTransitionName{}, false
	}
	kind := recoveryTransitionKind(body[:cut])
	key := body[cut+1:]
	if !isRecoveryClaimKey(key) || !knownRecoveryTransitionKind(kind) {
		return parsedRecoveryTransitionName{}, false
	}
	return parsedRecoveryTransitionName{Kind: kind, ClaimKey: key}, true
}

// knownRecoveryTransitionKind refuses unknown kinds rather than passing them through.
//
// An unrecognised kind is not a new kind to be tolerated: it means this binary does not
// understand a reservation that exists, and proceeding would ignore a claim it cannot read.
func knownRecoveryTransitionKind(kind recoveryTransitionKind) bool {
	switch kind {
	case recoveryTransitionKindRedeliver, recoveryTransitionKindNativeGoalResume:
		return true
	default:
		return false
	}
}

// isRecoveryClaimKey validates the sha256 hex shape both derivations use.
func isRecoveryClaimKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

// trimPrefixExact is strings.TrimPrefix plus a report of whether it applied, so a caller cannot
// mistake "no prefix" for "empty remainder".
func trimPrefixExact(s, prefix string) (string, bool) {
	if !strings.HasPrefix(s, prefix) {
		return "", false
	}
	return strings.TrimPrefix(s, prefix), true
}

// recoveryNameRecognition is the TRI-STATE the scan needs, per dev-2's binding correction.
//
// A boolean cannot express this directory's reality. goalAttemptDir holds ordinary
// <attempt>.json and <attempt>.claim.json files alongside recovery transitions, so:
//   - NotATransition: definitely not ours. IGNORED. Blocking here would let normal attempt files
//     wedge all recovery permanently, which is amendment 1 overcorrected into an outage.
//   - Recognized: parses completely. INSPECTED.
//   - Malformed: TRANSITION-LIKE -- it carries a recovery prefix -- but its structure, kind or key
//     cannot be read. BLOCKS, and deliberately WITHOUT requiring a key match: if corruption
//     destroyed the key, demanding the key be present proves nothing about ownership while
//     quietly permitting delivery. That was my fail-open, fast-rejected.
type recoveryNameRecognition int

const (
	recoveryNameNotATransition recoveryNameRecognition = iota
	recoveryNameRecognized
	recoveryNameMalformed
)

// recognizeRecoveryTransitionName is the one recognition point for both derivations.
//
// It distinguishes "not a transition" from "a transition I cannot read" by PREFIX first: only a
// name carrying a recovery prefix can be malformed-transition-like. Everything else is somebody
// else's file and is ignored.
func recognizeRecoveryTransitionName(name string) (parsedRecoveryTransitionName, recoveryNameRecognition) {
	switch {
	case strings.HasSuffix(name, ".consumed.json"), strings.HasSuffix(name, ".bound.json"):
		// Companions are handled by their own path; not reservations.
		return parsedRecoveryTransitionName{}, recoveryNameNotATransition
	case !strings.HasPrefix(name, legacyRecoveryTransitionPrefix) && !strings.HasPrefix(name, currentRecoveryTransitionPrefix):
		// No recovery prefix: an ordinary attempt/claim file or an unrelated one.
		return parsedRecoveryTransitionName{}, recoveryNameNotATransition
	case !strings.HasSuffix(name, ".json"):
		// Prefixed but not a JSON artifact: transition-like and unreadable.
		return parsedRecoveryTransitionName{}, recoveryNameMalformed
	}
	parsed, ok := parseRecoveryTransitionName(name)
	if !ok {
		return parsedRecoveryTransitionName{}, recoveryNameMalformed
	}
	return parsed, recoveryNameRecognized
}

// newRecoveryTransitionRecord is the SINGLE constructor AND the single path deriver for every
// recovery-transition reservation. It returns the record and the filename that must hold it, so
// no caller can pair a validated record with a PATH of its own choosing.
//
// It returns the CANONICAL FULL PATH, not a basename (dev-2's correction). A basename leaves the
// DIRECTORY to the caller, so a correct hash for namespace A could be written into namespace B's
// directory and each scanner would miss it -- the claim would exist and be invisible to both.
// That is the same gap as r6's session layer: I closed the name and left the container. There is
// deliberately no filename-only API, so there is nothing to redirect.
//
// dev-2's original correction was about the id:
// otherwise the redelivery site could keep passing its own id and write a current-looking record
// under the legacy key.
//
// PER-KIND VALIDATION, and the reason is POSSESSION rather than preference. A future kind should
// copy the DISPATCH, not one of its branches:
//
//	redeliver           has no assessment in hand, so it has NO PauseGeneration. Requiring one
//	                    would force either local recomputation (forbidden -- PR4 owns that
//	                    derivation) or a faked value, which is worse than the literal this
//	                    constructor replaces. It requires what that path genuinely holds:
//	                    AttemptID and BindingDigest.
//	native-goal-resume  consumes a full assessment, so it requires the whole ruled set:
//	                    NamespaceID, assessment-captured PauseGeneration, AttemptID and
//	                    PreclaimFingerprint. None recomputed.
//
// BOTH DIRECTIONS ARE ENFORCED. A redeliver write carrying a PauseGeneration is REFUSED too: it
// cannot honestly have one, so its presence means someone recomputed or invented it, which is the
// failure the dispatch exists to prevent. A contract that only checks for missing fields lets the
// other half through.
//
// READ vs WRITE asymmetry (ruled): on READ, absent PR5 fields mean legacy/redeliver, identified
// by the legacy key and blocking per the consumed-blocks ruling. On WRITE, absence of a required
// field refuses -- a defaulted identity field is indistinguishable on disk from a legacy record,
// so a lenient writer silently enrols new records into the legacy population.
func newRecoveryTransitionRecord(in recoveryTransitionInput) (resumeGoalTransitionRecord, string, error) {
	kind := recoveryTransitionKind(strings.TrimSpace(string(in.Kind)))
	if !knownRecoveryTransitionKind(kind) {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf("recovery transition requires a known kind, got %q", in.Kind)
	}
	attemptID := strings.TrimSpace(in.AttemptID)
	if attemptID == "" {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf("recovery transition (%s) requires an attempt id", kind)
	}
	pause := strings.TrimSpace(in.PauseGeneration)
	fingerprint := strings.TrimSpace(in.PreclaimFingerprint)

	record := in.Base
	record.RecoveryKind = string(kind)

	switch kind {
	case recoveryTransitionKindRedeliver:
		// This path holds no assessment. Presence of a pause generation here is evidence that
		// someone derived one locally, so it refuses rather than storing it.
		if pause != "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transitions must NOT carry a pause generation: this path holds no assessment, so a value here was recomputed or invented (got %q)", pause)
		}
		if strings.TrimSpace(in.BindingDigest) == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transition requires the binding digest its identity is derived from")
		}
		// The canonical redeliver identity is unchanged, so redelivery's own readers are
		// untouched -- PR5 does not refactor that read side. It is now emitted HERE, which is
		// what makes "no write bypasses the one constructor" true in meaning.
		record.TransitionID = resumeGoalTransitionID(attemptID, in.BindingDigest)
		record.PreclaimFingerprint = fingerprint // optional evidence on this path
		// Derived through resumeGoalTransitionPath so redelivery's EXISTING readers are
		// untouched: the path they already compute is the path this constructor emits.
		path, err := resumeGoalTransitionPath(in.Base.Project, in.Base.Profile, in.Base.Session, record.TransitionID)
		if err != nil {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("derive redeliver transition path: %w", err)
		}
		return record, path, nil

	case recoveryTransitionKindNativeGoalResume:
		// Scope consistency: the record's own triple must match what the caller claims, or the
		// path and the record would describe different namespaces.
		if strings.TrimSpace(in.Base.Project) == "" || strings.TrimSpace(in.Base.Profile) == "" || strings.TrimSpace(in.Base.Session) == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires an exact project/profile/session triple")
		}
		// NamespaceID must equal the canonical id for this profile/session, or a correct hash
		// could be written under a namespace it does not belong to -- present on disk and
		// invisible to the scanner that would look for it.
		//
		// squadnamespace.ID is at internal/namespace/namespace.go:46 (verified by reading it, not
		// by trusting the citation). My earlier search failed because I assumed the package
		// DIRECTORY matched the import alias: the alias is squadnamespace, the path is
		// internal/namespace.
		if want := squadnamespace.ID(in.Base.Profile, in.Base.Session); strings.TrimSpace(in.NamespaceID) != want {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume namespace id %q does not match the canonical id %q for profile/session: a claim written under a foreign namespace is invisible to the scanner that would find it", in.NamespaceID, want)
		}
		if pause == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the assessment-captured pause generation; extend the assessment-to-plan binding rather than omitting it")
		}
		if fingerprint == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the preclaim fingerprint as staleness evidence")
		}
		key, err := supervisionClaimKey(in.NamespaceID, pause, attemptID)
		if err != nil {
			return resumeGoalTransitionRecord{}, "", err
		}
		// DERIVED here, never accepted from the caller. dev-2's correction: a caller-supplied id
		// would let a legacy or one-byte-wrong value through, writing a current-looking record
		// under the wrong key -- unmatched on read, therefore absent, therefore a second
		// delivery.
		record.TransitionID = key
		record.PauseGeneration = pause
		record.PreclaimFingerprint = fingerprint
		// The DIRECTORY is derived from the same validated triple, inside the constructor. A
		// caller joining a basename could place a namespace-A claim in namespace-B's directory,
		// where neither scanner would find it.
		return record, filepath.Join(goalAttemptDir(in.Base.Project, in.Base.Profile, in.Base.Session), currentRecoveryTransitionName(kind, key)), nil
	}
	return resumeGoalTransitionRecord{}, "", fmt.Errorf("unhandled recovery transition kind %q", kind)
}

// recoveryTransitionInput is what a caller supplies. It carries NO TransitionID field on purpose:
// the identity is DERIVED per kind, so there is nothing for a caller to get wrong or to smuggle.
//
// Base carries what the existing redelivery writer already computes, which is what makes routing
// that writer through here additive rather than a rewrite of what it records.
type recoveryTransitionInput struct {
	Base                resumeGoalTransitionRecord
	Kind                recoveryTransitionKind
	NamespaceID         string
	AttemptID           string
	BindingDigest       string
	PauseGeneration     string
	PreclaimFingerprint string
}
