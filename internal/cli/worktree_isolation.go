package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// worktreeIsolationReadinessRow is #497's planning-level readiness check:
// launch fails closed when 2+ mutation-capable members would share one
// resolved working directory (one Git index/checkout) without an explicit
// recorded exception (team.Team.SharedCwdException). This is a static check
// over the accepted team profile; the live worktree drift/staleness doctor
// rows are a separate runtime concern owned elsewhere.
//
// AGREEMENT WITH doctor's shared-index-collision (#538 acceptance criterion 4,
// and the reason the two texts are NOT identical):
//
// Both checks detect the SAME condition -- 2+ mutation-capable members resolving
// to one Git index -- and honour the SAME exception (SharedCwdException clears
// it), using the same vocabulary. What differs is deliberate and lifecycle-bound:
//
//   - SEVERITY. This check runs at PREPARATION, before any agent exists, and
//     fails closed per #497. doctor's is liveness-derived: it fails only when 2+
//     affected members are actually LIVE and warns otherwise. Copying that rule
//     here would make preparation always warn, since nothing is live yet, which
//     would defeat the fail-closed guarantee.
//   - REMEDY. doctor names `worktree plan|materialize`, which require --task ID.
//     At preparation no tasks exist, so that remedy is unrunnable here and naming
//     it would be false. This row names the pre-launch mechanisms instead.
//
// So "agree" means same condition, same exception semantics, same vocabulary,
// with stage-appropriate severity and remedy. If you change one side's condition
// or exception handling, change both.
func worktreeIsolationReadinessRow(t team.Team) runReadinessRow {
	groups := map[string][]string{}
	for _, m := range t.Members {
		if team.EffectiveActorMode(t, m) != team.ActorModeImplementation {
			continue
		}
		cwd := m.EffectiveCWD(t.Project)
		groups[cwd] = append(groups[cwd], m.Role)
	}
	var collisions []string
	for cwd, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		sort.Strings(roles)
		collisions = append(collisions, fmt.Sprintf("%s: %s", cwd, strings.Join(roles, ", ")))
	}
	if len(collisions) == 0 {
		return runReadinessRow{Artifact: "worktree_isolation", Status: "ready", Evidence: "no 2+ mutation-capable members share one working directory"}
	}
	sort.Strings(collisions)
	evidence := strings.Join(collisions, "; ")
	if reason := strings.TrimSpace(t.SharedCwdException); reason != "" {
		return runReadinessRow{Artifact: "worktree_isolation", Status: "ready", Evidence: fmt.Sprintf("shared-cwd collision accepted (%s); exception: %s", evidence, reason)}
	}
	return runReadinessRow{
		Artifact: "worktree_isolation",
		Status:   "blocked",
		Evidence: fmt.Sprintf("2+ mutation-capable members share one working directory without a recorded exception: %s", evidence),
		Fix:      worktreeIsolationFix(sharedCwdCollisionRoles(groups)),
	}
}

// worktreeIsolationFix names remedies that are ACTUALLY EXECUTABLE.
//
// #538: the previous text said "give each mutation-capable member its own --cwd
// (isolated worktree)" without naming any command. --cwd does exist -- on
// `team init` / `new profile`, and now on `team member add|update` -- but the
// operator who hit this had no way to discover that from the message, reasonably
// concluded the flag did not exist, and lost more time here than on anything else
// in the run. A remedy the reader cannot execute is not a remedy.
//
// Every command below is exercised by the reachability regression test, so this
// text cannot drift back into naming something unrunnable.
func worktreeIsolationFix(roles []string) string {
	example := "ROLE"
	if len(roles) > 0 {
		example = roles[0]
	}
	return strings.Join([]string{
		"give each mutation-capable member its own working directory, either at creation with " +
			`'amq-squad new profile NAME --cwd "` + example + `=/path/to/worktree,..."'` +
			" or on an existing roster with " +
			`'amq-squad team member update ` + example + ` --cwd /path/to/worktree'`,
		"or accept the shared checkout deliberately with " +
			`'amq-squad team shared-cwd-exception set "<reason>"'` +
			` (at creation: 'amq-squad new profile NAME --shared-cwd-exception "<reason>"')`,
	}, "; ")
}

// sharedCwdCollisionRoles returns the colliding roles in deterministic order, so
// the fix text can name a real role from the operator's own roster instead of a
// placeholder they have to translate.
func sharedCwdCollisionRoles(groups map[string][]string) []string {
	var out []string
	for _, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		out = append(out, roles...)
	}
	sort.Strings(out)
	return out
}
