package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// The tests in this file lead with the end-to-end launch path on purpose.
// #493 is a defect about what a real launch prints to a real human, so the
// primary assertions drive runUp and read its actual stdout. The renderer unit
// tests below them cover mode-by-mode wording; they are the supporting cast,
// not the proof.

func handoffCardTeam(session string, op *team.OperatorConfig) team.Team {
	return team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Operator:     op,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: session},
			{Role: "dev-1", Binary: "claude", Handle: "dev-1", Session: session},
		},
	}
}

func operatorEnabled(mode string, notifications *team.OperatorNotificationPolicy) *team.OperatorConfig {
	return &team.OperatorConfig{
		Enabled:         true,
		Handle:          "user",
		InteractionMode: mode,
		Participant:     true,
		Notifications:   notifications,
	}
}

// TestLiveLaunchPrintsOperatorHandoffCard is the core #493 regression: a real
// live launch must end by telling the human they are the operator. Before this
// change the launch ended at "started <session>" and the operator was never
// told the squad would be talking to them.
func TestLiveLaunchPrintsOperatorHandoffCard(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, handoffCardTeam("issue-493", operatorEnabled(team.OperatorInteractionLeadPane, nil)))

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-493", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, want := range []string{
		"YOU ARE THE OPERATOR FOR THIS SESSION",
		"Handle   user",
		"the lead pane (role cto)",
		"amq-squad operator answer --session issue-493 --gate <topic> --approved|--denied",
		"amq-squad status --session issue-493",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("launch stdout missing %q; full output:\n%s", want, stdout)
		}
	}
}

// TestQuietDoesNotSuppressOperatorHandoffCard pins the #493 safety-output
// requirement. The test is only meaningful if --quiet is genuinely in effect,
// so it also asserts that a quiet-suppressible notice really did disappear:
// otherwise a broken policy would make this pass vacuously.
func TestQuietDoesNotSuppressOperatorHandoffCard(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, handoffCardTeam("issue-493q", operatorEnabled(team.OperatorInteractionSeparateTerminal, nil)))
	withOutputPolicy(t, outputPolicy{Quiet: true})

	stdout, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-493q", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	// Control: prove quiet was actually applied.
	if strings.Contains(stderr, "started issue-493q") {
		t.Fatalf("quiet policy was not in effect; stderr still carries the launch notice:\n%s", stderr)
	}
	if !strings.Contains(stdout, "YOU ARE THE OPERATOR FOR THIS SESSION") {
		t.Errorf("--quiet suppressed the operator handoff card; stdout:\n%s", stdout)
	}
}

// TestDryRunPrintsNoOperatorHandoffCard: a plan is not a handoff. Printing the
// card for --dry-run would tell an operator they are on the hook for a session
// that was never launched.
func TestDryRunPrintsNoOperatorHandoffCard(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, handoffCardTeam("issue-493d", operatorEnabled(team.OperatorInteractionLeadPane, nil)))

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--terminal", "fake", "--session", "issue-493d", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	if strings.Contains(stdout, "YOU ARE THE OPERATOR") {
		t.Errorf("--dry-run printed the handoff card; stdout:\n%s", stdout)
	}
}

// TestLiveLaunchCardWarnsWhenNotificationsOff covers the line the issue calls
// the important one: silent-by-design has to become an informed choice.
func TestLiveLaunchCardWarnsWhenNotificationsOff(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, handoffCardTeam("issue-493n", operatorEnabled(team.OperatorInteractionLeadPane, nil)))

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-493n", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, want := range []string{
		"ALERTS   OFF — NOTHING WILL INTERRUPT YOU.",
		"amq-squad next --session issue-493n",
		"amq-squad monitor --session issue-493n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("notifications-off launch missing %q; stdout:\n%s", want, stdout)
		}
	}
}

// TestOperatorHandoffCardReportsConfiguredSinks proves the ON branch reads the
// profile's real sink types rather than asserting a generic "notifications are
// on". A card that cannot name the sink cannot be checked by the operator.
func TestOperatorHandoffCardReportsConfiguredSinks(t *testing.T) {
	policy := &team.OperatorNotificationPolicy{
		Enabled: true,
		Sinks: []team.OperatorNotificationSinkConfig{
			{ID: "desk", Type: "desktop"},
			{ID: "term", Type: "terminal-bell"},
		},
	}
	card := newOperatorHandoffCard(handoffCardTeam("s", operatorEnabled(team.OperatorInteractionLeadPane, policy)), "", "s")
	if !card.NotificationsEnabled {
		t.Fatal("NotificationsEnabled = false, want true")
	}
	var sb strings.Builder
	writeOperatorHandoffCard(&sb, card)
	got := sb.String()
	if !strings.Contains(got, "ALERTS   ON via desktop, terminal-bell.") {
		t.Errorf("card did not name the configured sinks; got:\n%s", got)
	}
	if strings.Contains(got, "NOTHING WILL INTERRUPT YOU") {
		t.Errorf("card printed the OFF warning while notifications are on; got:\n%s", got)
	}
}

// TestOperatorHandoffCardConsolePerInteractionMode: the console line must be
// derived, not boilerplate. Each mode sends the human somewhere different, and
// the 2026-07-18 failure was precisely a human who did not know which.
func TestOperatorHandoffCardConsolePerInteractionMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string
	}{
		{team.OperatorInteractionLeadPane, "the lead pane (role cto)"},
		{team.OperatorInteractionSeparateTerminal, "a separate terminal"},
		{team.OperatorInteractionNOC, "the NOC/global board"},
		{team.OperatorInteractionSelfOperator, "under self-operator policy"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			card := newOperatorHandoffCard(handoffCardTeam("s", operatorEnabled(tc.mode, nil)), "", "s")
			if !strings.Contains(card.Console, tc.want) {
				t.Errorf("Console = %q, want it to contain %q", card.Console, tc.want)
			}
		})
	}
}

// TestOperatorHandoffCardLeadPaneWithoutLeadRole guards the derivation: when no
// lead role is configured the card must degrade to the generic phrase rather
// than print "role " with nothing after it.
func TestOperatorHandoffCardLeadPaneWithoutLeadRole(t *testing.T) {
	tm := handoffCardTeam("s", operatorEnabled(team.OperatorInteractionLeadPane, nil))
	tm.Lead = ""
	card := newOperatorHandoffCard(tm, "", "s")
	if card.Console != "the lead pane" {
		t.Errorf("Console = %q, want %q", card.Console, "the lead pane")
	}
}

// TestOperatorHandoffCardWhenOperatorGatesDisabled: "no human is on the hook"
// is itself a fact the operator needs at handoff. The card must not claim a
// handle that will never receive anything.
func TestOperatorHandoffCardWhenOperatorGatesDisabled(t *testing.T) {
	card := newOperatorHandoffCard(handoffCardTeam("s", &team.OperatorConfig{Enabled: false}), "", "s")
	if card.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	var sb strings.Builder
	writeOperatorHandoffCard(&sb, card)
	got := sb.String()
	if !strings.Contains(got, "OPERATOR GATES ARE DISABLED FOR THIS PROFILE") {
		t.Errorf("disabled card missing its headline; got:\n%s", got)
	}
	if strings.Contains(got, "YOU ARE THE OPERATOR") {
		t.Errorf("disabled card still claims the reader is the operator; got:\n%s", got)
	}
}

// TestOperatorHandoffCommandsScopeToProfileAndSession keeps the printed
// commands copy-pasteable. A non-default profile must appear, and the default
// profile must not be spelled out (matching the adjacent launch "next:" line).
func TestOperatorHandoffCommandsScopeToProfileAndSession(t *testing.T) {
	next, monitor, answer, status := operatorHandoffCommands("squad-v2-27-0", "v2-27-0")
	for _, got := range []string{next, monitor, answer, status} {
		if !strings.Contains(got, "--profile squad-v2-27-0") {
			t.Errorf("%q missing --profile", got)
		}
		if !strings.Contains(got, "--session v2-27-0") {
			t.Errorf("%q missing --session", got)
		}
	}
	defaultNext, _, _, _ := operatorHandoffCommands(team.DefaultProfile, "v2-27-0")
	if strings.Contains(defaultNext, "--profile") {
		t.Errorf("default profile should stay implicit, got %q", defaultNext)
	}
}
