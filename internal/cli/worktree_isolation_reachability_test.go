package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #538 acceptance criterion 2: a two-implementer squad must reach `ready` from a
// clean repo WITHOUT hand-editing team.json and WITHOUT trial-and-error across
// three commands.
//
// The original failure was a loop: --prepare blocked on worktree_isolation, the
// fix text named a --cwd flag the operator could not find and an exception
// command that needed a profile --prepare had just rolled back. These tests pin
// that each remedy the row names is reachable in ONE roster-creation command.

// remedy 1, at creation: per-member isolated working directories.
func TestSharedCwdRemedyIsolatedCwdsReachableInOneCommand(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	wtA := filepath.Join(dir, "wt-a")
	wtB := filepath.Join(dir, "wt-b")
	for _, p := range []string{wtA, wtB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--cwd", "dev-1=" + wtA + ",dev-2=" + wtB,
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("one-command roster creation with isolated cwds failed: %v", err)
	}

	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	// The readiness row must be satisfied without an exception being recorded.
	row := worktreeIsolationReadinessRow(tm)
	if row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s); isolated cwds must satisfy it. fix text was: %s", row.Status, row.Evidence, row.Fix)
	}
	if strings.TrimSpace(tm.SharedCwdException) != "" {
		t.Fatal("isolated cwds must not require a recorded exception")
	}
}

// remedy 2, at creation: deliberately accept the shared checkout.
//
// --shared-cwd-exception was one of the 17 value-taking team-init flags that
// `new profile` silently dropped, so this remedy was not merely undocumented but
// actively rejected with an error blaming the operator's argument.
func TestSharedCwdRemedyExceptionReachableInOneCommand(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--shared-cwd-exception", "single checkout accepted for this run",
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("one-command roster creation with a recorded exception failed: %v", err)
	}

	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(tm.SharedCwdException) == "" {
		t.Fatal("--shared-cwd-exception was accepted but not recorded")
	}
	row := worktreeIsolationReadinessRow(tm)
	if row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s); a recorded exception must satisfy it", row.Status, row.Evidence)
	}
}

// remedy 1, post-creation: fixing an EXISTING roster without editing team.json.
// This is the path that did not exist at all before #538.
func TestSharedCwdRemedyIsolatedCwdReachableOnExistingRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("roster creation failed: %v", err)
	}

	// Precondition: this roster IS blocked, so the test proves a real fix.
	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	blocked := worktreeIsolationReadinessRow(tm)
	if blocked.Status != "blocked" {
		t.Fatalf("expected a shared-cwd collision to start blocked, got %s (%s)", blocked.Status, blocked.Evidence)
	}

	wt := filepath.Join(dir, "wt-a")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "dev-2", "--project", dir, "--profile", "squad", "--cwd", wt})
	}); err != nil {
		t.Fatalf("team member update --cwd failed: %v", err)
	}

	fixed, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if row := worktreeIsolationReadinessRow(fixed); row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s) after giving dev-2 its own cwd", row.Status, row.Evidence)
	}
	// And it must be reversible: clearing the override restores the collision.
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "dev-2", "--project", dir, "--profile", "squad", "--cwd", ""})
	}); err != nil {
		t.Fatalf("clearing --cwd failed: %v", err)
	}
	cleared, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if row := worktreeIsolationReadinessRow(cleared); row.Status != "blocked" {
		t.Fatalf("clearing the cwd override should restore the collision, got %s", row.Status)
	}
}

// #538 acceptance criterion 1: every remedy the row NAMES must be executable.
// This asserts the fix text references only real commands and flags, so it cannot
// drift back into naming a bare "--cwd" with no command attached.
func TestWorktreeIsolationFixNamesOnlyExecutableRemedies(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	fix := worktreeIsolationReadinessRow(tm).Fix
	if fix == "" {
		t.Fatal("a blocked row must carry a fix")
	}
	// Each named command must exist as a real verb, and each named flag must be
	// accepted by it.
	for _, want := range []string{
		"amq-squad new profile NAME --cwd",
		"amq-squad team member update",
		"--shared-cwd-exception",
		"amq-squad team shared-cwd-exception set",
	} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix text does not name %q; text was: %s", want, fix)
		}
	}
	// It must name a role from the operator's OWN roster, not a placeholder.
	if !strings.Contains(fix, "dev-1") {
		t.Fatalf("fix text should name a colliding role from this roster; text was: %s", fix)
	}
	// The flags it names must be forwarded by new profile (the Finding B gap).
	for _, flagName := range []string{"--cwd", "--shared-cwd-exception"} {
		if !newProfileValueFlags[flagName] {
			t.Fatalf("fix text names %s but `new profile` does not forward it, so the remedy is unrunnable", flagName)
		}
	}
}
