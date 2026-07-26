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
//     fails closed per #497. doctor's severity is runtime-derived, and under the
//     #537 contract (t4, landing separately) becomes explicitly liveness-derived:
//     fail only when 2+ affected members are LIVE, warn otherwise. Either way the
//     rule cannot be copied here -- at preparation nothing is live, so a
//     liveness-derived severity would always warn and defeat fail-closed. This
//     sentence describes #537's target contract; if it has not landed in the tree
//     you are reading, doctor still fails unconditionally on the collision.
//   - REMEDY. doctor names `worktree plan|materialize`, which require --task ID.
//     At preparation no tasks exist, so that remedy is unrunnable here and naming
//     it would be false. This row names the pre-launch mechanisms instead.
//
// So "agree" means same condition, same exception semantics, same vocabulary,
// with stage-appropriate severity and remedy. If you change one side's condition
// or exception handling, change both.
func worktreeIsolationReadinessRow(t team.Team, profile string) runReadinessRow {
	groups := map[string][]string{}
	groupDisplay := map[string]string{}
	for _, m := range t.Members {
		if team.EffectiveActorMode(t, m) != team.ActorModeImplementation {
			continue
		}
		// #538 F4: group by CANONICAL filesystem location, not by the raw recorded
		// string. doctor detects this collision by resolved Git index path, so
		// comparing strings here made the two checks disagree on the CONDITION
		// itself: /repo and a symlink pointing at it counted as two directories at
		// preparation and one index at runtime, passing readiness and then failing
		// doctor. Representation is not identity -- the same lesson as #539/#540.
		cwd := m.EffectiveCWD(t.Project)
		key := canonicalFilesystemPath(cwd)
		if key == "" {
			key = cwd
		}
		// Display the path as recorded; group by the canonical key.
		if _, seen := groupDisplay[key]; !seen {
			groupDisplay[key] = cwd
		}
		groups[key] = append(groups[key], m.Role)
	}
	var collisions []string
	for key, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		sort.Strings(roles)
		shown := groupDisplay[key]
		if shown == "" {
			shown = key
		}
		collisions = append(collisions, fmt.Sprintf("%s: %s", shown, strings.Join(roles, ", ")))
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
		Fix:      worktreeIsolationFix(t.Project, profile, sharedCwdCollisionRoles(groups)),
	}
}

// worktreeIsolationFix names remedies that are ACTUALLY EXECUTABLE against the
// EXACT roster that is blocked.
//
// #538: the original text said "give each mutation-capable member its own --cwd
// (isolated worktree)" and named no command, so the operator concluded the flag
// did not exist and lost more time here than anywhere else in the run. A remedy
// the reader cannot execute is not a remedy.
//
// Second review F1: naming commands is not enough -- they must be SCOPED. An
// unscoped `team member update` targets the default profile, so for a blocked
// NAMED profile the suggested command would silently mutate a different roster.
// And a creation-time remedy is wrong here by construction: this profile already
// exists (readiness just read it), so `new profile NAME` would build an unrelated
// roster rather than fix this one. The creation forms belong only to the
// transactional-rollback case, which the preparation failure message owns.
func worktreeIsolationFix(project, profile string, roles []string) string {
	example := "ROLE"
	if len(roles) > 0 {
		example = roles[0]
	}
	scope := ""
	if p := strings.TrimSpace(project); p != "" {
		scope += " --project " + shellQuote(p)
	}
	// Only a non-default profile needs naming; adding --profile default would be
	// noise that invites cargo-culting it onto commands that reject it.
	if pr := strings.TrimSpace(profile); pr != "" && pr != team.DefaultProfile {
		scope += " --profile " + shellQuote(pr)
	}
	return strings.Join([]string{
		"give each mutation-capable member its own working directory with " +
			"'amq-squad team member update " + example + " --cwd /path/to/worktree" + scope + "'" +
			" (repeat per member; a relative --cwd resolves against the project)",
		"or accept the shared checkout deliberately with " +
			`'amq-squad team shared-cwd-exception set "<reason>"` + scope + "'",
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
