package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// Both checks honour the SAME exception (SharedCwdException clears it) and use the
// same vocabulary. On the CONDITION the claim is deliberately QUALIFIED, because
// an unqualified "same condition" was false and was caught in review twice:
//
//   - WHERE OBSERVABLE, this check uses doctor's own observable: a member cwd that
//     exists inside a Git checkout is grouped by its resolved index path
//     (git rev-parse --git-path index). That is what makes two SUBDIRECTORIES of
//     one checkout collide here as they do at runtime -- comparing directories
//     alone would call them distinct.
//   - WHERE NOT OBSERVABLE, a planned cwd that does not exist yet has no index to
//     resolve, so it is grouped by canonical directory as a declared PROXY: at
//     preparation the best available predictor of a distinct future index is a
//     distinct planned directory. doctor excludes such members entirely, because
//     at runtime a nonexistent worktree is a different finding.
//
// So: same observable where observable, declared proxy where not, same exception
// semantics and vocabulary always. Do not restate this as plain "same condition".
//
// What else differs is lifecycle-bound and deliberate:
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
	proxied := map[string]bool{}
	for _, m := range t.Members {
		if team.EffectiveActorMode(t, m) != team.ActorModeImplementation {
			continue
		}
		cwd := m.EffectiveCWD(t.Project)
		key, observed := memberIsolationKey(cwd)
		// Display the path as recorded; group by the observable (or its proxy).
		if _, seen := groupDisplay[key]; !seen {
			groupDisplay[key] = cwd
		}
		if !observed {
			proxied[key] = true
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
		detail := fmt.Sprintf("%s: %s", shown, strings.Join(roles, ", "))
		if proxied[key] {
			// Be explicit that this group was matched by planned directory rather
			// than an observed index, so the evidence never overstates what was
			// actually checked.
			detail += " (planned directory; no Git index to observe yet)"
		}
		collisions = append(collisions, detail)
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

// memberIsolationKey returns the grouping key for a member working directory and
// whether it was OBSERVED rather than proxied.
//
// #538 F4: doctor groups by resolved Git index path, so grouping by directory
// string made the two checks disagree on the condition itself. Two counterexamples
// drove the final shape:
//
//   - two SUBDIRECTORIES of one checkout are distinct directories but ONE index,
//     so a directory-only key misses a real collision;
//   - a planned worktree that does not exist yet has NO index, so an index-only
//     key cannot classify it at all (doctor excludes such members; preparation
//     must not, since predicting the collision is the whole point pre-launch).
//
// Hence the hybrid: observe the index when there is one to observe, and fall back
// to the canonical directory as a declared proxy when there is not. Callers must
// surface the proxy case rather than presenting it as an observation.
func memberIsolationKey(cwd string) (key string, observed bool) {
	canonical := canonicalFilesystemPath(cwd)
	if canonical == "" {
		canonical = strings.TrimSpace(cwd)
	}
	if index, ok := worktreeIsolationIndexProbe(canonical); ok {
		if resolved := canonicalFilesystemPath(index); resolved != "" {
			return resolved, true
		}
	}
	return canonical, false
}

// worktreeIsolationIndexProbe resolves a directory's Git index path using the same
// observable doctor uses (git rev-parse --git-path index). It is a seam so tests
// can exercise both branches without constructing real checkouts. A directory that
// does not exist, or is not inside a checkout, reports ok=false.
var worktreeIsolationIndexProbe = func(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-path", "index").Output()
	if err != nil {
		return "", false
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), true
}
