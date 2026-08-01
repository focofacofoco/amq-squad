package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// seedPreparedTeardownFixture creates the prepared-state footprint teardown is
// supposed to clear: the accepted manifest and its generation directory, plus
// the AMQ root and brief that rm already knew about.
func seedPreparedTeardownFixture(t *testing.T, project, profile, session string) (manifest, generations string) {
	t.Helper()
	manifest = preparedRunPath(project, profile, session)
	generations = preparedRunGenerationsPath(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"schema":3,"session":"`+session+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(generations, "abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generations, "abc123", "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest, generations
}

func seedRmSessionRoot(t *testing.T, baseRoot, session string) string {
	t.Helper()
	root := filepath.Join(baseRoot, session)
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRmRemovesPreparedManifestAndGenerationState is the #598 root cause 2
// regression. rm printed "session removed" while leaving
// .amq-squad/prepared/<profile>/<session>.json and <session>.generations on
// disk. Every later launch then loaded that stale accepted preview and drifted
// permanently, which is what turned a failed launch into a bricked namespace.
func TestRmRemovesPreparedManifestAndGenerationState(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "bricked"

	seedRmSessionRoot(t, baseRoot, session)
	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Yes: true,
	})
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, statErr := os.Stat(manifest); !os.IsNotExist(statErr) {
		t.Errorf("prepared manifest survived rm: %s (stat err %v)", manifest, statErr)
	}
	if _, statErr := os.Stat(generations); !os.IsNotExist(statErr) {
		t.Errorf("prepared generation state survived rm: %s (stat err %v)", generations, statErr)
	}
	// The print must be true, not merely reassuring: a verb that claims to have
	// removed the session while leaving the state that bricks it is worse than
	// one that refuses.
	for _, want := range []string{manifest, generations} {
		if !strings.Contains(out, want) {
			t.Errorf("rm output does not name the removed path %q\noutput:\n%s", want, out)
		}
	}
}

// TestRmOfOneSessionLeavesAnotherSessionsPreparedStateIntact is the scoping
// negative. The prepared tree is shared by every session in a profile, so a
// teardown that reached one directory too far would be a cross-session
// data-loss bug rather than a local one.
func TestRmOfOneSessionLeavesAnotherSessionsPreparedStateIntact(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile := team.DefaultProfile

	seedRmSessionRoot(t, baseRoot, "alpha")
	seedRmSessionRoot(t, baseRoot, "beta")
	_, _ = seedPreparedTeardownFixture(t, project, profile, "alpha")
	betaManifest, betaGenerations := seedPreparedTeardownFixture(t, project, profile, "beta")

	if _, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: "alpha",
		Profile: profile, Yes: true,
	}); err != nil {
		t.Fatalf("rm alpha: %v", err)
	}

	if _, err := os.Stat(betaManifest); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's prepared manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(betaGenerations, "abc123", "manifest.json")); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's generation state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseRoot, "beta")); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's AMQ root: %v", err)
	}
}

// TestRmClearsAnOrphanedPreparedManifestWithNoRootOrBrief closes the recovery
// deadlock. After a failed launch the AMQ root and brief can already be gone
// while the prepared manifest survives -- the exact bricked state. rm used to
// refuse that with "nothing to remove", so the one verb able to clear the
// orphan was the one that declined to look at it.
func TestRmClearsAnOrphanedPreparedManifestWithNoRootOrBrief(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "orphaned"
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	_, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Yes: true,
	})
	if err != nil {
		t.Fatalf("rm must clear an orphaned prepared manifest instead of refusing: %v", err)
	}
	if _, statErr := os.Stat(manifest); !os.IsNotExist(statErr) {
		t.Errorf("orphaned prepared manifest survived: %s", manifest)
	}
	if _, statErr := os.Stat(generations); !os.IsNotExist(statErr) {
		t.Errorf("orphaned generation state survived: %s", generations)
	}
}

// TestRmPreviewNamesPreparedStateBeforeRemoving keeps the confirm gate honest.
// The operator approves what the preview lists, so prepared state must appear
// there before rm is allowed to delete it.
func TestRmPreviewNamesPreparedStateBeforeRemoving(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "previewed"

	seedRmSessionRoot(t, baseRoot, session)
	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	// A declined confirmation renders the full preview and makes zero
	// filesystem changes, which is exactly the observation this needs.
	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Confirm: strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("declined rm must not error: %v", err)
	}
	for _, want := range []string{manifest, generations} {
		if !strings.Contains(out, want) {
			t.Errorf("preview omits prepared path %q\noutput:\n%s", want, out)
		}
	}
	if _, statErr := os.Stat(manifest); statErr != nil {
		t.Errorf("preview must not delete anything, but the manifest is gone: %v", statErr)
	}
	if _, statErr := os.Stat(generations); statErr != nil {
		t.Errorf("preview must not delete anything, but the generation state is gone: %v", statErr)
	}
}
