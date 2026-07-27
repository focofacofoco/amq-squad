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
		// rawReply bypasses composition entirely, to model a reply shape production would get
		// from a REGRESSED format rather than one this fixture would ever build.
		rawReply string
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
		{
			// #577 round 6 blocker 2: I DECLARED this falsifier in the round-6 authoring
			// message and never wrote the row. The rawReply field was declared and READ with
			// zero initializers, so the plumbing looked like evidence while proving nothing --
			// the same shape as claiming a test that did not exist, except the scaffolding made
			// it harder to notice.
			//
			// A bare "0" is what production receives if the format regresses to a single
			// field: one value, no identity echo. The row above covers a PRESENT-but-empty
			// second field, which is a different reply and a different guard.
			name: "reply with no echo at all is incomplete", rawReply: "0\n",
			inspection: found(requested, 4242),
			wantErr:    "incomplete liveness reply",
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
			// shape changes. Scope, grep-counted rather than recalled: 14 call sites across 5 files
			// use this helper. An earlier claim said "seven files" because it counted the STRING
			// pane_dead -- two of those files use it as a visibility-problem VALUE, not as a liveness
			// fake. Counting the string is not counting the thing.
			tmuxOutputCommand = func(_ string, args ...string) (string, error) {
				if tc.rawReply != "" {
					return tc.rawReply, nil
				}
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
		// before/after are SEMANTIC pane rows; the fake renders them into whatever format
		// production requests. nil after = the modeled world is unchanged by --create.
		before      []paneRow
		after       []paneRow
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
			after: []paneRow{{"issue-96", "%5", "@9", "cto"}}, wantDeliver: "%5",
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
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1's real falsifier, per the review: a PERMISSION-phrased error is documented in
			// internal/tmuxpane/tmux.go as meaning the socket is BLOCKED, not that the server is
			// absent. Treating it as vacancy is what could destroy an operator's pane.
			name: "permission-denied error is NOT vacancy", sessionsErr: errTmuxPermissionDenied,
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F2: the session EXISTS (list-sessions names it) and a SUCCESSFUL list-panes comes
			// back empty. A tmux session cannot have zero panes, so this is broken evidence and
			// must refuse BEFORE --create rather than proceed on a confident empty map.
			name: "existing session with an empty successful read refuses", sessions: "issue-96\n",
			before: nil, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		// #577 round 6 F3: the malformed-row coverage was SINGLE-CAUSE -- one row with an invalid
		// pane id, so reverting the short-row refusal OR deleting the window-id validation kept it
		// failing for the unrelated pane-id reason. Each mutation now has its own falsifier, with
		// exactly ONE defect per fixture.
		{
			name: "invalid PANE id refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "not-a-pane-id", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			name: "invalid WINDOW id refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "not-a-window-id", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// A SHORT row: fewer fields than the format requested. Its pane and window ids are
			// both valid, so only the short-row refusal can catch it.
			name: "short row refuses", sessions: "issue-96\n",
			before: []paneRow{{Session: "issue-96", Pane: "%23"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// #577 round 6 F1's falsifier: rows from ANOTHER session. Every id is shape-valid, so
			// only the session-name comparison can reject them -- which is the point, because
			// tmux's prefix/glob resolution is what could deliver them.
			name: "rows from a DIFFERENT session refuse", sessions: "issue-96\n",
			before: []paneRow{{"issue-96-old", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1: no server at all IS provable vacancy -- no server, no panes.
			name: "no server running is proven vacancy", sessionsErr: errNoTmuxServer,
			after: []paneRow{{"issue-96", "%5", "@9", "cto"}}, wantDeliver: "%5",
		},
		{
			// F2 case A: --create selected an existing role window and created NOTHING. The
			// round-3 fixture could not express this at all.
			// #577 round 6 blocker 3: this row carried an explicit after in the OLD wire format,
			// which the new parser reads as a wrong-session row -- so the no-new-pane error was
			// unreachable and the row passed for an unrelated reason. Omitting after also exercises
			// the F4 retention fix directly: the modeled world keeps the operator pane.
			name: "created nothing refuses", sessions: "issue-96\n",
			before:  []paneRow{{"issue-96", "%23", "@4", "cto"}},
			wantErr: "no new pane in a new window",
		},
		{
			// F2 case B, the interleaving that defeated novelty: --create created nothing AND an
			// operator's pane arrived in a NEW window. Novel pane, novel window, wrong name.
			// Round 3 would have accepted this and killed the operator's pane.
			name: "operator pane in a new window refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, after: []paneRow{{"issue-96", "%23", "@4", "cto"}, {"issue-96", "%77", "@8", "operator-scratch"}},
			wantErr: "no new pane in a new window",
		},
		{
			// The companion: a genuine create IS attributable, so the refusals above are not the
			// function rejecting everything.
			name: "genuine create is attributed", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "@4", "qa"}}, after: []paneRow{{"issue-96", "%23", "@4", "qa"}, {"issue-96", "%24", "@5", "cto"}}, wantDeliver: "%24",
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
					// #577 round 6 blocker 1: this fake returned canned wire text regardless of the
					// requested -t and -F, so removing the exact "=" target changed nothing and
					// removing #{session_name} still received an invented session field. The fixture
					// was supplying both belts the code had stopped asking for.
					//
					// The target is PINNED: anything other than the exact-match form is an error, so
					// dropping "=" fails every positive row. The rows are SEMANTIC and the wire text is
					// rendered from the requested format, so dropping #{session_name} shifts every
					// column left -- and the row then fails at the SESSION EQUALITY check, because
					// fields[0] holds a pane id rather than the session name. Traced rather than
					// assumed: my earlier comment credited the field-count guard, which is not the
					// branch that actually rejects it.
					// The claim is about the -t VALUE, so check that, not mere token presence:
					// containsArg would pass if "=issue-96" appeared anywhere, including in a format
					// string. Exactly one -t, and its value must be the exact-match form.
					// #577 round 7 F1: the fake ignored the -s SCOPE flag, so deleting it from
					// production stayed green -- while real tmux without -s enumerates only ONE
					// window. An existing role window missing from the before-snapshot could then be
					// attributed to this create and destroyed. Same posture as the =target check:
					// the fake refuses an invocation real tmux would answer differently.
					if !hasFlag(args, "-s") {
						return "", fmt.Errorf("list-panes must pass -s to enumerate the whole session; without it tmux returns one window only: %s", call)
					}
					// Three independent properties, all asserted: exactly ONE -t, it HAS a value, and
					// that value is the exact-match target. Presence is out-of-band (HasValue), so no
					// format or target string can imitate an absent one -- and rejection no longer
					// depends on the exact-value comparison happening to differ from a marker.
					targets := targetArgs(args)
					if len(targets) != 1 || !targets[0].HasValue || targets[0].Value != "=issue-96" {
						return "", fmt.Errorf("list-panes must pass exactly one -t whose value is =issue-96; got %v in %s", targets, call)
					}
					// Count AND validity, decided in ONE place (paneListFormatArg) that the direct
					// test also calls -- so removing the rejection there fails the named test instead
					// of only surfacing as a downstream render failure.
					format, formatErr := paneListFormatArg(args)
					if formatErr != nil {
						return "", fmt.Errorf("%w in %s", formatErr, call)
					}
					rows := tc.before
					// #577 round 6 F4: when after is unset the modeled world must not EMPTY itself
					// post-create. A populated before with no after made the operator pane vanish if
					// production wrongly proceeded, so the fixture flattered the code: the pane it
					// should have protected was no longer there to protect.
					if created && tc.after != nil {
						rows = tc.after
					}
					return renderPaneRows(rows, format), nil
				case strings.Contains(call, "#{pane_dead}"):
					// Delegated to the shared helper so the liveness wire format lives in ONE place.
					// #577 round 5 restored the id echo here; a local literal would have needed
					// editing at all 14 call sites across 5 files, which is how they drifted at once.
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

// fakePaneIdentityReply answers a tmux -p format query by DERIVING the reply from the format
// string production actually sent.
//
// #577 round 6 F2: the previous version manufactured "<pane_id>\t<dead>" regardless of what was
// requested, so reverting production to a bare "#{pane_dead}" -- one field, no identity echo --
// stayed GREEN. The fixture supplied the identity the code had stopped asking for, which made
// the echo test I added in round 5 unable to detect the very regression it exists for.
//
// Deriving from the request means a production format change CHANGES THE REPLY SHAPE, so the
// incomplete-reply and echo-compare guards see it.
func fakePaneIdentityReply(args []string) string {
	target := ""
	format := ""
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			target = args[i+1]
		}
		if strings.Contains(a, "#{") {
			format = a
		}
	}
	if format == "" {
		return ""
	}
	// Emit exactly the fields requested, in the order requested, on the separator requested.
	// Unknown fields render EMPTY, as real tmux does -- modelling anything else would be the
	// fixture-lag disease inverted. That is not a silent skip BECAUSE production refuses on a
	// blank or incomplete reply, guarded by the "blank liveness field" and "reply with no echo
	// at all" rows. Deleting either side breaks this named contract.
	values := map[string]string{
		"#{pane_id}":   target,
		"#{pane_pid}":  "4242",
		"#{pane_dead}": "0",
	}
	parts := strings.Split(format, "\t")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v, ok := values[strings.TrimSpace(part)]; ok {
			out = append(out, v)
			continue
		}
		out = append(out, "")
	}
	return strings.Join(out, "\t") + "\n"
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

// paneRow is a SEMANTIC list-panes row. Tests state what exists; the fake renders whatever
// wire format production asked for.
//
// #577 round 6 blocker 1: canned wire text let a fixture supply columns the code no longer
// requested, so removing #{session_name} from production changed nothing observable. A semantic
// row cannot do that -- if the format omits a field, the rendered row omits it too.
type paneRow struct {
	Session string
	Pane    string
	Window  string
	Name    string
}

// renderPaneRows emits exactly the fields the requested format names, in order, space-separated
// as tmux does for this format.
func renderPaneRows(rows []paneRow, format string) string {
	if format == "" {
		return ""
	}
	var out strings.Builder
	for _, row := range rows {
		values := map[string]string{
			"#{session_name}": row.Session,
			"#{pane_id}":      row.Pane,
			"#{window_id}":    row.Window,
			"#{window_name}":  row.Name,
		}
		parts := strings.Fields(format)
		rendered := make([]string, 0, len(parts))
		for _, part := range parts {
			// Unknown variables render EMPTY, which is what real tmux does. Production's
			// field-count and shape checks are the guards; see the blank/incomplete rows.
			rendered = append(rendered, values[part])
		}
		out.WriteString(strings.Join(rendered, " "))
		out.WriteString("\n")
	}
	return out.String()
}

// flagValue is extraction as OUT-OF-BAND state: the value, plus whether one was present at all.
//
// #577 round 8: the previous version marked a dangling flag with an in-band sentinel STRING. I
// had rejected the empty string precisely to avoid collision and then chose another value from
// the SAME DOMAIN, so the decider could not distinguish a real dangling flag from the legitimate
// (if weird) invocation -F "<dangling flag with no value>", which real tmux accepts. An in-band
// sentinel collides with its domain by construction; no better magic string fixes that.
//
// hasValue answers a question the value cannot: "was there a value?" is not a fact about the
// value, so it does not belong in the value.
type flagValue struct {
	Value    string
	HasValue bool
}

// formatArgs returns every -F occurrence, recording presence separately from content.
//
// #577 round 7 F2: paneListFormatArg returned the FIRST valid -F and silently ignored later
// ones, so a duplicate or dangling second -F stayed green where real tmux rejects the
// invocation. Both -F and -t use the SAME shared representation, flagValue{Value, HasValue}: a
// value-less flag is still recorded, so it counts toward multiplicity, and its absence of a value
// is carried out of band rather than by any in-domain marker. Without counting it, "exactly one"
// would be a claim about value-bearing pairs rather than about flags.
func formatArgs(args []string) []flagValue {
	var formats []flagValue
	for i, a := range args {
		if a != "-F" {
			continue
		}
		if i+1 >= len(args) {
			// Counted, and marked value-less OUT OF BAND so no format string can imitate it.
			formats = append(formats, flagValue{})
			continue
		}
		formats = append(formats, flagValue{Value: args[i+1], HasValue: true})
	}
	return formats
}

// paneListFormatArg is the ONE decision function for -F validity: exactly one flag, carrying a
// value. It returns the format or the reason it is unacceptable.
//
// #577 round 7 re-review: count and validity used to be checked by an INLINE clause in the fake,
// while the direct test exercised only formatArgs. Deleting that inline clause left the test
// green -- it proved the helper emitted a sentinel, not that anything REJECTED one. Plumbing as
// evidence, for the second time tonight.
//
// Factoring the decision here means the fake and the test falsify the SAME function, which is the
// same shared-decider principle as #573's predicate: two places deciding one thing is how they
// come to disagree, and a test that does not call the decider proves nothing about it.
func paneListFormatArg(args []string) (string, error) {
	formats := formatArgs(args)
	if len(formats) != 1 {
		return "", fmt.Errorf("list-panes must pass exactly one -F; got %d", len(formats))
	}
	if !formats[0].HasValue {
		return "", fmt.Errorf("list-panes passed a -F with no value; real tmux rejects the invocation")
	}
	return formats[0].Value, nil
}

// hasFlag reports whether a bare flag is present, for flags that take no value.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// targetArgs returns every value that immediately follows a -t flag.
//
// #577 round 6 re-review B: the previous check asked whether "=issue-96" appeared ANYWHERE in
// the argv, which would also pass if it appeared inside a format string, and said nothing about
// how many -t flags there were. The comment claimed the -t value was pinned; the code pinned a
// substring. Adjacency is the property, so adjacency is what is inspected.
func targetArgs(args []string) []flagValue {
	var targets []flagValue
	for i, a := range args {
		if a != "-t" {
			continue
		}
		if i+1 >= len(args) {
			// A DANGLING -t is still a -t. Appending only when a value follows meant
			// "-t =issue-96 ... -t" produced ONE target and passed, contradicting the claim that
			// exactly one -t is required -- and real tmux rejects the malformed invocation. A
			// value-less flag is recorded with HasValue false, so it COUNTS toward multiplicity
			// and is separately identifiable as absent. Presence is out-of-band on purpose: an
			// in-band marker would collide with legitimate values (#577 r8).
			targets = append(targets, flagValue{})
			continue
		}
		targets = append(targets, flagValue{Value: args[i+1], HasValue: true})
	}
	return targets
}

// #577 round 7 re-review: this test must falsify the DECISION, not the plumbing.
//
// The previous version called formatArgs and asserted a sentinel came back, which stayed green
// if the rejection clause was deleted -- it proved a helper's output, not that anything refused.
// It now calls paneListFormatArg, the same decider the list-panes fake uses, so removing either
// the count check or the !HasValue check inside it fails THIS test by name.
func TestPaneListFormatArgRejectsDanglingAndDuplicateFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			// The case that slipped a count-only guard: ONE flag, no value.
			name: "lone dangling -F", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F"},
			wantErr: "no value",
		},
		{
			name: "duplicate -F", args: []string{"-F", "#{pane_id}", "-F", "#{window_id}"},
			wantErr: "exactly one",
		},
		{
			name: "no -F at all", args: []string{"list-panes", "-s"},
			wantErr: "exactly one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := paneListFormatArg(tc.args)
			if err == nil {
				t.Fatalf("must be rejected; got format %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal must say why (%q): %v", tc.wantErr, err)
			}
		})
	}

	// The companion property: a well-formed single -F is ACCEPTED, so the rejections above
	// cannot pass by refusing everything.
	format, err := paneListFormatArg([]string{"-F", "#{pane_id}"})
	if err != nil || format != "#{pane_id}" {
		t.Errorf("a valid single -F must be accepted unchanged; got %q, %v", format, err)
	}
}

// #577 round 8: the in-band sentinel made this argv indistinguishable from a dangling flag. Real
// tmux accepts it as a literal (if useless) format value, so the decider must too -- an in-band
// marker cannot tell a domain value from an absence, which is why presence moved out of band.
//
// WHAT THIS ROW PROVES, narrowly: it falsifies REINTRODUCTION OF THE OLD SENTINEL specifically.
// It cannot prevent every future magic string, because it names one. The general property is
// carried by the representation itself -- flagValue keeps presence out of the value domain -- and
// a new in-band marker would need its own row. Stated so the row is not mistaken for a guard
// against the whole class.
func TestPaneListFormatArgAcceptsAFormatEqualToTheOldSentinel(t *testing.T) {
	const looksLikeTheOldSentinel = "<dangling flag with no value>"

	got, err := paneListFormatArg([]string{"list-panes", "-s", "-t", "=issue-96", "-F", looksLikeTheOldSentinel})
	if err != nil {
		t.Fatalf("a literal format value must be accepted even when it spells the old sentinel: %v", err)
	}
	if got != looksLikeTheOldSentinel {
		t.Errorf("format must pass through unchanged; got %q", got)
	}
}
