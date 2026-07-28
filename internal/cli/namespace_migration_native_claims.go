package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// #498 U7: FAIL-CLOSED GUARD against migrating a namespace that holds NATIVE recovery-transition claims.
//
// WHY THIS EXISTS. The migration adapter rewrites a transition record's Profile and Session and re-marshals it
// (namespace_migration_adapters.go, the .resume-redelivery- branch). For a NATIVE claim that is silent
// corruption: the claim key is derived from NamespaceID(profile, session) + pause generation + attempt, so
// rewriting profile/session WITHOUT recomputing the identity leaves a record whose id no longer matches its own
// namespace. Unmatchable on read means treated as ABSENT, and absent means "no prior claim" -- which is a SECOND
// DELIVERY of an audited resume. That is precisely the failure #498 exists to prevent, reached through a code
// path nobody had enumerated until the re-decider sweep.
//
// The adapter also only recognises the LEGACY ".resume-redelivery-" prefix, so a current-prefix native file
// falls through to the goal-attempt branch and fails the migration in a confusing way rather than a clear one.
//
// SCOPE, deliberately minimal and ruled: this guard REFUSES. It does not recompute claim keys and it does not
// teach the adapter the native prefix. Full native-migration support is a separate v2.26.0 issue ranked above
// #583 because it is a correctness bug rather than breadth. Silent corruption becomes an explicit refusal, and
// that is the whole intended delta.
//
// IT IS A NAMESPACE PRE-CHECK, not a per-file refusal, and that placement is load-bearing: a per-file refusal
// discovered mid-rewrite would abort a multi-file migration halfway, which is its own corruption mode. The
// question this answers is "is this namespace safe to migrate AT ALL", asked before anything is rewritten.
func inspectNamespaceNativeRecoveryClaims(goalAttemptsSource string, blockers *[]string) {
	if goalAttemptsSource == "" {
		return
	}
	entries, err := os.ReadDir(goalAttemptsSource)
	if err != nil {
		if os.IsNotExist(err) {
			// No goal-attempts tree at all means no claims to corrupt. Absence here is genuinely absence,
			// unlike the malformed case below, because the directory's non-existence is not ambiguous.
			return
		}
		// UNREADABLE IS NOT EMPTY. If the directory cannot be listed we do not know whether native claims are
		// present, and proceeding would gamble the double-delivery outcome on an unverified assumption.
		*blockers = append(*blockers, fmt.Sprintf(
			"cannot inspect %s for native recovery-transition claims, so migration safety cannot be established: %v",
			goalAttemptsSource, err))
		return
	}
	for _, entry := range entries {
		// SYMLINKS ARE SKIPPED EXPLICITLY, before the directory branch.
		//
		// os.ReadDir reports entry types from the directory record without following links, so a symlink to a
		// directory already has IsDir()==false and could not drive recursion. This guard therefore changes no
		// behaviour -- it makes the cycle-safety LOCAL instead of inherited from a stdlib detail every future
		// reader has to re-derive. A traversal whose termination depends on an unstated property of its
		// iteration primitive is correct by coincidence.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		// RECURSE TO ANY DEPTH. The comment here previously said "recurse one level" while the code recursed on
		// every directory entry -- the behaviour was intended and the comment was wrong, which is
		// comment-outlives-code at birth. Unbounded is CORRECT rather than merely tolerated: claims live beside
		// attempt records under per-session directories, and a depth guess would silently miss a native claim
		// one level below wherever the guess landed, which is exactly the failure this guard exists to prevent.
		if entry.IsDir() {
			inspectNamespaceNativeRecoveryClaims(filepath.Join(goalAttemptsSource, entry.Name()), blockers)
			continue
		}
		// ONE RECOGNITION OWNER. recognizeRecoveryTransitionName is the same parser the pause scan uses, so
		// this guard cannot drift from the scanner's idea of what a claim file is. A string-prefix test here
		// would be a second opinion about naming, which is the two-deciders shape.
		parsed, recognition := recognizeRecoveryTransitionName(entry.Name())
		switch recognition {
		case recoveryNameRecognized:
			// LEGACY-shaped records are what the adapter already handles: they carry no kind in the name and
			// their identity does not depend on the namespace, so rewriting profile/session is safe for them.
			// Only current-derivation claims are refused.
			if parsed.Legacy {
				continue
			}
			*blockers = append(*blockers, fmt.Sprintf(
				"native recovery-transition claim %s cannot be migrated safely: its claim key is derived from the namespace, so rewriting profile/session would make the claim unmatchable on read (treated as ABSENT, permitting a second delivery). Resolve or consume the claim before migrating; native-aware migration is tracked separately",
				filepath.Join(goalAttemptsSource, entry.Name())))
		case recoveryNameMalformed:
			// UNKNOWN IS NOT ABSENT -- the same rule the pause scan enforces, and the reason this case is
			// listed explicitly rather than folded into the default. A transition-like name we cannot classify
			// might be a native claim; treating it as "no native claims present" would migrate anyway and
			// corrupt exactly the record we failed to read.
			*blockers = append(*blockers, fmt.Sprintf(
				"recovery-transition-like file %s cannot be classified, so it cannot be shown safe to migrate: unknown is not absent, and a native claim here would be corrupted by a namespace rewrite",
				filepath.Join(goalAttemptsSource, entry.Name())))
		}
		// recoveryNameNotATransition: an ordinary attempt/claim file. Ignored, because blocking on those would
		// make every namespace unmigratable, which is amendment 1 overcorrected into an outage.
	}
}
