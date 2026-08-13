//go:build windows

package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(windowsTerminalTeamLaunchBackend{})
}

type windowsTerminalTeamLaunchBackend struct{}

type windowsTerminalLaunchPlan struct {
	Workstream string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

func (windowsTerminalTeamLaunchBackend) Name() string { return "windows-terminal" }

func (windowsTerminalTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Target != "new-window" {
		return fmt.Errorf("--terminal windows-terminal supports --target new-window; got %q", opts.Target)
	}
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	if _, err := exec.LookPath("wt.exe"); err != nil {
		return fmt.Errorf("wt.exe not found on PATH: install Windows Terminal")
	}
	return nil
}

func (b windowsTerminalTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	plan := b.buildPlan(t, opts)
	fmt.Println("# amq-squad team launch - Windows Terminal")
	fmt.Printf("# target: new-window\n# workstream: %s\n# tabs: %d\n\n", plan.Workstream, len(plan.Panes))
	for i, pane := range plan.Panes {
		fmt.Println(windowsCommandPreview("wt.exe", windowsTerminalLaunchArgv(plan.Workstream, pane)...))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			fmt.Printf("# wait %s\n", plan.StartDelay)
		}
	}
	return nil
}

func (b windowsTerminalTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	plan := b.buildPlan(t, opts)
	if len(plan.Panes) == 0 {
		return fmt.Errorf("Windows Terminal plan has no tabs")
	}
	for i, pane := range plan.Panes {
		if strings.TrimSpace(pane.Program) == "" {
			return fmt.Errorf("Windows Terminal plan for %s has no program", pane.Role)
		}
		if err := runCommand("wt.exe", windowsTerminalLaunchArgv(plan.Workstream, pane)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	quietNotice("Opened %d Windows Terminal tab(s) for %s.\n", len(plan.Panes), plan.Workstream)
	verbosePolicyEcho()
	return nil
}

func (windowsTerminalTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) windowsTerminalLaunchPlan {
	return windowsTerminalLaunchPlan{
		Workstream: opts.Workstream,
		Panes:      resolvedTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

func windowsTerminalLaunchArgv(workstream string, pane teamLaunchPane) []string {
	windowName := nativeTerminalWindowName(workstream, "team")
	tabName := nativeTerminalWindowName(workstream, pane.Role)
	args := []string{
		"-w", windowName,
		"new-tab",
		"--title", tabName,
		"--suppressApplicationTitle",
		"--startingDirectory", pane.CWD,
		"--",
		pane.Program,
	}
	return append(args, pane.Args...)
}

func windowsCommandPreview(program string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(program))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}
