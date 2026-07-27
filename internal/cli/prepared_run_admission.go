package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// #573: `team resume` previewed prepared and staged actors as will-launch and emitted a
// command that `agent up` then REFUSED, because admission requires a single-use spawn
// reservation the preview knew nothing about. Preview and execution disagreed, and the
// operator was handed a command the binary rejects.
//
// preparedRunActorAdmission is the SINGLE answer to "would admission refuse this actor for
// lack of a reservation, and what binds one?". Admission (launch.go) and the resume planner
// (team_resume.go) both call it, so they cannot disagree about the verdict OR about the
// wording. Duplicating the condition is what produced #573 in the first place: the
// `!prepared || !containsRole(manifest.StagedRoster, role)` clause was inlined at five
// sites, and the planner simply was not one of them.
//
// RECORD-AWARE ON PURPOSE. "Governed by an accepted prepared generation" does NOT predict
// admission. execRestoreRecord (restore.go) takes the token FROM THE PERSISTED RECORD and
// falls back to an EMPTY token when the record carries none -- which admission refuses. So a
// manifest-only predicate would report an actor bindable and then fail at exec, recreating
// the very preview/execution disagreement this type exists to kill, one layer down.
type preparedRunAdmission struct {
	// Governed is true when an accepted prepared generation covers this session.
	Governed bool
	// Staged is true when the actor is in the staged roster rather than the initial one.
	// It selects BOTH the refusal wording and the recovery command, which is why admission
	// has always had two distinct messages here.
	Staged bool
	// Bindable is true when a persisted launch record already carries a COMPLETE token, so
	// the managed restore path can bind without operator input.
	Bindable bool
	// Generation is the accepted prepared generation, named in the refusal so the operator
	// can tell which reservation is being asked for.
	Generation string
	// Reason is the refusal text, owned HERE rather than at each call site. Admission and
	// the planner render the same sentence because there is only one sentence.
	Reason string
	// Recovery is the exact operator-runnable command that binds a reservation. Empty when
	// no reservation is required.
	Recovery string
}

// required reports whether an actor with no in-process token would be refused.
func (a preparedRunAdmission) required() bool { return a.Governed }

// preparedRunActorAdmission classifies an actor against an ALREADY-LOADED manifest.
//
// The manifest is passed in rather than read here so admission, which has it in hand,
// does not re-read it. A second read would open a window in which the preview and the
// admission decision are made against different generations -- a different flavour of the
// same disagreement.
//
// rec may be nil, meaning "no persisted record was found": that is not bindable.
func preparedRunActorAdmission(manifest preparedRunManifest, prepared bool, role, handle string, rec *launch.Record) preparedRunAdmission {
	adm := preparedRunAdmission{Generation: strings.TrimSpace(manifest.Generation)}
	if !prepared {
		return adm
	}
	adm.Governed = true
	adm.Staged = containsRole(manifest.StagedRoster, role)
	if rec != nil {
		adm.Bindable = preparedRunTokenFromRecord(*rec).complete()
	}

	if adm.Staged {
		adm.Reason = fmt.Sprintf("staged actor %s/%s requires an exact single-use spawn reservation for prepared generation %s",
			role, handle, adm.Generation)
	} else {
		adm.Reason = fmt.Sprintf("prepared actor %s/%s requires the exact reserved generation token", role, handle)
	}

	switch {
	case adm.Bindable:
		// The record carries the token, so the managed restore path binds it with no
		// operator-supplied secret. This is the only case `resume --exec` can bind
		// automatically.
		adm.Recovery = "amq-squad restore --role " + shellQuote(role)
	case adm.Staged:
		// There is no hand-typable prepared-token flag; the staged pair is the ONLY
		// operator-typable binding. The claim ID is deliberately left as a placeholder
		// because it must come from the authoritative current claim, not from a guess.
		adm.Recovery = "amq-squad agent up --role " + shellQuote(role) +
			" --staged-spawn --staged-claim <exact active claim ID from `amq-squad status --json`>"
	default:
		// Prepared but unbindable and not staged: no operator-typable command exists, so
		// naming one would be worse than admitting that. Point at the command that
		// reserves instead of inventing a flag.
		adm.Recovery = "amq-squad run start --profile <profile> --session " + shellQuote(strings.TrimSpace(manifest.Session)) +
			" (re-reserves the generation; no operator-typable token flag exists for this case)"
	}
	return adm
}

// preparedRunAdmissionForMember loads the manifest and classifies one member. Used by the
// resume planner, which -- unlike admission -- holds no manifest.
//
// preparedRunManifestForProjection is reused deliberately: it already distinguishes a
// genuinely non-prepared session from a DAMAGED accepted state. A fresh reader written here
// would almost certainly collapse that distinction into "not prepared", which is fail-OPEN
// in the planner and would preview a command admission rejects -- #573 again.
func preparedRunAdmissionForMember(project, profile, session, role, handle string, rec *launch.Record) (preparedRunAdmission, error) {
	manifest, _, prepared, err := preparedRunManifestForProjection(project, profile, session)
	if err != nil {
		return preparedRunAdmission{}, err
	}
	return preparedRunActorAdmission(manifest, prepared, role, handle, rec), nil
}
