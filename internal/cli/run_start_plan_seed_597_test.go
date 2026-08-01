package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// #597 guard 3: --prepare-plan is the READ-ONLY proposal stage, but the
// accepted-brief goal binding resolved against a brief path that only --prepare
// creates. On a fresh namespace it refused even though --seed-from named the
// content on the same command line.
//
// Tests drive runRunStart, the stable operator entry point, so the same
// unmodified file runs at the parent commit.

func seedPlanStageFixture(t *testing.T) (project, seedRef string) {
	t.Helper()
	project = t.TempDir()
	seedPath := filepath.Join(project, "seed-brief.md")
	if err := os.WriteFile(seedPath, []byte("# Tonight\n\nReal seeded brief content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, "file:" + seedPath
}

func runPlanStage(t *testing.T, project, seedRef, goal string) (string, error) {
	t.Helper()
	args := []string{
		"--project", project, "--profile", team.DefaultProfile, "--session", "tonight",
		"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--visibility", "detached", "--prepare-plan",
	}
	if seedRef != "" {
		args = append(args, "--seed-from", seedRef)
	}
	if goal != "" {
		args = append(args, "--goal", goal)
	}
	out, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	return out, err
}

// TestPlanStageBindsTheSeededGoal is the relaxation, and it asserts the EFFECT:
// the plan stage produces a goal binding, not merely that the old refusal
// stopped firing.
func TestPlanStageBindsTheSeededGoal(t *testing.T) {
	project, seedRef := seedPlanStageFixture(t)

	out, err := runPlanStage(t, project, seedRef, "")
	if err != nil {
		t.Fatalf("--prepare-plan with --seed-from must not refuse on a fresh namespace: %v", err)
	}
	// The observable outcome: the proposal reports a ready goal_binding row
	// carrying the accepted-brief source and a computed digest.
	if !strings.Contains(out, "goal_binding") {
		t.Fatalf("plan output has no goal_binding row:\n%s", out)
	}
	for _, want := range []string{runwizard.GoalBindingSourceAcceptedBrief, "digest="} {
		if !strings.Contains(out, want) {
			t.Errorf("goal binding does not carry %q; the plan stage must BIND the goal, not just stop refusing\n%s", want, out)
		}
	}
	// Read-only really is read-only: the plan stage must not have written the
	// brief it bound against.
	if _, statErr := os.Stat(briefPathForProfile(project, team.DefaultProfile, "tonight")); !os.IsNotExist(statErr) {
		t.Errorf("--prepare-plan wrote the brief; it must stay read-only (stat err %v)", statErr)
	}
}

// TestPlanStageStillRefusesWithNoGoalSource is what the guard still refuses.
// Relaxing it is not deleting it: a plan stage with no brief, no seed and no
// explicit goal has nothing to bind and must still say so.
func TestPlanStageStillRefusesWithNoGoalSource(t *testing.T) {
	project, _ := seedPlanStageFixture(t)

	_, err := runPlanStage(t, project, "", "")
	if err == nil {
		t.Fatal("a plan stage with no brief, no --seed-from and no --goal must still be refused")
	}
	// And the refusal must name BOTH escape hatches. Naming one is how the next
	// operator files the same issue.
	for _, want := range []string{"--seed-from", "--goal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %s; got: %v", want, err)
		}
	}
}

// TestPlanStageExplicitGoalStillWorks pins the pre-existing escape hatch, so
// the relaxation cannot quietly become the only way through.
func TestPlanStageExplicitGoalStillWorks(t *testing.T) {
	project, _ := seedPlanStageFixture(t)
	if _, err := runPlanStage(t, project, "", "Execute the explicit goal"); err != nil {
		t.Fatalf("--prepare-plan with an explicit --goal must work: %v", err)
	}
}
