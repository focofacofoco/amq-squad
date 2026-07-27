package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(tmuxSessionTeamLaunchBackend{})
}

// tmuxSessionBinary is the user CLI this backend drives. It is a package var
// so tests can assert binary resolution without depending on the real wrapper
// being installed, and so the LIVE path that spawns iTerm2 -CC stays out of CI.
const tmuxSessionBinary = "tmux-session"

// tmuxSessionLookPath resolves the tmux-session wrapper on PATH. Indirected
// through a var so Validate can be unit-tested both ways (present/absent)
// without touching the real PATH or spawning anything.
var tmuxSessionLookPath = exec.LookPath

// tmuxSessionTeamLaunchBackend is the opt-in window-per-agent backend selected
// via `--terminal tmux-session`. Unlike the default tmux backend (which splits
// panes inside one window), it drives the user's `tmux-session` CLI so EACH
// agent lands in its own named iTerm2 window. The LIVE path goes through
// iTerm2 -CC and cannot be verified headlessly; only the emitted command shape
// is unit-tested. The default tmux backend is unchanged and stays the default.
type tmuxSessionTeamLaunchBackend struct{}

// tmuxSessionLaunchPlan is the resolved, backend-agnostic launch description
// the emitter and exec path both consume. Workstream is the AMQ session that
// also names the tmux-session session (one session, one window per agent).
type tmuxSessionLaunchPlan struct {
	Workstream string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

func (tmuxSessionTeamLaunchBackend) Name() string {
	return "tmux-session"
}

// Validate fails fast with an actionable message when the tmux-session wrapper
// is not on PATH, so the operator is told exactly how to recover (install it or
// fall back to the default tmux backend) before any pane work begins.
func (tmuxSessionTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	if _, err := tmuxSessionLookPath(tmuxSessionBinary); err != nil {
		return fmt.Errorf("%s not found on PATH — install it or use --terminal tmux", tmuxSessionBinary)
	}
	return nil
}

func (b tmuxSessionTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTmuxSessionLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b tmuxSessionTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	return runTmuxSessionLaunchPlan(b.buildPlan(t, opts))
}

func (tmuxSessionTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) tmuxSessionLaunchPlan {
	return tmuxSessionLaunchPlan{
		Workstream: opts.Workstream,
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

// tmuxSessionCreateArgv is the PURE per-agent argv for adding (and attaching) a
// named window in the workstream session. Keeping it a standalone function
// means emission is unit-testable without spawning iTerm2: it is the single
// source of truth used by both the dry-run printer and the live exec path.
//
//	tmux-session --session <workstream> --create <role> <cwd>
func tmuxSessionCreateArgv(workstream, role, cwd string) []string {
	return []string{"--session", workstream, "--create", role, cwd}
}

// tmuxSessionRenameArgv is the PURE per-agent argv that stamps the window with
// the deterministic name-first jump token (amq:<session>:<role>) by renaming
// the just-created <role> window. Reusing paneTitleToken keeps tmux pane
// resolver's expectedPaneToken in lockstep across both backends.
//
//	tmux-session --session <workstream> --rename <role> amq:<session>:<role>
func tmuxSessionRenameArgv(workstream, role string) []string {
	return []string{"--session", workstream, "--rename", role, paneTitleToken(workstream, role)}
}

// tmuxSessionResumeArgv is the PURE final argv that resumes/attaches the whole
// session for focus once every agent window exists.
//
//	tmux-session --session <workstream> --resume
func tmuxSessionResumeArgv(workstream string) []string {
	return []string{"--session", workstream, "--resume"}
}

func printTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) {
	fmt.Println("# amq-squad team launch - tmux-session (window per agent)")
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# windows: %d\n\n", len(plan.Panes))
	for _, line := range tmuxSessionDryRunLines(plan) {
		fmt.Println(line)
	}
}

// tmuxSessionDryRunLines renders the faithful, copy-pasteable preview for the
// window-per-agent plan. For each agent it emits, in order: a --create for the
// agent's own named window, a tmux send-keys that types the agent command into
// that window, and a --rename that stamps the amq:<session>:<role> jump token.
// A trailing --resume focuses the session. The emitted lines mirror exactly
// what runTmuxSessionLaunchPlan executes.
func tmuxSessionDryRunLines(plan tmuxSessionLaunchPlan) []string {
	if len(plan.Panes) == 0 {
		return nil
	}
	lines := make([]string, 0, len(plan.Panes)*3+1)
	for i, pane := range plan.Panes {
		lines = append(lines,
			shellCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...),
			tmuxSessionPaneCommandDryRunLine(plan.Workstream, pane.Role, pane.Command),
			shellCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...),
		)
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	lines = append(lines, shellCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...))
	return lines
}

// tmuxSessionPaneCommandDryRunLine targets the agent's own named window by its
// "<session>:<role>" tmux target (the window-per-agent equivalent of the pane-id
// targeting the default backend uses) and makes the agent command that window's
// ROOT PROCESS.
//
// #571: the previous name said SendKeys and the previous comment said "types the
// agent command". Both were still accurate about the mechanism this backend used
// and both became false in the same commit that stopped typing. A name that
// survives the mechanism it names is how the next reader searching for typing
// paths misses this one.
func tmuxSessionPaneCommandDryRunLine(workstream, role, command string) string {
	return tmuxPaneCommandDryRunLine(workstream+":"+role, command)
}

func runTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("tmux-session plan has no panes")
	}
	for i, pane := range plan.Panes {
		// Refuse BEFORE creating, so an occupied window name is reported rather than
		// respawned. Checking after creation cannot distinguish "we just made it" from
		// "it was already there", and the wrong answer costs the operator a live pane.
		if occupant, occupied := tmuxSessionWindowOccupant(plan.Workstream, pane.Role); occupied {
			return fmt.Errorf("refusing to launch %s: window %s:%s already exists (pane %s). Delivering here would KILL whatever runs in it -- an operator window named for this role, or a prior partial launch. Close or rename that window, then relaunch",
				pane.Role, plan.Workstream, pane.Role, occupant)
		}
		if err := runCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...); err != nil {
			return err
		}
		// "new-window" is the correct visibility provenance here: this backend's
		// contract is one named, attached iTerm2 window per agent (tmuxSessionResumeArgv
		// attaches the session for focus), so status maps it to adoption_mode=managed_window
		// (operator-visible), NOT a detached session. Do not remap to managed_session.
		// #577 finding 3, and this is the most dangerous defect #571 introduced.
		// respawn-pane -k DESTROYS whatever occupies the target, and a NAME target
		// ("<session>:<role>") is not proof of what that is. An operator window that
		// happens to be named for the role, or the leftover window of a prior partial
		// launch, would have its live shell or TUI killed. "Delivery cannot lose the
		// command" and "delivery cannot destroy the operator's work" are different
		// properties; the original fix only reasoned about the first.
		//
		// So delivery binds to the EXACT pane identity creation produced, and a window
		// that already exists is refused instead of respawned. Resolving the pane id
		// after creation also means the -k applies to a pane this launcher just made.
		paneID, err := tmuxSessionFreshPaneID(plan.Workstream, pane.Role)
		if err != nil {
			return fmt.Errorf("bind %s to a verified fresh pane: %w", pane.Role, err)
		}
		if err := deliverPaneCommand(paneID, withTmuxTargetEnv("new-window", pane.Command)); err != nil {
			return fmt.Errorf("deliver command for %s: %w", pane.Role, err)
		}
		// Verified counting applies to THIS backend too. #571 landed the pane-pid check
		// on the two default-backend sites and left this one delivering unverified, so
		// "pane creation can no longer count as success" held for two of three paths.
		// This is the operator-visible iTerm2 path, where an unstarted agent is a window
		// sitting at a shell prompt -- the exact failure that read as a successful launch.
		//
		// Placed BEFORE the rename deliberately: tmuxSessionRenameArgv retires the
		// "<session>:<role>" window name in favour of the amq: token, so this target only
		// resolves until that call.
		if _, err := verifyPaneProcessLaunched(paneID); err != nil {
			return fmt.Errorf("worker %s not launched: %w", pane.Role, err)
		}
		if err := runCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if err := runCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...); err != nil {
		return err
	}
	quietNotice("Opened %d agent window(s) in tmux-session %s.\n", len(plan.Panes), shellQuote(plan.Workstream))
	verbosePolicyEcho()
	return nil
}

// tmuxSessionWindowOccupant reports the pane already occupying "<workstream>:<role>", if any.
//
// Deliberately fail-SAFE rather than fail-open: any answer that names a pane counts as
// occupied. A query error means "cannot prove it is free", and since being wrong here kills a
// live pane, an unprovable answer must not be read as vacant.
func tmuxSessionWindowOccupant(workstream, role string) (string, bool) {
	out, err := tmuxOutputCommand("tmux", "list-panes", "-t", workstream+":"+role, "-F", "#{pane_id}")
	if err != nil {
		// tmux errors when the window does not exist, which is the expected free case.
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			return id, true
		}
	}
	return "", false
}

// tmuxSessionFreshPaneID resolves the single pane of the window creation just made.
//
// Exactly one pane is required. Zero means creation did not produce an inspectable pane, and
// more than one means this is not the fresh single-pane window we created -- both are refusals,
// because delivery is about to run respawn-pane -k and must only ever target a pane this
// launcher owns.
func tmuxSessionFreshPaneID(workstream, role string) (string, error) {
	target := workstream + ":" + role
	out, err := tmuxOutputCommand("tmux", "list-panes", "-t", target, "-F", "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("list panes for %s: %w", target, err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("window %s has no inspectable pane after creation: treating zero panes as success is how an unlaunched worker counted as launched", target)
	default:
		return "", fmt.Errorf("window %s has %d panes after creation (%s); a freshly created agent window must have exactly one, so this is not a pane this launcher owns", target, len(ids), strings.Join(ids, " "))
	}
}
