package cli

import (
	"fmt"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

	found := func(pane string, pid int) func(string) tmuxpane.PaneInspection {
		return func(string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: pane, PID: pid}}
		}
	}
	for _, tc := range []struct {
		name string
		// inspection is what the SHARED resolver reports; dead is the separate #{pane_dead} read.
		inspection func(string) tmuxpane.PaneInspection
		// dead is the #{pane_dead} VALUE; the fake composes the id echo around it.
		dead string
		// echoPane overrides the echoed id, for the vanished-pane substitution case.
		echoPane string
		wantErr  string
	}{
		{
			name:       "empty pid means the command never started",
			inspection: found(requested, 0), dead: "0",
			wantErr: "no running process",
		},
		{
			// The launcher-shell case, now caught INSIDE the shared resolver: it verifies the
			// returned row is the requested pane and reports Gone rather than answering about a
			// different one. Round 3 re-derived this check badly; round 4 delegates it.
			name: "resolver reports the pane gone",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane"}
			},
			dead:    "0\n",
			wantErr: "no longer exists",
		},
		{
			// remain-on-exit keeps a dead pane reporting its last pid, so a pid alone cannot
			// distinguish running from just-exited. This is why pane_dead stays a separate read.
			name:       "dead pane still reports its last pid",
			inspection: found(requested, 4242), dead: "1",
			wantErr: "is dead",
		},
		{
			// UNPROVEN is not absent. A transient iTerm2 -CC pause must fail CLOSED here, and
			// must NOT be reported as Gone -- the distinction is the resolver's whole point.
			name: "unavailable is unproven, not gone",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionUnavailable, Detail: "control mode paused"}
			},
			dead:    "0\n",
			wantErr: "unproven evidence",
		},
		{
			name: "malformed is unproven",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionMalformed, Detail: "empty row"}
			},
			dead:    "0\n",
			wantErr: "unproven evidence",
		},
		{
			name:       "blank liveness field cannot confirm the command is running",
			inspection: found(requested, 4242), dead: "",
			wantErr: "no liveness field",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// F3 routes identity through the shared resolver, so the fake goes on THAT seam.
			// tmuxpane owns its exec seam privately, which is exactly why inspectPaneExact exists.
			oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
			t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
			inspectPaneExact = func(id string) tmuxpane.PaneInspection { return tc.inspection(id) }
			// pane_dead is still read directly, because paneListFormat does not carry it.
			// The echo is composed HERE so each row states only the liveness value it is about.
			// A row carrying the wire format would have to be edited again the next time the query
			// shape changes -- which is exactly what just happened across seven files.
			tmuxOutputCommand = func(_ string, args ...string) (string, error) {
				if tc.echoPane != "" {
					return tc.echoPane + "\t" + tc.dead, nil
				}
				return requested + "\t" + tc.dead, nil
			}

			pid, err := verifyPaneProcessLaunched(requested)
			if err == nil {
				t.Fatalf("case %q was ACCEPTED and returned pid %q; an unproven pane must refuse", tc.name, pid)
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
	oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
	inspectPaneExact = func(string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: "%7", PID: 4242}}
	}
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%7\t0\n", nil }

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
	oldInspect := inspectPaneExact
	t.Cleanup(func() { inspectPaneExact = oldInspect })
	queried := false
	inspectPaneExact = func(string) tmuxpane.PaneInspection {
		queried = true
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: "%1", PID: 4242}}
	}

	if _, err := verifyPaneProcessLaunched("  "); err == nil {
		t.Fatal("an empty pane identity must refuse")
	}
	if queried {
		t.Error("must refuse WITHOUT querying tmux: an empty -t target resolves to the active pane")
	}
}

// #577 round 4: the four cases the review named, table-driven, because the round-3 test could
// not see two of them -- it modelled every --create as adding a pane, so the fixture encoded the
// same assumption as the code it guarded.
//
// The launcher must deliver ONLY to a pane it can attribute to its own --create: novel pane AND
// novel window AND that window named for the role this create targeted. Novelty alone proves
// count, not causality.
func TestTmuxSessionAttributesTheCreatedPaneOrRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		// sessions is the list-sessions reply; err is its error.
		sessions    string
		sessionsErr error
		// before/after are list-panes replies: "<pane> <window> <name>" per line.
		before      string
		after       string
		wantDeliver string // "" = must refuse
		wantErr     string
		// wantNoCreate asserts the refusal happened BEFORE --create. Refusing after creating
		// leaves an abandoned window that the next launch will then have to reason about.
		wantNoCreate bool
	}{
		{
			// F1: a fresh workstream must LAUNCH. The round-3 code refused here, making the
			// backend's first launch structurally impossible.
			//
			// #577 round 5 F4: the evidence here is a SUCCESSFUL list-sessions that does not name
			// this workstream -- real absent-session proof. The earlier version leaned on a
			// successful-EMPTY list-panes instead, so reverting only the vacancy fix stayed green:
			// the row passed for a reason unrelated to what it claimed to test.
			name: "fresh workstream launches", sessions: "other-session\n",
			after: "%5 @9 cto\n", wantDeliver: "%5",
		},
		{
			// F1: a transient probe failure on a POPULATED session must refuse, not be read as
			// fresh. Read-as-fresh is what let an operator's pane look novel and get destroyed.
			//
			// #577 round 5 F4: this row had NO populated pane fixture, so "populated" was in the
			// comment and not in the evidence. before is now a real operator pane, and the row
			// asserts --create was never invoked -- refusing AFTER creating would leave an
			// abandoned window behind.
			name: "transient probe failure on a populated session refuses", sessionsErr: errTmuxProbeFailed,
			before: "%23 @4 cto\n", wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1's real falsifier, per the review: a PERMISSION-phrased error is documented in
			// internal/tmuxpane/tmux.go as meaning the socket is BLOCKED, not that the server is
			// absent. Treating it as vacancy is what could destroy an operator's pane.
			name: "permission-denied error is NOT vacancy", sessionsErr: errTmuxPermissionDenied,
			before: "%23 @4 cto\n", wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F2: the session EXISTS (list-sessions names it) and a SUCCESSFUL list-panes comes
			// back empty. A tmux session cannot have zero panes, so this is broken evidence and
			// must refuse BEFORE --create rather than proceed on a confident empty map.
			name: "existing session with an empty successful read refuses", sessions: "issue-96\n",
			before: "", wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F2: a malformed row must refuse, not be skipped. A row we cannot parse may describe
			// an operator's pane.
			name: "malformed pane row refuses", sessions: "issue-96\n",
			before: "not-a-pane-id @4 cto\n", wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1: no server at all IS provable vacancy -- no server, no panes.
			name: "no server running is proven vacancy", sessionsErr: errNoTmuxServer,
			after: "%5 @9 cto\n", wantDeliver: "%5",
		},
		{
			// F2 case A: --create selected an existing role window and created NOTHING. The
			// round-3 fixture could not express this at all.
			name: "created nothing refuses", sessions: "issue-96\n",
			before: "%23 @4 cto\n", after: "%23 @4 cto\n",
			wantErr: "no new pane in a new window",
		},
		{
			// F2 case B, the interleaving that defeated novelty: --create created nothing AND an
			// operator's pane arrived in a NEW window. Novel pane, novel window, wrong name.
			// Round 3 would have accepted this and killed the operator's pane.
			name: "operator pane in a new window refuses", sessions: "issue-96\n",
			before: "%23 @4 cto\n", after: "%23 @4 cto\n%77 @8 operator-scratch\n",
			wantErr: "no new pane in a new window",
		},
		{
			// The companion: a genuine create IS attributable, so the refusals above are not the
			// function rejecting everything.
			name: "genuine create is attributed", sessions: "issue-96\n",
			before: "%23 @4 qa\n", after: "%23 @4 qa\n%24 @5 cto\n", wantDeliver: "%24",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldOut, oldRun, oldLook, oldSessionRun := tmuxOutputCommand, tmuxRunCommand, tmuxSessionLookPath, tmuxSessionRunCommand
			t.Cleanup(func() {
				tmuxOutputCommand, tmuxRunCommand, tmuxSessionLookPath, tmuxSessionRunCommand = oldOut, oldRun, oldLook, oldSessionRun
			})
			tmuxSessionLookPath = func(string) (string, error) { return "/usr/bin/tmux-session", nil }
			oldInspect := inspectPaneExact
			t.Cleanup(func() { inspectPaneExact = oldInspect })
			inspectPaneExact = func(id string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
			}

			created := false
			tmuxSessionRunCommand = func(name string, args ...string) error {
				if strings.Contains(strings.Join(args, " "), "--create") {
					created = true
				}
				return nil
			}
			tmuxOutputCommand = func(name string, args ...string) (string, error) {
				call := strings.Join(args, " ")
				switch {
				case strings.Contains(call, "list-sessions"):
					// After --create the session EXISTS, even on a fresh workstream -- that is what
					// the wrapper does. Returning the pre-create listing both times made the
					// post-create lookup see a vacant server and refuse a healthy launch: the
					// fixture, not the fix, was wrong.
					if created {
						return "issue-96\n", nil
					}
					return tc.sessions, tc.sessionsErr
				case strings.Contains(call, "list-panes"):
					if created {
						return tc.after, nil
					}
					return tc.before, nil
				case strings.Contains(call, "#{pane_dead}"):
					// Delegated to the shared helper so the liveness wire format lives in ONE place.
					// #577 round 5 restored the id echo here; a local literal would have needed
					// editing in every fixture, which is how seven files drifted at once.
					return fakePaneIdentityReply(args), nil
				}
				return "", fmt.Errorf("unexpected output command: %s %s", name, call)
			}
			var delivered []string
			tmuxRunCommand = func(name string, args ...string) error {
				for i, a := range args {
					if a == "respawn-pane" {
						for j := i; j < len(args)-1; j++ {
							if args[j] == "-t" {
								delivered = append(delivered, args[j+1])
							}
						}
					}
				}
				return nil
			}

			err := runTmuxSessionLaunchPlan(tmuxSessionLaunchPlan{
				Workstream: "issue-96",
				Panes:      []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "agent up"}},
			})

			if tc.wantDeliver == "" {
				if err == nil {
					t.Fatalf("must refuse; delivered to %v", delivered)
				}
				if len(delivered) != 0 {
					t.Fatalf("refused but STILL delivered to %v: respawn-pane -k would destroy a pane this launcher does not own", delivered)
				}
				if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("refusal must say why (%q): %v", tc.wantErr, err)
				}
				if tc.wantNoCreate && created {
					t.Error("refused AFTER invoking --create: an abandoned window is operator litter and the next launch must then reason about it")
				}
				return
			}
			if err != nil {
				t.Fatalf("a launch this launcher can attribute must proceed: %v", err)
			}
			if len(delivered) != 1 || delivered[0] != tc.wantDeliver {
				t.Errorf("delivered to %v, want exactly [%s]", delivered, tc.wantDeliver)
			}
		})
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
	// #577 round 4 split the read: identity comes from the shared resolver via
	// inspectPaneExact, and THIS seam is asked only for #{pane_dead}. Returning the old
	// three-field reply for a pane_dead query made a live pane read as dead, which is how
	// nine tests failed in CI after the focused set passed.
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "#{pane_dead}") {
		// #577 round 5 F3 restored the identity ECHO to the liveness read, so this seam must
		// answer "<pane_id>\t<pane_dead>". Returning a bare "0" would fail the new
		// incomplete-reply check -- the helper has to track the query shape it stands in for.
		target := ""
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				target = args[i+1]
			}
		}
		return target + "\t0\n"
	}
	target := ""
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	return target + "\t4242\t0\n"
}

// stubExactPaneInspection installs a permissive shared-resolver fake: whatever pane is asked
// about is Found, alive, with a fixed pid.
//
// Every launch test needs it now, because #577 round 4 routed pane identity through
// tmuxpane.InspectPaneExactByID, whose own exec seam is package-private -- so a test that
// fakes only tmuxOutputCommand reaches the REAL resolver and gets Unavailable/Malformed.
// Naming it once beats nine copies drifting apart.
func stubExactPaneInspection(t *testing.T) {
	t.Helper()
	old := inspectPaneExact
	t.Cleanup(func() { inspectPaneExact = old })
	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
	}
}

// Distinct sentinels so the table can express "the tmux server is absent" (provable vacancy)
// separately from "the probe failed for some other reason" (a refusal). #577 round 4: conflating
// them is exactly what made a failed probe a licence to destroy an operator's pane.
var (
	errNoTmuxServer    = fmt.Errorf("no server running on /tmp/tmux-501/default")
	errTmuxProbeFailed = fmt.Errorf("tmux: connection interrupted")
	// The phrase internal/tmuxpane/tmux.go documents as PERMISSION DENIED, not absence. My
	// round-4 matcher accepted it as proof of no server.
	errTmuxPermissionDenied = fmt.Errorf("error connecting to /tmp/tmux-501/default (Operation not permitted)")
)

// #577 round 5 F3's falsifier. None of the repaired fixtures modelled the TRANSITION: they all
// return Found plus dead=0, so the deleted identity echo was invisible to every one of them.
//
// Here the resolver reports the requested pane Found, and the liveness read then answers about
// a DIFFERENT pane -- exactly what display-message does when the target vanished, because it
// falls back to the active pane instead of erroring. Without the echo comparison the launcher
// reads the launcher's own pane_dead=0 and counts a fast-exited worker as launched.
func TestVerifyRefusesWhenLivenessAnswersAboutADifferentPane(t *testing.T) {
	oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
	}
	// The fallback: a live, alive pane that is NOT the one we asked about.
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%1\t0\n", nil }

	pid, err := verifyPaneProcessLaunched("%7")
	if err == nil {
		t.Fatalf("liveness answered about %%1 while %%7 was requested and it was ACCEPTED, returning pid %q", pid)
	}
	if !strings.Contains(err.Error(), "DIFFERENT pane") {
		t.Errorf("refusal must name the substitution so the operator knows the pane vanished: %v", err)
	}
	if pid != "" {
		t.Errorf("a refused verification must return no pid; got %q", pid)
	}
}
