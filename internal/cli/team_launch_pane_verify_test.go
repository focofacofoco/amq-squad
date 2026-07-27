package cli

import (
	"fmt"
	"strings"
	"testing"
)

// #577 finding 5: the #571 commit message and PR body both claimed the empty-pid refusal
// "keeps its own dedicated test rather than being inferred from these". IT DID NOT EXIST.
// Every fake returned 4242, so empty, zero, dead, fallback and attribution inputs were all
// untested while the PR asserted otherwise. Claiming a test exists is worse than the missing
// coverage, because it invites a reviewer to trust a check that is not there.
//
// This is that test, written against every input the reviewer named.
func TestVerifyPaneProcessLaunchedRefusesEveryUnprovenPane(t *testing.T) {
	const requested = "%7"

	for _, tc := range []struct {
		name string
		// reply is what tmux answers for "#{pane_id}\t#{pane_pid}\t#{pane_dead}"
		reply   string
		err     error
		wantErr string
	}{
		{
			name:    "empty pid means the command never started",
			reply:   requested + "\t\t0",
			wantErr: "no running process",
		},
		{
			name:    "zero pid is not a process",
			reply:   requested + "\t0\t0",
			wantErr: "no running process",
		},
		{
			// The launcher-shell case. tmux resolves an unresolvable target to the ACTIVE
			// pane, so a fast-exiting worker yields a real, live pid belonging to the
			// launching shell. The old check accepted it and reported the worker launched.
			name:    "missing-target fallback answers about a different pane",
			reply:   "%1\t99999\t0",
			wantErr: "DIFFERENT pane",
		},
		{
			// remain-on-exit retains the pane and its last pid, so a pid alone cannot
			// distinguish running from just-exited.
			name:    "dead pane still reports its last pid",
			reply:   requested + "\t4242\t1",
			wantErr: "is dead",
		},
		{
			name:    "incomplete reply must not be parsed as a pid",
			reply:   requested + "\t4242",
			wantErr: "incomplete identity",
		},
		{
			name:    "tmux query failure is never success",
			err:     fmt.Errorf("no such pane"),
			wantErr: "read pane process id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := tmuxOutputCommand
			t.Cleanup(func() { tmuxOutputCommand = old })
			tmuxOutputCommand = func(string, ...string) (string, error) {
				if tc.err != nil {
					return "", tc.err
				}
				return tc.reply, nil
			}

			pid, err := verifyPaneProcessLaunched(requested)
			if err == nil {
				t.Fatalf("reply %q was ACCEPTED and returned pid %q; an unproven pane must refuse", tc.reply, pid)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal must name the reason %q so the operator knows what failed; got: %v", tc.wantErr, err)
			}
			if pid != "" {
				t.Errorf("a refused verification must return no pid; got %q", pid)
			}
		})
	}
}

// The companion property, asserted directly rather than inferred from the refusals above: a
// pane that IS the requested one, alive, with a real pid, is accepted. Without this the
// refusal table could all pass while the function rejected everything.
func TestVerifyPaneProcessLaunchedAcceptsTheRequestedLivePane(t *testing.T) {
	old := tmuxOutputCommand
	t.Cleanup(func() { tmuxOutputCommand = old })
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%7\t4242\t0", nil }

	pid, err := verifyPaneProcessLaunched("%7")
	if err != nil {
		t.Fatalf("the requested live pane must verify: %v", err)
	}
	if pid != "4242" {
		t.Errorf("pid = %q, want 4242", pid)
	}
}

// An empty pane identity must refuse rather than ask tmux about nothing. tmux given an empty
// -t target resolves to the ACTIVE pane, which is the launcher -- so a missing identity would
// otherwise verify the launcher's own shell and call the worker launched.
func TestVerifyPaneProcessLaunchedRefusesAnEmptyIdentity(t *testing.T) {
	old := tmuxOutputCommand
	t.Cleanup(func() { tmuxOutputCommand = old })
	queried := false
	tmuxOutputCommand = func(string, ...string) (string, error) {
		queried = true
		return "%1\t4242\t0", nil
	}

	if _, err := verifyPaneProcessLaunched("  "); err == nil {
		t.Fatal("an empty pane identity must refuse")
	}
	if queried {
		t.Error("must refuse WITHOUT querying tmux: an empty -t target resolves to the active pane")
	}
}

// #577 finding 3: the session backend must refuse a window whose name is already occupied,
// because delivery runs respawn-pane -k and would kill whatever is in it -- an operator's
// window named for the role, or a prior partial launch.
func TestTmuxSessionRefusesAnAlreadyOccupiedWindow(t *testing.T) {
	oldOut := tmuxOutputCommand
	oldRun := tmuxRunCommand
	oldLook := tmuxSessionLookPath
	t.Cleanup(func() {
		tmuxOutputCommand = oldOut
		tmuxRunCommand = oldRun
		tmuxSessionLookPath = oldLook
	})
	tmuxSessionLookPath = func(string) (string, error) { return "/usr/bin/tmux-session", nil }
	// The window already has a pane: an operator is working in it.
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%23\n", nil }
	delivered := false
	tmuxRunCommand = func(name string, args ...string) error {
		for _, a := range args {
			if a == "respawn-pane" {
				delivered = true
			}
		}
		return nil
	}

	err := runTmuxSessionLaunchPlan(tmuxSessionLaunchPlan{
		Workstream: "issue-96",
		Panes:      []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "agent up"}},
	})
	if err == nil {
		t.Fatal("an occupied window must be refused, not respawned")
	}
	if delivered {
		t.Fatal("respawn-pane was issued against an occupied window: this KILLS the operator's live pane")
	}
	for _, want := range []string{"already exists", "%23", "KILL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must say what is at risk and where; missing %q in: %v", want, err)
		}
	}
}

// #577 finding 4: respawn-pane runs the command under a fresh non-interactive shell, so the
// operator's profile PATH is absent. The delivered command must carry the launcher's PATH
// explicitly, because a version-managed or ~/.local/bin amq-squad is otherwise unresolvable.
//
// Asserted on the real wrapper, with a PATH value that could only have come from the
// launcher's environment.
func TestDeliveredCommandCarriesTheLauncherPATH(t *testing.T) {
	t.Setenv("PATH", "/opt/version-manager/bin:/usr/bin")

	got := withTmuxTargetEnv("%9", "cd /repo && amq-squad agent up codex")

	if !strings.Contains(got, "PATH=") {
		t.Fatalf("delivered command must export PATH; respawn-pane inherits only tmux env:\n%s", got)
	}
	if !strings.Contains(got, "/opt/version-manager/bin") {
		t.Errorf("must carry the LAUNCHER's PATH, not a default:\n%s", got)
	}
	// The export must precede the command, or the binary resolves before PATH is set.
	if strings.Index(got, "PATH=") > strings.Index(got, "amq-squad agent up") {
		t.Errorf("PATH export must come BEFORE the command:\n%s", got)
	}
}

// fakePaneIdentityReply answers a #{pane_id}\t#{pane_pid}\t#{pane_dead} query the way a
// healthy tmux would: ECHOING BACK the pane that was asked about.
//
// Shared by every fake deliberately. A fake that returns a fixed pane id would pass the
// identity check only by luck, and worse, a fake that returns the WRONG id would make the
// launcher's fallback detection look broken when it is working. Echoing the -t argument is
// the only reply that models "the requested pane exists and is alive" without encoding a
// specific pane into the harness.
func fakePaneIdentityReply(args []string) string {
	target := ""
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	return target + "\t4242\t0\n"
}
