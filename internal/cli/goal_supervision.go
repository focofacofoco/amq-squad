package cli

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	goalSupervisionAssessmentSchema = 1
	goalSupervisionFreshness        = 30 * time.Second
)

type GoalSupervisionState string

const (
	GoalSupervisionRunning                  GoalSupervisionState = "running"
	GoalSupervisionParkedWaitingAMQ         GoalSupervisionState = "parked_waiting_amq"
	GoalSupervisionNativeGoalPausedEligible GoalSupervisionState = "native_goal_paused_eligible"
	GoalSupervisionNativeGoalBlockedHuman   GoalSupervisionState = "native_goal_blocked_human"
	GoalSupervisionNativeGoalBlockedUnknown GoalSupervisionState = "native_goal_blocked_unknown"
	GoalSupervisionLeadDown                 GoalSupervisionState = "lead_down"
	GoalSupervisionPaneBusyOrUnverified     GoalSupervisionState = "pane_busy_or_unverified"
	GoalSupervisionGoalTerminal             GoalSupervisionState = "goal_terminal"
)

type GoalSupervisionSourceStatus struct {
	Complete bool     `json:"complete"`
	Errors   []string `json:"errors,omitempty"`
}

type GoalSupervisionRuntimeIdentity struct {
	Live        bool `json:"live"`
	PIDLive     bool `json:"pid_live"`
	PaneLive    bool `json:"pane_live"`
	PIDAlive    bool `json:"pid_alive"`
	BinaryMatch bool `json:"binary_match"`
}

type GoalSupervisionPaneIdentity struct {
	Managed    bool   `json:"managed"`
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	Target     string `json:"target,omitempty"`
	BusyKnown  bool   `json:"busy_known"`
	Busy       bool   `json:"busy"`
}

type GoalSupervisionGoalIdentity struct {
	Mode          string `json:"mode,omitempty"`
	NativeGoal    bool   `json:"native_goal"`
	Verified      bool   `json:"verified"`
	Source        string `json:"source,omitempty"`
	DeliveryState string `json:"delivery_state,omitempty"`
	GoalDigest    string `json:"goal_digest,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	BindingDigest string `json:"binding_digest,omitempty"`
	CommandDigest string `json:"command_digest,omitempty"`
}

type GoalSupervisionBinding struct {
	Project               string                         `json:"project"`
	Profile               string                         `json:"profile"`
	Session               string                         `json:"session"`
	NamespaceID           string                         `json:"namespace_id"`
	LeadRole              string                         `json:"lead_role,omitempty"`
	LeadHandle            string                         `json:"lead_handle,omitempty"`
	LaunchID              string                         `json:"launch_id,omitempty"`
	LaunchStartedAt       time.Time                      `json:"launch_started_at,omitempty"`
	LaunchRecordDigest    string                         `json:"launch_record_digest,omitempty"`
	LaunchRecordModTime   int64                          `json:"launch_record_mod_time,omitempty"`
	Runtime               GoalSupervisionRuntimeIdentity `json:"runtime"`
	Pane                  GoalSupervisionPaneIdentity    `json:"pane"`
	Goal                  GoalSupervisionGoalIdentity    `json:"goal"`
	PauseGeneration       string                         `json:"pause_generation,omitempty"`
	PreparedRunGeneration string                         `json:"prepared_run_generation,omitempty"`
	PreparedRunDigest     string                         `json:"prepared_run_digest,omitempty"`
	PreparedLaunchAttempt string                         `json:"prepared_launch_attempt,omitempty"`
	PreparedGoalNamespace string                         `json:"prepared_goal_namespace,omitempty"`
	PreparedGoalDigest    string                         `json:"prepared_goal_digest,omitempty"`
}

type GoalSupervisionBlockerEvidence struct {
	ID               string `json:"id,omitempty"`
	Known            bool   `json:"known"`
	Resolved         bool   `json:"resolved"`
	Detail           string `json:"detail,omitempty"`
	ResolutionDigest string `json:"resolution_digest,omitempty"`
}

type GoalSupervisionGateEvidence struct {
	Known     bool `json:"known"`
	Open      int  `json:"open"`
	Ambiguous bool `json:"ambiguous"`
}

type GoalSupervisionLocalInputEvidence struct {
	Observed    bool   `json:"observed"`
	Kind        string `json:"kind,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Destructive bool   `json:"destructive,omitempty"`
}

type GoalSupervisionInvariantEvidence struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

type GoalSupervisionEligibilityReason struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type GoalSupervisionClaimProjection struct {
	State         string `json:"state"`
	ClaimID       string `json:"claim_id,omitempty"`
	DeliveryStage string `json:"delivery_stage,omitempty"`
	Indeterminate bool   `json:"indeterminate,omitempty"`
}

type GoalSupervisionActions struct {
	Inspect runtimeActionJSON `json:"inspect"`
	Restore runtimeActionJSON `json:"restore"`
	Notify  runtimeActionJSON `json:"notify"`
	Resume  runtimeActionJSON `json:"resume"`
}

// GoalSupervisionAssessment is the versioned, read-only #498 supervision
// object. PR5 consumes this exact binding/fingerprint before it may reserve or
// deliver anything; this type performs no claim, AMQ send, or pane mutation.
type GoalSupervisionAssessment struct {
	SchemaVersion          int                                `json:"schema_version"`
	ObservedAt             time.Time                          `json:"observed_at"`
	FreshUntil             time.Time                          `json:"fresh_until"`
	Fresh                  bool                               `json:"fresh"`
	Fingerprint            string                             `json:"fingerprint"`
	Source                 GoalSupervisionSourceStatus        `json:"source"`
	State                  GoalSupervisionState               `json:"state"`
	Eligible               bool                               `json:"eligible"`
	AutomaticResumeAllowed bool                               `json:"automatic_resume_allowed"`
	AttentionRequired      bool                               `json:"attention_required"`
	Binding                GoalSupervisionBinding             `json:"binding"`
	Blocker                GoalSupervisionBlockerEvidence     `json:"blocker"`
	Gates                  GoalSupervisionGateEvidence        `json:"gates"`
	LocalInput             GoalSupervisionLocalInputEvidence  `json:"local_input"`
	Invariants             GoalSupervisionInvariantEvidence   `json:"invariants"`
	Policy                 team.GoalSupervisionPolicyStatus   `json:"policy"`
	Claim                  GoalSupervisionClaimProjection     `json:"claim"`
	Reasons                []GoalSupervisionEligibilityReason `json:"eligibility_reasons"`
	Actions                GoalSupervisionActions             `json:"actions"`
}

type goalSupervisionAssessmentInput struct {
	ObservedAt       time.Time
	Now              time.Time
	MaxAge           time.Duration
	SourceErrors     []string
	Binding          GoalSupervisionBinding
	GoalStateKnown   bool
	NativePaused     bool
	ParkedWaitingAMQ bool
	GoalTerminal     bool
	Blocker          GoalSupervisionBlockerEvidence
	Gates            GoalSupervisionGateEvidence
	LocalInput       GoalSupervisionLocalInputEvidence
	InvariantErrors  []string
	Policy           team.GoalSupervisionPolicyStatus
	Claim            GoalSupervisionClaimProjection
	ClaimClear       bool
	BudgetAllowed    bool
}

type goalSupervisionGateObservation struct {
	Evidence     GoalSupervisionGateEvidence
	SourceErrors []string
}

func assessGoalSupervision(in goalSupervisionAssessmentInput) GoalSupervisionAssessment {
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	}
	if in.MaxAge <= 0 {
		in.MaxAge = goalSupervisionFreshness
	}
	if in.Now.IsZero() {
		in.Now = in.ObservedAt
	}
	if !in.GoalStateKnown {
		in.SourceErrors = append(in.SourceErrors, "native goal state is unverified")
	}
	if !in.Gates.Known {
		in.SourceErrors = append(in.SourceErrors, "operator gate source is unknown")
	}
	if in.NativePaused && !in.Binding.Pane.BusyKnown {
		in.SourceErrors = append(in.SourceErrors, "pane busy state is unknown")
	}
	if in.NativePaused && !in.Blocker.Known {
		in.SourceErrors = append(in.SourceErrors, "native goal blocker is unknown")
	}
	if in.Claim.Indeterminate {
		in.SourceErrors = append(in.SourceErrors, "claim state is indeterminate")
	}
	in.SourceErrors = stableUniqueStrings(in.SourceErrors)
	in.InvariantErrors = stableUniqueStrings(in.InvariantErrors)
	if in.Claim.State == "" {
		in.Claim.State = "none"
	}
	if in.Policy.Mode == "" {
		in.Policy = team.GoalSupervisionPolicyStatus{
			Mode: team.GoalSupervisionManual, Revision: 1, Source: "compatibility-default",
		}
	}
	a := GoalSupervisionAssessment{
		SchemaVersion: goalSupervisionAssessmentSchema,
		ObservedAt:    in.ObservedAt.UTC(),
		FreshUntil:    in.ObservedAt.UTC().Add(in.MaxAge),
		Fresh:         !in.Now.UTC().Before(in.ObservedAt.UTC()) && !in.Now.UTC().After(in.ObservedAt.UTC().Add(in.MaxAge)),
		Source: GoalSupervisionSourceStatus{
			Complete: len(in.SourceErrors) == 0,
			Errors:   in.SourceErrors,
		},
		Binding:    in.Binding,
		Blocker:    in.Blocker,
		Gates:      in.Gates,
		LocalInput: in.LocalInput,
		Invariants: GoalSupervisionInvariantEvidence{
			OK: len(in.InvariantErrors) == 0, Errors: in.InvariantErrors,
		},
		Policy: in.Policy,
		Claim:  in.Claim,
	}
	a.Reasons = goalSupervisionEligibilityReasons(a, in)
	a.Eligible = allGoalSupervisionReasonsPass(a.Reasons)
	a.State = goalSupervisionStateFor(a, in)
	a.AutomaticResumeAllowed = a.Eligible && a.Policy.Mode == team.GoalSupervisionSafeAuto
	a.AttentionRequired = goalSupervisionNeedsAttention(a.State)
	a.Fingerprint = goalSupervisionFingerprint(a)
	a.Actions = goalSupervisionActions(a)
	return a
}

func goalSupervisionEligibilityReasons(a GoalSupervisionAssessment, in goalSupervisionAssessmentInput) []GoalSupervisionEligibilityReason {
	reason := func(code string, passed bool, detail string) GoalSupervisionEligibilityReason {
		return GoalSupervisionEligibilityReason{Code: code, Passed: passed, Detail: detail}
	}
	b := a.Binding
	return []GoalSupervisionEligibilityReason{
		reason("fresh_assessment", a.Fresh, "assessment must remain inside its freshness window"),
		reason("sources_complete", a.Source.Complete, strings.Join(a.Source.Errors, "; ")),
		reason("exact_namespace", goalSupervisionAllNonBlank(b.Project, b.Profile, b.Session, b.NamespaceID), "project/profile/session/namespace must be exact"),
		reason("exact_lead_identity", goalSupervisionAllNonBlank(b.LeadRole, b.LeadHandle), "lead role and handle must be exact"),
		reason("resumable_lifecycle", !in.ParkedWaitingAMQ && !in.GoalTerminal, "parked and terminal lifecycles are never resume-eligible"),
		reason("native_goal_paused", in.NativePaused && b.Goal.NativeGoal && b.Goal.Verified, "verified blocked native /goal binding required"),
		reason("goal_attempt", b.Goal.Mode == "native_goal_blocked" && goalSupervisionAllNonBlank(b.Goal.Source, b.Goal.DeliveryState, b.Goal.GoalDigest, b.Goal.AttemptID, b.Goal.BindingDigest, b.Goal.CommandDigest), "exact blocked native goal, attempt, binding, delivery, and command digests required"),
		reason("launch_generation", goalSupervisionAllNonBlank(b.LaunchID, b.LaunchRecordDigest) && b.LaunchRecordModTime > 0 && !b.LaunchStartedAt.IsZero(), "launch ID, digest, modtime, and start time required"),
		reason("prepared_run_binding", goalSupervisionAllNonBlank(b.PreparedRunGeneration, b.PreparedRunDigest, b.PreparedLaunchAttempt, b.PreparedGoalNamespace, b.PreparedGoalDigest) && b.PreparedGoalNamespace == b.NamespaceID, "prepared run generation, launch attempt, and exact namespace goal binding required"),
		reason("pause_generation", goalSupervisionAllNonBlank(b.PauseGeneration), "native pause generation required"),
		reason("pane_identity", b.Pane.Managed && goalSupervisionAllNonBlank(b.Pane.PaneID) && b.Runtime.Live && b.Runtime.PaneLive, "exact managed live pane required"),
		reason("pane_idle", b.Pane.BusyKnown && !b.Pane.Busy, "positive idle evidence required"),
		reason("blocker_known", a.Blocker.Known && goalSupervisionAllNonBlank(a.Blocker.ID), "original blocker identity required"),
		reason("blocker_resolved", a.Blocker.Resolved && goalSupervisionAllNonBlank(a.Blocker.ResolutionDigest), "durable blocker-resolution evidence required"),
		reason("gates_known", a.Gates.Known, "gate source must be available"),
		reason("no_open_gate", a.Gates.Known && a.Gates.Open == 0, "operator gates must be closed"),
		reason("no_gate_ambiguity", !a.Gates.Ambiguous, "shadowed or unbound gate evidence is ineligible"),
		reason("no_local_input", !a.LocalInput.Observed, "permission and local-input prompts require a human"),
		reason("invariants_ok", a.Invariants.OK, strings.Join(a.Invariants.Errors, "; ")),
		reason("claim_clear", in.ClaimClear && !a.Claim.Indeterminate, "no prior or indeterminate claim may exist"),
		reason("retry_budget", in.BudgetAllowed, "retry/cooldown/loop budget must remain"),
	}
}

func goalSupervisionAllNonBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func goalSupervisionStateFor(a GoalSupervisionAssessment, in goalSupervisionAssessmentInput) GoalSupervisionState {
	switch {
	case in.GoalTerminal:
		return GoalSupervisionGoalTerminal
	case !a.Binding.Runtime.Live:
		return GoalSupervisionLeadDown
	case in.ParkedWaitingAMQ:
		return GoalSupervisionParkedWaitingAMQ
	case in.GoalStateKnown && !in.NativePaused:
		return GoalSupervisionRunning
	case !in.GoalStateKnown:
		return GoalSupervisionNativeGoalBlockedUnknown
	case !a.Binding.Pane.Managed || !a.Binding.Runtime.PaneLive || !a.Binding.Pane.BusyKnown || a.Binding.Pane.Busy:
		return GoalSupervisionPaneBusyOrUnverified
	case a.LocalInput.Observed || a.Gates.Known && a.Gates.Open > 0:
		return GoalSupervisionNativeGoalBlockedHuman
	case !a.Source.Complete || a.Gates.Ambiguous || !a.Invariants.OK || !a.Blocker.Known:
		return GoalSupervisionNativeGoalBlockedUnknown
	case a.Blocker.Known && !a.Blocker.Resolved:
		return GoalSupervisionNativeGoalBlockedHuman
	case a.Eligible:
		return GoalSupervisionNativeGoalPausedEligible
	default:
		return GoalSupervisionNativeGoalBlockedUnknown
	}
}

func goalSupervisionNeedsAttention(state GoalSupervisionState) bool {
	switch state {
	case GoalSupervisionNativeGoalPausedEligible, GoalSupervisionNativeGoalBlockedHuman,
		GoalSupervisionNativeGoalBlockedUnknown, GoalSupervisionLeadDown,
		GoalSupervisionPaneBusyOrUnverified:
		return true
	default:
		return false
	}
}

func allGoalSupervisionReasonsPass(reasons []GoalSupervisionEligibilityReason) bool {
	if len(reasons) == 0 {
		return false
	}
	for _, reason := range reasons {
		if !reason.Passed {
			return false
		}
	}
	return true
}

func goalSupervisionFingerprint(a GoalSupervisionAssessment) string {
	copy := a
	copy.ObservedAt = time.Time{}
	copy.FreshUntil = time.Time{}
	copy.Fresh = false
	copy.Fingerprint = ""
	copy.Actions = GoalSupervisionActions{}
	return digestJSON(copy)
}

func goalSupervisionActions(a GoalSupervisionAssessment) GoalSupervisionActions {
	scope := runtimeActionScope(a.Binding.Project, a.Binding.Profile, a.Binding.Session)
	namespaceID := a.Binding.NamespaceID
	restoreAvailable := a.State == GoalSupervisionLeadDown &&
		goalSupervisionReasonPassed(a.Reasons, "exact_namespace") &&
		goalSupervisionReasonPassed(a.Reasons, "exact_lead_identity") &&
		!a.Gates.Ambiguous
	resumeAvailable := a.Eligible && a.Policy.Mode != team.GoalSupervisionNotifyOnly
	resumeReason := ""
	switch {
	case !a.Eligible:
		resumeReason = firstFailedGoalSupervisionReason(a.Reasons)
	case a.Policy.Mode == team.GoalSupervisionNotifyOnly:
		resumeReason = "goal supervision policy is notify-only"
	}
	actions := runtimeaction.ApplyCanonical([]runtimeaction.Action{
		{
			Kind: "goal_supervision_inspect", Label: "inspect goal supervision assessment",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad status" + scope + " --json",
			Available: true,
		},
		{
			Kind: "goal_supervision_restore", Label: "restore managed lead",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad resume" + scope + " --exec",
			Mutates: true, NeedsConfirmation: true, Available: restoreAvailable,
			Reason: unavailableUnless(restoreAvailable, "exact unambiguous lead-down scope is required"),
		},
		{
			Kind: "goal_supervision_notify", Label: "preview goal-supervision notification",
			Scope: "session", NamespaceID: namespaceID, Command: "amq-squad notify" + scope + " --dry-run --json",
			Available: a.AttentionRequired,
			Reason:    unavailableUnless(a.AttentionRequired, "assessment does not require attention"),
		},
		{
			Kind: "native_goal_resume", Label: "resume exact native /goal attempt",
			Scope: "agent", NamespaceID: namespaceID, Command: "/goal resume",
			Mutates: true, NeedsConfirmation: a.Policy.Mode != team.GoalSupervisionSafeAuto,
			Available: resumeAvailable, Reason: resumeReason,
		},
	})
	// These two are read-only display actions even though their new kinds are
	// unknown to the legacy runtimeaction classifier.
	actions[0].ActionKind = "display"
	actions[2].ActionKind = "display"
	return GoalSupervisionActions{
		Inspect: actions[0], Restore: actions[1], Notify: actions[2], Resume: actions[3],
	}
}

func goalSupervisionReasonPassed(reasons []GoalSupervisionEligibilityReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return reason.Passed
		}
	}
	return false
}

func runtimeActionScope(project, profile, session string) string {
	return " --project " + shellQuote(project) +
		" --profile " + shellQuote(squadnamespace.NormalizeProfile(profile)) +
		" --session " + shellQuote(session)
}

func unavailableUnless(available bool, reason string) string {
	if available {
		return ""
	}
	return reason
}

func firstFailedGoalSupervisionReason(reasons []GoalSupervisionEligibilityReason) string {
	for _, reason := range reasons {
		if !reason.Passed {
			if reason.Detail != "" {
				return reason.Code + ": " + reason.Detail
			}
			return reason.Code
		}
	}
	return "assessment is ineligible"
}

func stableUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

var goalSupervisionPaneBusy = func(paneID string) (busy bool, known bool) {
	busy, err := tmuxpane.PaneBusy(paneID)
	return busy, err == nil
}

func buildGoalSupervisionAssessment(
	t team.Team,
	profile, session string,
	ns squadnamespace.Ref,
	rows []statusRecord,
	gateObservation goalSupervisionGateObservation,
	invariantErrors []executionInvariantError,
	namespaceConflict *namespaceConflictData,
	probe duplicateLaunchProbe,
	now time.Time,
) GoalSupervisionAssessment {
	profile = squadnamespace.NormalizeProfile(profile)
	bindingData := goalBindingForStatus(ns, newSessionStatusContext(t, profile, session, firstLiveTmuxSession(rows)), rows)
	input := goalSupervisionAssessmentInput{
		ObservedAt:      now.UTC(),
		Now:             now.UTC(),
		Policy:          team.EffectiveGoalSupervisionPolicy(t),
		Claim:           GoalSupervisionClaimProjection{State: "none"},
		ClaimClear:      true,
		BudgetAllowed:   true,
		SourceErrors:    append([]string(nil), gateObservation.SourceErrors...),
		Gates:           gateObservation.Evidence,
		InvariantErrors: goalSupervisionInvariantStrings(invariantErrors),
		Binding: GoalSupervisionBinding{
			Project: t.Project, Profile: profile, Session: session, NamespaceID: ns.ID,
			LeadRole: strings.TrimSpace(t.Lead),
		},
	}
	if namespaceConflict != nil {
		input.SourceErrors = append(input.SourceErrors, "namespace/profile identity is ambiguous")
		input.Gates.Ambiguous = true
	}
	if !t.Orchestrated || input.Binding.LeadRole == "" {
		input.SourceErrors = append(input.SourceErrors, "profile has no configured visible lead")
		return assessGoalSupervision(input)
	}
	leadMember, ok := teamMemberByRole(t, input.Binding.LeadRole)
	if !ok {
		input.SourceErrors = append(input.SourceErrors, "configured lead role does not resolve to one member")
		return assessGoalSupervision(input)
	}
	input.Binding.LeadHandle = strings.TrimSpace(leadMember.Handle)
	if input.Binding.LeadHandle == "" {
		input.Binding.LeadHandle = leadMember.Role
	}
	var leadRows []statusRecord
	for _, row := range rows {
		if row.Role == input.Binding.LeadRole && row.Handle == input.Binding.LeadHandle {
			leadRows = append(leadRows, row)
		}
	}
	if len(leadRows) != 1 {
		input.SourceErrors = append(input.SourceErrors, "visible lead runtime does not resolve to exactly one record")
		return assessGoalSupervision(input)
	}
	row := leadRows[0]
	if row.ClassificationError != "" {
		input.SourceErrors = append(input.SourceErrors, "lead runtime classification: "+row.ClassificationError)
	}
	if !row.liveness.LaunchFound {
		input.SourceErrors = append(input.SourceErrors, "lead launch record is missing")
		return assessGoalSupervision(input)
	}
	rec := row.liveness.LaunchRecord
	if rec.BootstrapExpectation != nil {
		input.Binding.LaunchID = strings.TrimSpace(rec.BootstrapExpectation.LaunchID)
	}
	input.Binding.LaunchStartedAt = rec.StartedAt.UTC()
	input.Binding.PreparedRunGeneration = strings.TrimSpace(rec.PreparedRunGeneration)
	input.Binding.PreparedRunDigest = strings.TrimSpace(rec.PreparedRunDigest)
	input.Binding.PreparedLaunchAttempt = strings.TrimSpace(rec.PreparedRunLaunchAttempt)
	input.Binding.PreparedGoalNamespace = strings.TrimSpace(rec.PreparedRunGoalNamespace)
	input.Binding.PreparedGoalDigest = strings.TrimSpace(rec.PreparedRunGoalDigest)
	digest, modTime, generationErr := readGoalFileGeneration(launch.ExistingPath(row.AgentDir))
	if generationErr != nil {
		input.SourceErrors = append(input.SourceErrors, "capture launch generation: "+generationErr.Error())
	} else {
		input.Binding.LaunchRecordDigest = digest
		input.Binding.LaunchRecordModTime = modTime
	}
	paneID := ""
	if rec.Tmux != nil {
		paneID = strings.TrimSpace(rec.Tmux.PaneID)
		input.Binding.Pane = GoalSupervisionPaneIdentity{
			Managed: paneID != "" && rec.Tmux.Target != "adopted",
			Session: rec.Tmux.Session, WindowID: rec.Tmux.WindowID, WindowName: rec.Tmux.WindowName,
			PaneID: paneID, Target: rec.Tmux.Target,
		}
	}
	runtimeIdentity := classifyLaunchRuntimeIdentity(rec, leadMember.Binary, paneID, launchRuntimeProbeFromDuplicate(probe))
	input.Binding.Runtime = GoalSupervisionRuntimeIdentity{
		Live: runtimeIdentity.Live, PIDLive: runtimeIdentity.PIDLive, PaneLive: runtimeIdentity.PaneLive,
		PIDAlive: runtimeIdentity.PIDAlive, BinaryMatch: runtimeIdentity.BinaryMatch,
	}
	if runtimeIdentity.PaneLive {
		input.Binding.Pane.Busy, input.Binding.Pane.BusyKnown = goalSupervisionPaneBusy(paneID)
	}
	if row.goalBinding != nil {
		input.Binding.Goal = GoalSupervisionGoalIdentity{
			Mode: row.goalBinding.Mode, NativeGoal: row.goalBinding.NativeGoal,
			Verified: bindingData.Verified,
			Source:   row.goalBinding.Source, DeliveryState: row.goalBinding.DeliveryState,
			GoalDigest: digestGoalSupervisionString(row.goalBinding.Goal), AttemptID: strings.TrimSpace(row.goalBinding.AttemptID),
			BindingDigest: digestJSON(*row.goalBinding), CommandDigest: digestGoalSupervisionString(row.goalBinding.Command),
		}
		input.Binding.PauseGeneration = digestJSON(struct {
			Launch  string
			Binding string
			Mode    string
		}{digest, input.Binding.Goal.BindingDigest, row.goalBinding.Mode})
	}
	input.GoalStateKnown = bindingData.Verified
	input.NativePaused = input.GoalStateKnown && bindingData.NativeGoal && bindingData.Mode == "native_goal_blocked"
	if input.NativePaused {
		detail := strings.TrimSpace(bindingData.Detail)
		input.Blocker = GoalSupervisionBlockerEvidence{
			ID: digestGoalSupervisionString(detail), Known: detail != "", Detail: detail,
		}
	}
	if row.LocalInput != nil {
		input.LocalInput = GoalSupervisionLocalInputEvidence{
			Observed: true, Kind: row.LocalInput.Kind, Summary: row.LocalInput.Summary,
			Destructive: row.LocalInput.Destructive,
		}
	}
	if row.Activity != nil && !row.Activity.Stale {
		switch strings.TrimSpace(row.Activity.Phase) {
		case "parked_waiting_amq":
			input.ParkedWaitingAMQ = true
		case "goal_terminal":
			input.GoalTerminal = true
		}
	}
	return assessGoalSupervision(input)
}

func inspectGoalSupervisionGates(
	t team.Team,
	profile, session string,
	observedSessionRoot string,
	probe duplicateLaunchProbe,
	now time.Time,
) goalSupervisionGateObservation {
	operator := team.EffectiveOperator(t)
	if !team.SupportsOperatorGates(t) || !operator.Enabled {
		return goalSupervisionGateObservation{
			Evidence: GoalSupervisionGateEvidence{Known: true},
		}
	}
	ns := squadnamespace.Resolve(t.Project, profile, session)
	root := strings.TrimSpace(observedSessionRoot)
	if root == "" {
		root = strings.TrimSpace(ns.AMQRoot)
	}
	if root == "" {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"operator gate namespace root is unavailable"},
		}
	}
	baseRoot := root
	if squadnamespace.ProfilesEqual(profile, team.DefaultProfile) {
		baseRoot = filepath.Dir(root)
	}
	stateProbe := state.Probe{
		PIDAlive:         probe.PIDAlive,
		ProcessMatch:     probe.ProcessMatch,
		ProcessTTY:       probe.ProcessTTY,
		ProcessStartTime: probe.ProcessStartTime,
		Now:              func() time.Time { return now },
	}
	data, err := buildOperatorStatusData(operatorExecution{
		ProjectDir: t.Project,
		Profile:    profile,
		Session:    session,
		BaseRoot:   baseRoot,
		ReadOnly:   true,
		Probe:      stateProbe,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"inspect durable operator gates: " + err.Error()},
		}
	}
	if data.Operator.Poll == nil {
		return goalSupervisionGateObservation{
			SourceErrors: []string{"durable operator gate projection is unavailable"},
		}
	}
	var sourceErrors []string
	for _, warning := range data.sourceWarnings {
		detail := strings.TrimSpace(warning.Reason)
		if path := strings.TrimSpace(warning.Path); path != "" {
			detail = path + ": " + detail
		}
		if detail == "" {
			detail = "durable operator gate scan reported an unspecified warning"
		}
		sourceErrors = append(sourceErrors, "inspect durable operator gates: "+detail)
	}
	return goalSupervisionGateObservation{
		Evidence: GoalSupervisionGateEvidence{
			Known:     true,
			Open:      data.Operator.Poll.OpenGates,
			Ambiguous: len(sourceErrors) > 0,
		},
		SourceErrors: stableUniqueStrings(sourceErrors),
	}
}

func digestGoalSupervisionString(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return digestBytes([]byte(value))
}

func goalSupervisionInvariantStrings(errors []executionInvariantError) []string {
	values := make([]string, 0, len(errors))
	for _, invariant := range errors {
		code := strings.TrimSpace(invariant.Code)
		message := strings.TrimSpace(invariant.Message)
		switch {
		case code != "" && message != "":
			values = append(values, code+": "+message)
		case code != "":
			values = append(values, code)
		case message != "":
			values = append(values, message)
		}
	}
	return stableUniqueStrings(values)
}
