package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func installActiveGlobalNOC(t *testing.T, root string) globalNOCLaunch {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	id := globalNOCLaunchID(root, now)
	identity := &tmuxpane.PaneIdentity{
		Session: "tmux-main", WindowID: "@9", WindowName: "noc", PaneID: "%90",
	}
	backstop := globalNOCBackstop{IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60}
	launchRecord, err := beginGlobalNOCLaunch(root, id, "codex", "gpt-5", identity, "sha256:bootstrap", backstop, now)
	if err != nil {
		t.Fatalf("begin NOC launch: %v", err)
	}
	if err := transitionGlobalNOCLaunch(root, id, globalNOCLaunchActive, "ready", now.Add(time.Second)); err != nil {
		t.Fatalf("activate NOC launch: %v", err)
	}
	launchRecord.State = globalNOCLaunchActive
	launchRecord.Detail = "ready"
	launchRecord.UpdatedAt = now.Add(time.Second)
	return launchRecord
}

func stubVerifiedGlobalNOCPane(t *testing.T, launchRecord globalNOCLaunch) {
	t.Helper()
	oldCurrent := globalNOCCurrentPaneIdentity
	oldInspector := statusPaneInspector
	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		tmux := launchRecord.Record.Tmux
		return &tmuxpane.PaneIdentity{
			Session: tmux.Session, WindowID: tmux.WindowID,
			WindowName: tmux.WindowName, PaneID: tmux.PaneID,
		}, nil
	}
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: paneTitleToken(launchRecord.Record.Session, launchRecord.Record.Role)}, id == launchRecord.Record.Tmux.PaneID
	}
	t.Cleanup(func() {
		globalNOCCurrentPaneIdentity = oldCurrent
		statusPaneInspector = oldInspector
	})
}

func TestGlobalNOCDetectionRequiresExactStampedCurrentPane(t *testing.T) {
	root := t.TempDir()
	launchRecord := installActiveGlobalNOC(t, root)
	stubVerifiedGlobalNOCPane(t, launchRecord)

	context, err := detectRegisteredGlobalNOC(root)
	if err != nil {
		t.Fatalf("detect registered NOC: %v", err)
	}
	if context == nil || context.Launch.ID != launchRecord.ID {
		t.Fatalf("verified context = %+v", context)
	}

	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@9", PaneID: "%other"}, nil
	}
	context, err = detectRegisteredGlobalNOC(root)
	if err != nil || context != nil {
		t.Fatalf("mismatched current pane context=%+v err=%v", context, err)
	}

	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		tmux := launchRecord.Record.Tmux
		return &tmuxpane.PaneIdentity{Session: tmux.Session, WindowID: tmux.WindowID, PaneID: tmux.PaneID}, nil
	}
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: "unverified"}, true
	}
	context, err = detectRegisteredGlobalNOC(root)
	if err == nil || context != nil || !strings.Contains(err.Error(), "canonical runtime identity is unverified") {
		t.Fatalf("unstamped pane context=%+v err=%v", context, err)
	}
}

func TestGlobalNOCRegistrySupersedesGenerationAndTracksPollingRun(t *testing.T) {
	root := t.TempDir()
	first := installActiveGlobalNOC(t, root)
	now := first.UpdatedAt.Add(time.Second)
	secondID := globalNOCLaunchID(root, now)
	secondIdentity := &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@10", WindowName: "noc-2", PaneID: "%91"}
	backstop := globalNOCBackstop{IntervalSeconds: 45, TimeoutSeconds: 900, MaxTicks: 20}
	oldInspector := statusPaneInspector
	oldExactInspection := globalNOCPaneInspection
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: paneTitleToken(first.Record.Session, first.Record.Role)}, id == first.Record.Tmux.PaneID
	}
	globalNOCPaneInspection = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id}}
	}
	if _, err := beginGlobalNOCLaunch(root, secondID, "claude", "", secondIdentity, "sha256:second", backstop, now); err == nil || !strings.Contains(err.Error(), "cannot be replaced") {
		t.Fatalf("live NOC replacement error = %v", err)
	}
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	globalNOCPaneInspection = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane " + id}
	}
	t.Cleanup(func() {
		statusPaneInspector = oldInspector
		globalNOCPaneInspection = oldExactInspection
	})
	second, err := beginGlobalNOCLaunch(root, secondID, "claude", "", secondIdentity, "sha256:second", backstop, now)
	if err != nil {
		t.Fatalf("begin second NOC: %v", err)
	}
	if err := transitionGlobalNOCLaunch(root, second.ID, globalNOCLaunchActive, "ready", now.Add(time.Second)); err != nil {
		t.Fatalf("activate second NOC: %v", err)
	}
	second.State = globalNOCLaunchActive
	second.UpdatedAt = now.Add(time.Second)
	context := &globalNOCContext{ControlRoot: root, Launch: second}
	run, err := beginGlobalNOCRun(context, filepath.Join(root, "project"), "release", "v2", "cto", "registered_noc_default", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("begin NOC run: %v", err)
	}
	if err := finishGlobalNOCRun(context, run.ID, globalNOCRunPollRequired, "wake unavailable", nil, now.Add(3*time.Second)); err != nil {
		t.Fatalf("finish NOC run: %v", err)
	}
	registry, err := readGlobalNOCRegistry(root)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Launches) != 2 || registry.Launches[0].State != globalNOCLaunchStopped || registry.Launches[1].State != globalNOCLaunchActive {
		t.Fatalf("launch generations = %+v", registry.Launches)
	}
	if len(registry.Runs) != 1 || registry.Runs[0].State != globalNOCRunPollRequired || registry.Runs[0].NOCLaunchID != second.ID {
		t.Fatalf("run registrations = %+v", registry.Runs)
	}
}

func TestGlobalNOCRegistryRejectsSymlinkedMetadataDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	err := writeGlobalNOCRegistry(root, func(*globalNOCRegistry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("symlink registry error = %v", err)
	}
}

func TestResolveGlobalNOCRegistrationPlanDefaultsAndOptOut(t *testing.T) {
	root := t.TempDir()
	launchRecord := installActiveGlobalNOC(t, root)
	stubVerifiedGlobalNOCPane(t, launchRecord)
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	plan, err := resolveGlobalNOCRegistrationPlan("", false, false)
	if err != nil || !plan.Enabled || plan.Strict || plan.Policy != "registered_noc_default" || plan.Context == nil {
		t.Fatalf("default plan=%+v err=%v", plan, err)
	}
	optOut, err := resolveGlobalNOCRegistrationPlan("", false, true)
	if err != nil || !optOut.OptOut || optOut.Enabled || optOut.Context == nil {
		t.Fatalf("opt-out plan=%+v err=%v", optOut, err)
	}
	explicit, err := resolveGlobalNOCRegistrationPlan("control", true, false)
	if err != nil || !explicit.Enabled || !explicit.Strict || explicit.Handle != "control" || explicit.Policy != "explicit" {
		t.Fatalf("explicit plan=%+v err=%v", explicit, err)
	}
	if _, err := resolveGlobalNOCRegistrationPlan("control", true, true); err == nil {
		t.Fatal("conflicting registration flags were accepted")
	}
}

func TestRunStartWizardPrefillPreservesNOCRegistrationChoice(t *testing.T) {
	spec, err := parseRunStartWizardPrefill([]string{"--project", t.TempDir(), "--register-orchestrator"})
	if err != nil {
		t.Fatalf("parse explicit registration prefill: %v", err)
	}
	if spec.RegisterOrchestrator != defaultGoalOrchestratorHandle || spec.NoRegisterOrchestrator {
		t.Fatalf("explicit prefill = %+v", spec)
	}
	live := strings.Join(preparedRunStartLaunchArgs(spec), " ")
	if !strings.Contains(live, "--register-orchestrator "+defaultGoalOrchestratorHandle) {
		t.Fatalf("live args lost registration: %s", live)
	}

	spec, err = parseRunStartWizardPrefill([]string{"--project", t.TempDir(), "--no-register-orchestrator"})
	if err != nil {
		t.Fatalf("parse opt-out prefill: %v", err)
	}
	if !spec.NoRegisterOrchestrator || spec.RegisterOrchestrator != "" {
		t.Fatalf("opt-out prefill = %+v", spec)
	}
	if !strings.Contains(strings.Join(spec.Args(), " "), "--no-register-orchestrator") {
		t.Fatalf("canonical args lost opt-out: %v", spec.Args())
	}
}

func TestApplyGoalOrchestratorRegistrationBestEffortFailsToPolling(t *testing.T) {
	oldRegistrar := goalOrchestratorRegistrar
	wantErr := errors.New("wake unavailable")
	var captured *launch.OrchestratorRegistration
	goalOrchestratorRegistrar = func(_ goalDeliveryOptions, _ string, _ string, _ bool, provenance *launch.OrchestratorRegistration) (*launch.OrchestratorRegistration, error) {
		captured = provenance
		return nil, wantErr
	}
	t.Cleanup(func() { goalOrchestratorRegistrar = oldRegistrar })
	var result goalOrchestratorRegistrationResult
	_, stderr, err := captureOutput(t, func() error {
		return applyGoalOrchestratorRegistration(goalDeliveryOptions{}, goalOrchestratorRegistrationRequest{
			Enabled: true, BestEffort: true, Handle: "orchestrator",
			Policy: "registered_noc_default", NOCControlRoot: "/control",
			NOCLaunchID: "noc-1", NOCGeneration: 3, NOCRunRegistrationID: "run-1",
			ResultSink: func(got goalOrchestratorRegistrationResult) { result = got },
		}, "", false)
	})
	if err != nil {
		t.Fatalf("best-effort registration returned error: %v", err)
	}
	if !errors.Is(result.Err, wantErr) || captured == nil || captured.NOCLaunchID != "noc-1" || captured.NOCGeneration != 3 || captured.NOCRunRegistrationID != "run-1" {
		t.Fatalf("result=%+v provenance=%+v", result, captured)
	}
	if !strings.Contains(stderr, "poll_required") {
		t.Fatalf("degradation warning missing:\n%s", stderr)
	}

	if err := applyGoalOrchestratorRegistration(goalDeliveryOptions{}, goalOrchestratorRegistrationRequest{
		Enabled: true, Handle: "orchestrator", Policy: "explicit",
	}, "", false); !errors.Is(err, wantErr) {
		t.Fatalf("strict registration error=%v, want %v", err, wantErr)
	}
}

func TestGlobalNOCBootstrapPinsContractAndBounds(t *testing.T) {
	got := buildGlobalNOCBootstrap("/control", "noc-1", "/control/.amq-squad/noc/registry.json", globalNOCBackstop{
		IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60,
	})
	for _, want := range []string{
		"Step 1", "Step 2", "Step 3", "Board protocol",
		"--no-register-orchestrator", "poll_required",
		"interval=30s timeout=1800s max_ticks=60",
		"never author an unbounded watch or polling loop",
		"never answer or clear a gate",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bootstrap missing %q:\n%s", want, got)
		}
	}
}
