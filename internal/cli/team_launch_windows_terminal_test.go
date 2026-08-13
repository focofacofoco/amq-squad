//go:build windows

package cli

import (
	"slices"
	"testing"
)

func TestWindowsTerminalLaunchArgvPreservesTypedCommand(t *testing.T) {
	pane := teamLaunchPane{
		Role:    "qa lead",
		CWD:     `C:\repo with spaces`,
		Program: `C:\Program Files\amq-squad.exe`,
		Args:    []string{"agent", "up", "codex", "--role", "qa lead"},
	}
	got := windowsTerminalLaunchArgv("work stream", pane)

	wantTail := []string{
		"--",
		pane.Program,
		"agent", "up", "codex", "--role", "qa lead",
	}
	if len(got) < len(wantTail) || !slices.Equal(got[len(got)-len(wantTail):], wantTail) {
		t.Fatalf("argv tail = %#v, want %#v", got, wantTail)
	}
	for _, arg := range got {
		if arg == "&&" || arg == "cd" {
			t.Fatalf("Windows Terminal argv contains shell composition token %q: %#v", arg, got)
		}
	}
}

func TestWindowsLiveLaunchDefaults(t *testing.T) {
	if got := defaultLiveLaunchTerminal(); got != "windows-terminal" {
		t.Fatalf("terminal default = %q, want windows-terminal", got)
	}
	if got := defaultLiveLaunchTarget(); got != "new-window" {
		t.Fatalf("target default = %q, want new-window", got)
	}
}
