package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func eligibleGoalSupervisionInput() goalSupervisionAssessmentInput {
	observedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	return goalSupervisionAssessmentInput{
		ObservedAt: observedAt,
		Now:        observedAt.Add(time.Second),
		MaxAge:     30 * time.Second,
		Binding: GoalSupervisionBinding{
			Project: "/project", Profile: "default", Session: "release", NamespaceID: "default/release",
			LeadRole: "cto", LeadHandle: "cto", LaunchID: "launch-1",
			LaunchStartedAt: observedAt.Add(-time.Minute), LaunchRecordDigest: "launch-digest",
			LaunchRecordModTime: observedAt.Add(-time.Minute).UnixNano(),
			Runtime: GoalSupervisionRuntimeIdentity{
				Live: true, PIDLive: true, PaneLive: true, PIDAlive: true, BinaryMatch: true,
			},
			Pane: GoalSupervisionPaneIdentity{
				Managed: true, Session: "amq", WindowID: "@1", WindowName: "cto",
				PaneID: "%1", Target: "amq:cto", BusyKnown: true,
			},
			Goal: GoalSupervisionGoalIdentity{
				Mode: "native_goal_blocked", NativeGoal: true, Verified: true,
				Source: "launch-record", DeliveryState: "blocked", GoalDigest: "goal-digest",
				AttemptID: "attempt-1", BindingDigest: "binding-digest", CommandDigest: "command-digest",
			},
			PauseGeneration:       "pause-generation",
			PreparedRunGeneration: "prepared-generation",
			PreparedRunDigest:     "prepared-digest",
			PreparedLaunchAttempt: "prepared-attempt",
			PreparedGoalNamespace: "default/release",
			PreparedGoalDigest:    "prepared-goal-digest",
		},
		GoalStateKnown: true,
		NativePaused:   true,
		Blocker: GoalSupervisionBlockerEvidence{
			ID: "blocker-1", Known: true, Resolved: true,
			Detail: "dependency complete", ResolutionDigest: "resolution-digest",
		},
		Gates:         GoalSupervisionGateEvidence{Known: true},
		Policy:        team.GoalSupervisionPolicyStatus{Mode: team.GoalSupervisionSafeAuto, Revision: 2, Source: "profile"},
		Claim:         GoalSupervisionClaimProjection{State: "none"},
		ClaimClear:    true,
		BudgetAllowed: true,
	}
}

func TestAssessGoalSupervisionEligibilityTruthTable(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*goalSupervisionAssessmentInput)
		state     GoalSupervisionState
		eligible  bool
		automatic bool
	}{
		{
			name: "safe auto eligible", state: GoalSupervisionNativeGoalPausedEligible,
			eligible: true, automatic: true,
		},
		{
			name: "manual eligible but confirmation required",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Policy.Mode = team.GoalSupervisionManual
			},
			state: GoalSupervisionNativeGoalPausedEligible, eligible: true,
		},
		{
			name: "notify only eligible but never resumes",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Policy.Mode = team.GoalSupervisionNotifyOnly
			},
			state: GoalSupervisionNativeGoalPausedEligible, eligible: true,
		},
		{
			name: "running",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.NativePaused = false
			},
			state: GoalSupervisionRunning,
		},
		{
			name: "goal state unverified",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.GoalStateKnown = false
				in.NativePaused = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "parked waiting amq",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.ParkedWaitingAMQ = true
			},
			state: GoalSupervisionParkedWaitingAMQ,
		},
		{
			name: "goal terminal",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.GoalTerminal = true
			},
			state: GoalSupervisionGoalTerminal,
		},
		{
			name: "lead down",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Runtime.Live = false
			},
			state: GoalSupervisionLeadDown,
		},
		{
			name: "lead down ambiguous scope",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Runtime.Live = false
				in.Gates.Ambiguous = true
			},
			state: GoalSupervisionLeadDown,
		},
		{
			name: "pane busy",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Pane.Busy = true
			},
			state: GoalSupervisionPaneBusyOrUnverified,
		},
		{
			name: "pane idle unverified",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Pane.BusyKnown = false
			},
			state: GoalSupervisionPaneBusyOrUnverified,
		},
		{
			name: "open gate requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates.Open = 1
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "local input requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.LocalInput = GoalSupervisionLocalInputEvidence{Observed: true, Kind: "permission"}
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "known unresolved blocker requires human",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Blocker.Resolved = false
				in.Blocker.ResolutionDigest = ""
			},
			state: GoalSupervisionNativeGoalBlockedHuman,
		},
		{
			name: "unknown blocker",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Blocker = GoalSupervisionBlockerEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "incomplete source",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.SourceErrors = []string{"launch source unavailable"}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "ambiguous gate",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates.Ambiguous = true
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "gate source unknown is not a clear zero",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Gates = GoalSupervisionGateEvidence{}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "invariant violation",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.InvariantErrors = []string{"lead identity mismatch"}
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "prior claim indeterminate",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Claim.Indeterminate = true
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "retry budget exhausted",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.BudgetAllowed = false
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "stale assessment",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Now = in.ObservedAt.Add(in.MaxAge + time.Nanosecond)
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "future observation",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Now = in.ObservedAt.Add(-time.Nanosecond)
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "attempt identity missing",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.Goal.AttemptID = ""
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
		{
			name: "prepared binding missing",
			mutate: func(in *goalSupervisionAssessmentInput) {
				in.Binding.PreparedRunGeneration = ""
			},
			state: GoalSupervisionNativeGoalBlockedUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := eligibleGoalSupervisionInput()
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			got := assessGoalSupervision(in)
			if got.State != tc.state || got.Eligible != tc.eligible || got.AutomaticResumeAllowed != tc.automatic {
				t.Fatalf("assessment = state %q eligible=%t automatic=%t, want %q/%t/%t; reasons=%+v",
					got.State, got.Eligible, got.AutomaticResumeAllowed,
					tc.state, tc.eligible, tc.automatic, got.Reasons)
			}
			if tc.name == "manual eligible but confirmation required" &&
				(!got.Actions.Resume.Available || !got.Actions.Resume.NeedsConfirmation) {
				t.Fatalf("manual resume action = %+v, want available with confirmation", got.Actions.Resume)
			}
			if tc.name == "notify only eligible but never resumes" && got.Actions.Resume.Available {
				t.Fatalf("notify-only resume action = %+v, want unavailable", got.Actions.Resume)
			}
			if tc.name == "lead down" && !got.Actions.Restore.Available {
				t.Fatalf("exact lead-down restore action = %+v, want available", got.Actions.Restore)
			}
			if tc.name == "lead down ambiguous scope" && got.Actions.Restore.Available {
				t.Fatalf("ambiguous lead-down restore action = %+v, want unavailable", got.Actions.Restore)
			}
		})
	}
}

func TestAssessGoalSupervisionEligibilityReasonsAreExplicitAndOrdered(t *testing.T) {
	got := assessGoalSupervision(eligibleGoalSupervisionInput())
	var codes []string
	for _, reason := range got.Reasons {
		if !reason.Passed {
			t.Fatalf("eligible reason %q failed: %+v", reason.Code, reason)
		}
		codes = append(codes, reason.Code)
	}
	want := []string{
		"fresh_assessment", "sources_complete", "exact_namespace", "exact_lead_identity",
		"resumable_lifecycle", "native_goal_paused", "goal_attempt", "launch_generation", "prepared_run_binding",
		"pause_generation", "pane_identity", "pane_idle", "blocker_known", "blocker_resolved",
		"gates_known", "no_open_gate", "no_gate_ambiguity", "no_local_input",
		"invariants_ok", "claim_clear", "retry_budget",
	}
	if !reflect.DeepEqual(gotCodes(got.Reasons), want) {
		t.Fatalf("reason codes = %v, want %v", codes, want)
	}
}

func TestGoalSupervisionFingerprintIgnoresObservationTimeButBindsAttempt(t *testing.T) {
	firstInput := eligibleGoalSupervisionInput()
	first := assessGoalSupervision(firstInput)

	secondInput := eligibleGoalSupervisionInput()
	secondInput.ObservedAt = secondInput.ObservedAt.Add(10 * time.Second)
	secondInput.Now = secondInput.Now.Add(10 * time.Second)
	second := assessGoalSupervision(secondInput)
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fresh observations changed fingerprint: %q != %q", first.Fingerprint, second.Fingerprint)
	}

	secondInput.Binding.Goal.AttemptID = "attempt-2"
	changed := assessGoalSupervision(secondInput)
	if first.Fingerprint == changed.Fingerprint {
		t.Fatalf("attempt change did not change fingerprint %q", first.Fingerprint)
	}
}

func TestGoalSupervisionProjectionJSONConsistentAcrossStatusBoardDoctor(t *testing.T) {
	assessment := assessGoalSupervision(eligibleGoalSupervisionInput())
	surfaces := []struct {
		name  string
		value any
	}{
		{name: "status", value: statusEnvelopeData{GoalSupervision: assessment}},
		{name: "board", value: sessionBoardRow{GoalSupervision: &assessment}},
		{name: "doctor", value: doctorCheck{Kind: "goal_supervision", GoalSupervision: &assessment}},
	}
	var canonical json.RawMessage
	for _, surface := range surfaces {
		payload, err := json.Marshal(surface.value)
		if err != nil {
			t.Fatalf("%s marshal: %v", surface.name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatalf("%s decode: %v", surface.name, err)
		}
		got := fields["goal_supervision"]
		if len(got) == 0 {
			t.Fatalf("%s omitted goal_supervision: %s", surface.name, payload)
		}
		if canonical == nil {
			canonical = append(json.RawMessage(nil), got...)
			continue
		}
		if !bytes.Equal(canonical, got) {
			t.Fatalf("%s projection differs:\n%s\n%s", surface.name, canonical, got)
		}
	}
}

func TestInspectGoalSupervisionGatesReadsDurableOpenGate(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	cfg, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	observation := inspectGoalSupervisionGates(cfg, team.DefaultProfile, "s", filepath.Join(base, "s"), duplicateLaunchProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		Now:          func() time.Time { return notifyNow },
	}, notifyNow)
	if len(observation.SourceErrors) != 0 ||
		!observation.Evidence.Known ||
		observation.Evidence.Open != 1 {
		t.Fatalf("gate observation = %+v, want known one durable open gate", observation)
	}
}

func TestInspectGoalSupervisionGatesFailsClosedWhenNamespaceCannotBeScanned(t *testing.T) {
	cfg := team.Team{
		Project: t.TempDir(),
		Operator: &team.OperatorConfig{
			Enabled: true, Handle: team.DefaultOperatorHandle, Participant: true,
		},
	}
	observation := inspectGoalSupervisionGates(
		cfg, team.DefaultProfile, "missing", "", duplicateLaunchProbe{}, notifyNow,
	)
	if observation.Evidence.Known || len(observation.SourceErrors) == 0 {
		t.Fatalf("gate observation = %+v, want unknown with source error", observation)
	}
}

func TestInspectGoalSupervisionGatesMarksMailboxWarningsAmbiguous(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	tornDir := filepath.Join(base, "s", "agents", team.DefaultOperatorHandle, "inbox", "new")
	if err := os.MkdirAll(tornDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tornDir, "torn.md"), []byte("---json\n{\"id\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	observation := inspectGoalSupervisionGates(
		cfg, team.DefaultProfile, "s", filepath.Join(base, "s"),
		duplicateLaunchProbe{
			PIDAlive:     func(int) bool { return true },
			ProcessMatch: func(int, func(string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		notifyNow,
	)
	if !observation.Evidence.Known ||
		!observation.Evidence.Ambiguous ||
		len(observation.SourceErrors) == 0 {
		t.Fatalf("gate observation = %+v, want known but ambiguous with source error", observation)
	}
}

func gotCodes(reasons []GoalSupervisionEligibilityReason) []string {
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}
