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

// #577 round 2 finding 3: the session backend must never deliver to a pane it did not
// create. Round 1 refused an occupied NAME, then re-resolved the same name after creation --
// reopening the hole, because a same-named window can appear between precheck and lookup.
//
// The property is now stronger than a refusal: delivery targets the pane identified by SET
// DIFFERENCE, so a pre-existing operator pane with the same window name is simply never the
// target. This asserts the operator's pane is untouched, which a refusal-only test could not.
func TestTmuxSessionDeliversOnlyToThePaneItCreated(t *testing.T) {
	oldOut := tmuxOutputCommand
	oldRun := tmuxRunCommand
	oldLook := tmuxSessionLookPath
	t.Cleanup(func() {
		tmuxOutputCommand = oldOut
		tmuxRunCommand = oldRun
		tmuxSessionLookPath = oldLook
	})
	tmuxSessionLookPath = func(string) (string, error) { return "/usr/bin/tmux-session", nil }

	// %23 is an operator's live pane in a window that happens to carry the role name.
	// Creation adds %24. Only %24 may be delivered to.
	created := false
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		if strings.Contains(call, "list-panes") {
			if created {
				return "%23\n%24\n", nil
			}
			return "%23\n", nil
		}
		if strings.Contains(call, "#{pane_pid}") {
			return fakePaneIdentityReply(args), nil
		}
		return "", fmt.Errorf("unexpected output command: %s %s", name, call)
	}
	var respawnTargets []string
	tmuxRunCommand = func(name string, args ...string) error {
		for i, a := range args {
			if a == "respawn-pane" {
				for j := i; j < len(args)-1; j++ {
					if args[j] == "-t" {
						respawnTargets = append(respawnTargets, args[j+1])
					}
				}
			}
		}
		return nil
	}
	oldSessionRun := tmuxSessionRunCommand
	t.Cleanup(func() { tmuxSessionRunCommand = oldSessionRun })
	tmuxSessionRunCommand = func(name string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "--create") {
			created = true
		}
		return nil
	}

	if err := runTmuxSessionLaunchPlan(tmuxSessionLaunchPlan{
		Workstream: "issue-96",
		Panes:      []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "agent up"}},
	}); err != nil {
		t.Fatalf("a creatable window must launch: %v", err)
	}

	if len(respawnTargets) != 1 {
		t.Fatalf("expected exactly one delivery, got %v", respawnTargets)
	}
	if respawnTargets[0] == "%23" {
		t.Fatal("delivered to the PRE-EXISTING pane %23: respawn-pane -k would KILL the operator's live shell")
	}
	if respawnTargets[0] != "%24" {
		t.Errorf("delivery target = %q, want the created pane %%24", respawnTargets[0])
	}
	for _, target := range respawnTargets {
		if !strings.HasPrefix(target, "%") {
			t.Errorf("delivery target %q is not an exact pane id; a name can resolve to someone else's pane", target)
		}
	}
}

// An unreadable pre-creation snapshot must REFUSE, not proceed. Round 1's precheck returned
// vacant on a query error while its own doc comment promised fail-safe; that contradiction is
// the reason this test exists rather than being folded into the one above.
func TestTmuxSessionRefusesWhenItCannotEnumeratePanes(t *testing.T) {
	oldOut := tmuxOutputCommand
	oldRun := tmuxRunCommand
	oldLook := tmuxSessionLookPath
	t.Cleanup(func() {
		tmuxOutputCommand = oldOut
		tmuxRunCommand = oldRun
		tmuxSessionLookPath = oldLook
	})
	tmuxSessionLookPath = func(string) (string, error) { return "/usr/bin/tmux-session", nil }
	tmuxOutputCommand = func(string, ...string) (string, error) {
		return "", fmt.Errorf("tmux: no server running")
	}
	oldSessionRun := tmuxSessionRunCommand
	t.Cleanup(func() { tmuxSessionRunCommand = oldSessionRun })
	createCalled := false
	tmuxSessionRunCommand = func(name string, args ...string) error {
		createCalled = true
		return nil
	}
	defer func() {
		if createCalled {
			t.Error("must refuse BEFORE creating: a window created and then abandoned is operator litter")
		}
	}()
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
		t.Fatal("an unreadable pane snapshot must refuse: it cannot prove the target is not an operator's pane")
	}
	if delivered {
		t.Fatal("delivered despite being unable to enumerate panes")
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
