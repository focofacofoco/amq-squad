package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamLaunchRejectsPreFloorAMQBeforeParentMutations(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-team"}},
	})
	backend := useFakeBackend(t)
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	err := executeTeamLaunch(teamLaunchOptions{
		Terminal: "fake", Workstream: "floor-team", Profile: team.DefaultProfile,
		Trust: trustModeApproveForMe, SquadBin: "amq-squad", NoBootstrap: true,
	}, true, true)
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("team launch floor error=%v", err)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor team launch opened %d backend launch(es)", len(backend.launches))
	}
	for _, path := range []string{
		filepath.Join(base, "floor-team"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-team"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-team"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor team launch mutated %s: %v", path, statErr)
		}
	}
}

func TestUpResetRejectsPreFloorAMQBeforeDeletingSession(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-reset"}},
	})
	backend := useFakeBackend(t)
	root := filepath.Join(base, "floor-reset")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "must-survive")
	if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-reset", "--reset", "--yes", "--terminal", "fake"})
	})
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("up --reset floor error=%v", err)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "unchanged\n" {
		t.Fatalf("pre-floor reset changed sentinel: body=%q err=%v", got, readErr)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor reset opened %d backend launch(es)", len(backend.launches))
	}
	for _, path := range []string{
		briefPathForProfile(dir, team.DefaultProfile, "floor-reset"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-reset"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor reset mutated %s: %v", path, statErr)
		}
	}
}

func TestResumeExecRejectsPreFloorAMQBeforeParentMutations(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-resume"}},
	})
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	err := executeResume(resumeExecution{
		ProjectDir: dir, RequestedSession: "floor-resume", ExplicitSession: true,
		Profile: team.DefaultProfile,
		Exec:    resumeExecOptions{Enabled: true, Terminal: "tmux", Target: "current-window", Layout: "vertical"},
	})
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("resume --exec floor error=%v", err)
	}
	for _, path := range []string{
		filepath.Join(base, "floor-resume"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-resume"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-resume"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor resume mutated %s: %v", path, statErr)
		}
	}
}
