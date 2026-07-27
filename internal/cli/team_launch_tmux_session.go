package cli

import (
	"fmt"
	"os/exec"
	"sort"
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

// tmuxSessionRunCommand is the seam for this backend's calls to the tmux-session wrapper CLI.
//
// Added in #577 round 3 because the execution path had NO test coverage: every test stopped at
// the dry-run or at a refusal that returned before creation. Both round-1 and round-2 safety
// findings in this file were in code no test could reach, which is not a coincidence.
var tmuxSessionRunCommand = runCommand

func runTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("tmux-session plan has no panes")
	}
	for i, pane := range plan.Panes {
		// Snapshot the session's panes BEFORE creating, so the pane this launcher causes can
		// be identified by difference rather than by name. An unreadable snapshot is a refusal:
		// delivery replaces a pane's process, so an unverifiable target may be an operator's.
		before, snapshotOK := tmuxSessionPaneIDs(plan.Workstream)
		if !snapshotOK {
			return fmt.Errorf("refusing to launch %s: cannot enumerate existing panes in session %s, so a freshly created pane cannot be told apart from one already in use. Delivery replaces a pane's process and would risk an operator's window",
				pane.Role, plan.Workstream)
		}
		if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...); err != nil {
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
		paneID, err := tmuxSessionCreatedPaneID(plan.Workstream, pane.Role, before)
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
		if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...); err != nil {
		return err
	}
	quietNotice("Opened %d agent window(s) in tmux-session %s.\n", len(plan.Panes), shellQuote(plan.Workstream))
	verbosePolicyEcho()
	return nil
}

// tmuxSessionPaneIDs lists every pane id in the session, or reports that it could not.
//
// The bool is "this answer is trustworthy", NOT "panes exist". #577 round 2 finding 3: the
// previous precheck returned VACANT on a query error while its own doc comment promised
// fail-safe. A function whose documentation asserts the opposite of its behaviour is worse
// than an undocumented one, because the reviewer reads the promise.
func tmuxSessionPaneIDs(workstream string) (map[string]bool, bool) {
	out, err := tmuxOutputCommand("tmux", "list-panes", "-s", "-t", workstream, "-F", "#{pane_id}")
	if err != nil {
		// Cannot prove anything. The caller must treat this as unsafe, never as vacant.
		return nil, false
	}
	ids := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids[id] = true
		}
	}
	return ids, true
}

// tmuxSessionCreatedPaneID identifies the pane creation just made by SET DIFFERENCE against a
// snapshot taken before creation.
//
// #577 round 2 finding 3: re-resolving "<workstream>:<role>" by NAME after creation reopened
// the very window the precheck was meant to close. Between precheck and lookup, a same-named
// window can appear -- from an operator or a racing launch -- and the name then resolves to
// THEIR pane, which respawn-pane -k proceeds to kill. A name is not an identity, and checking
// the name twice does not make it one.
//
// The difference is name-independent: whatever appeared, the pane this launcher caused is the
// one that was not there before. Exactly one new pane is required; zero or several means the
// launcher cannot say which pane it owns, and it must not guess when the next step destroys
// the target.
func tmuxSessionCreatedPaneID(workstream, role string, before map[string]bool) (string, error) {
	after, ok := tmuxSessionPaneIDs(workstream)
	if !ok {
		return "", fmt.Errorf("cannot enumerate panes in session %s after creating the %s window: refusing to deliver, because the next step replaces a pane's process and an unverified target may be an operator's", workstream, role)
	}
	var created []string
	for id := range after {
		if !before[id] {
			created = append(created, id)
		}
	}
	sort.Strings(created)
	switch len(created) {
	case 1:
		return created[0], nil
	case 0:
		return "", fmt.Errorf("creating the %s window in session %s produced no new pane: treating that as success is how an unlaunched worker counted as launched", role, workstream)
	default:
		return "", fmt.Errorf("creating the %s window in session %s produced %d new panes (%s); the launcher cannot identify which pane it owns, and delivery would replace one of them", role, workstream, len(created), strings.Join(created, " "))
	}
}
