package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// #538 F3: the rollback guidance must be TRUE for the case it is printed in.
//
// Preparation is transactional. If it CREATED the profile, rollback removes it and
// the fix-existing commands have nothing to act on. If the profile already
// existed, rollback RESTORES it and those commands work as printed. Emitting the
// fresh-profile text unconditionally made the message false in the second case,
// which is worse than silence: it sends the operator to a command that cannot work
// while telling them the working one is unavailable.
func TestPreparationRollbackGuidanceDistinguishesCreatedFromRestored(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), ".amq-squad", "teams", "squad.json")

	t.Run("profile created by this transaction", func(t *testing.T) {
		msg := preparationRollbackGuidance([]runPreparationFileSnapshot{{Path: profilePath, Exists: false}}, profilePath)
		if !strings.Contains(msg, "CREATED") || !strings.Contains(msg, "removed") {
			t.Fatalf("must say the profile was created and removed; got: %s", msg)
		}
		// The only runnable remedy here is the creation form.
		if !strings.Contains(msg, "new profile NAME") {
			t.Fatalf("must point at the creation form; got: %s", msg)
		}
		if strings.Contains(msg, "RESTORED") {
			t.Fatalf("must not claim restoration; got: %s", msg)
		}
	})

	t.Run("profile pre-existed", func(t *testing.T) {
		msg := preparationRollbackGuidance([]runPreparationFileSnapshot{{Path: profilePath, Exists: true}}, profilePath)
		if !strings.Contains(msg, "RESTORED") {
			t.Fatalf("must say the profile was restored; got: %s", msg)
		}
		// Telling the operator to create it would fail: it already exists.
		if strings.Contains(msg, "new profile NAME") {
			t.Fatalf("must NOT offer the creation form for a profile that still exists; got: %s", msg)
		}
		if strings.Contains(msg, "have nothing to act on") {
			t.Fatalf("the fix-existing commands DO apply here; got: %s", msg)
		}
	})

	// Unknown/untracked path must degrade to the safe, non-false statement rather
	// than assuming creation.
	t.Run("profile not tracked in the snapshot set", func(t *testing.T) {
		msg := preparationRollbackGuidance(nil, profilePath)
		if strings.Contains(msg, "CREATED") {
			t.Fatalf("must not assert creation without evidence; got: %s", msg)
		}
		if !strings.Contains(msg, "RESTORED") {
			t.Fatalf("must fall back to the restored wording; got: %s", msg)
		}
	})
}
