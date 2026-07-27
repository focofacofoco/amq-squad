package wizard

import (
	"strings"
	"testing"
)

// #455 item 2: a codex NOC under default sandboxing cannot drive tmux at all, and the
// wizard never asked -- operators hand-typed sandbox flags into free-text native args.
// These cover the structured posture step, both flows, and the visible-degradation rule.

func TestGlobalPostureArgsAreEmittedPerBinary(t *testing.T) {
	cases := []struct {
		agent, posture, wantFlag string
	}{
		{"codex", "full-access", "--sandbox danger-full-access"},
		{"codex", "workspace-write", "--sandbox workspace-write"},
		{"codex", "read-only", "--sandbox read-only"},
		{"claude", "full-access", "--dangerously-skip-permissions"},
	}
	for _, tc := range cases {
		t.Run(tc.agent+"/"+tc.posture, func(t *testing.T) {
			got := globalPostureArgs(tc.agent, tc.posture)
			if !strings.Contains(got, tc.wantFlag) {
				t.Errorf("globalPostureArgs(%q,%q) = %q, want it to contain %q", tc.agent, tc.posture, got, tc.wantFlag)
			}
		})
	}
}

// The tmux-capable posture must be FIRST for every binary: a global orchestrator that
// cannot drive tmux cannot do its job, so the working choice must not be second.
func TestTmuxCapablePostureIsListedFirst(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		choices := GlobalPostureChoices(agent)
		if len(choices) == 0 {
			t.Fatalf("no postures for %q", agent)
		}
		if !choices[0].DrivesTmux {
			t.Errorf("%s: first posture %q does not drive tmux", agent, choices[0].Value)
		}
	}
}

// Every posture must state its consequence, because #455's complaint is that the operator
// could not know the effect -- naming the flag is not enough.
func TestEveryPostureStatesItsConsequence(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, c := range GlobalPostureChoices(agent) {
			if strings.TrimSpace(c.Consequence) == "" {
				t.Errorf("%s/%s has no consequence text", agent, c.Value)
			}
			if !c.DrivesTmux && !strings.Contains(c.Label, "CANNOT") {
				t.Errorf("%s/%s cannot drive tmux but its label does not say so: %q", agent, c.Value, c.Label)
			}
		}
	}
}

// Posture args PREPEND operator free text so an explicitly typed flag still wins: later
// flags generally take precedence, so the picker assists rather than overrides.
func TestPostureArgsPrecedeOperatorFreeText(t *testing.T) {
	s := Spec{
		Backend: BackendGlobalStart, GlobalRoot: "/tmp/root", GlobalAgent: "codex",
		GlobalPosture: "full-access", GlobalCodexArgs: "--sandbox read-only",
	}
	args := strings.Join(s.GlobalArgs(), " ")
	posture := strings.Index(args, "danger-full-access")
	operator := strings.Index(args, "--sandbox read-only")
	if posture < 0 || operator < 0 {
		t.Fatalf("expected both postures present in %q", args)
	}
	if posture > operator {
		t.Errorf("posture args must PRECEDE operator free text so the operator's flag wins; got %q", args)
	}
}

// Unknown posture is non-breaking but must NOT be invisible: silently applying no sandbox
// flags recreates the item-2 failure through the back door, and the operator has LESS
// reason to suspect it because they used the posture step.
func TestUnknownPostureDegradesVisibly(t *testing.T) {
	if got := globalPostureArgs("codex", "typo-posture"); got != "" {
		t.Errorf("unknown posture must apply no args, got %q", got)
	}
	line := globalPostureReviewLine("codex", "typo-posture")
	for _, want := range []string{"unknown posture", "typo-posture", "no sandbox flags applied"} {
		if !strings.Contains(line, want) {
			t.Errorf("review line %q must contain %q so the degradation is visible", line, want)
		}
	}
}

// The review gate shows the CONSEQUENCE, not just the stored value: the operator approves
// an outcome.
func TestReviewLineStatesTheTmuxConsequence(t *testing.T) {
	capable := globalPostureReviewLine("codex", "full-access")
	if !strings.Contains(capable, "can drive tmux") {
		t.Errorf("capable posture line %q must state it can drive tmux", capable)
	}
	restricted := globalPostureReviewLine("codex", "read-only")
	if !strings.Contains(restricted, "CANNOT drive tmux") {
		t.Errorf("restricted posture line %q must state it CANNOT drive tmux", restricted)
	}
}
