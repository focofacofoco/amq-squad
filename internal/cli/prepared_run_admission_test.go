package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// #573: `team resume` previewed prepared and staged actors as will-launch and emitted a
// command `agent up` then REFUSED, because admission required a spawn reservation the preview
// knew nothing about. The condition was inlined at five sites and the planner was not one of
// them.
//
// This table asserts the VERDICTS. The structural test below asserts that both surfaces
// actually consult this predicate rather than agreeing by coincidence. Neither alone is
// sufficient: correct verdicts in a predicate nobody calls fixes nothing, and a shared call
// whose verdicts are wrong is worse than none.
func TestPreparedRunActorAdmissionClassifiesEveryState(t *testing.T) {
	// GoalNamespace and GoalDigest are REQUIRED here, not decoration: #579 round 3 F1 makes
	// Bindable depend on the record's token AGREEING with the accepted generation, and
	// preparedRunTokenFromSnapshot derives that generation from these fields. Omitting them made
	// the fixture unable to model agreement at all -- every bindable row failed, and it would have
	// been easy to misread that as the F1 fix being wrong rather than the fixture being incomplete.
	manifest := preparedRunManifest{
		Generation:    "g-7",
		Project:       "/repo",
		Profile:       "squad",
		Session:       "v2-25-0",
		GoalNamespace: "squad/v2-25-0",
		GoalDigest:    "gd-7",
		StagedRoster:  []string{"qa"},
	}
	// A record the managed path can ACTUALLY bind: complete generation AND the reserved launch
	// attempt. #579 finding 1 -- the attempt was missing from this fixture and from the
	// predicate, so a record like this previewed bindable and managed resume then refused it.
	bound := &launch.Record{
		PreparedRunGeneration:    "g-7",
		PreparedRunDigest:        "d-7",
		PreparedRunGoalNamespace: "squad/v2-25-0",
		PreparedRunGoalDigest:    "gd-7",
		PreparedRunLaunchAttempt: "a-1",
	}
	unbound := &launch.Record{}

	for _, tc := range []struct {
		name         string
		prepared     bool
		role         string
		rec          *launch.Record
		wantRequired bool
		wantStaged   bool
		wantBindable bool
		wantRecovery string
	}{
		{
			name: "not a prepared session at all", prepared: false, role: "qa", rec: nil,
			wantRequired: false,
		},
		{
			name: "staged actor with no record", prepared: true, role: "qa", rec: nil,
			wantRequired: true, wantStaged: true, wantRecovery: "--staged-spawn",
		},
		{
			name: "prepared non-staged actor with no record", prepared: true, role: "cto", rec: nil,
			wantRequired: true, wantStaged: false, wantRecovery: "run start",
		},
		{
			name: "staged actor whose record carries no token", prepared: true, role: "qa", rec: unbound,
			wantRequired: true, wantStaged: true, wantRecovery: "--staged-spawn",
		},
		{
			// The row that makes the predicate record-aware. --exec CAN bind this through the
			// managed restore path, so blocking it would refuse something that works.
			name: "staged actor whose record carries a complete claim-bound token", prepared: true, role: "qa", rec: bound,
			wantRequired: true, wantStaged: true, wantBindable: true, wantRecovery: "agent resume",
		},
		{
			name: "prepared actor whose record carries a complete token", prepared: true, role: "cto", rec: bound,
			wantRequired: true, wantStaged: false, wantBindable: true, wantRecovery: "agent resume",
		},
		// #579 finding 6: the rows below were all missing, and every one of them is a state the
		// predicate can actually see. The table was six happy-ish rows.
		{
			// finding 1: complete generation, NO reserved attempt. Previewed will-launch and
			// managed resume then refused it.
			name: "complete generation but no reserved attempt is NOT bindable", prepared: true, role: "qa",
			rec:          &launch.Record{PreparedRunGeneration: "g-7", PreparedRunDigest: "d-7", PreparedRunGoalNamespace: "squad/v2-25-0", PreparedRunGoalDigest: "gd-7"},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			name: "partial token is not bindable", prepared: true, role: "qa",
			rec:          &launch.Record{PreparedRunGeneration: "g-7", PreparedRunLaunchAttempt: "a-1"},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			// finding 2: contradictory evidence -- a token-bearing record in a session with no
			// accepted generation. Must be governed, never a plain launch.
			name: "token-bearing record in an UNPREPARED session is governed", prepared: false, role: "qa", rec: bound,
			wantRequired: true, wantStaged: false, wantBindable: false, wantRecovery: "run start",
		},
		// #579 round 3. Each row below is the FALSIFYING INPUT for a claim my round-2 commit
		// message made and my round-2 checks could not disprove, because those checks confirmed a
		// line existed rather than testing what would make the sentence false.
		{
			// F1's falsifying input: a COMPLETE, claim-bound token from a SUPERSEDED generation.
			// Shape-perfect, agreement-absent. Round 2 called this bindable and offered
			// `agent resume <role>`; exec-side validation refuses it.
			name: "stale-generation token is NOT bindable", prepared: true, role: "qa",
			rec: &launch.Record{
				PreparedRunGeneration: "g-OLD", PreparedRunDigest: "d-OLD",
				PreparedRunGoalNamespace: "squad/v2-25-0", PreparedRunGoalDigest: "gd-OLD",
				PreparedRunLaunchAttempt: "a-1",
			},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			// F2's falsifying input: ONLY an orphaned launch attempt. token.empty() is TRUE here --
			// it checks only the four generation fields -- so round 2 let this fall through as an
			// ordinary actor in an unprepared session.
			name: "attempt-only record in an UNPREPARED session is governed", prepared: false, role: "qa",
			rec:          &launch.Record{PreparedRunLaunchAttempt: "a-orphan"},
			wantRequired: true, wantStaged: false, wantBindable: false, wantRecovery: "run start",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adm := preparedRunActorAdmission(manifest, "d-7", tc.prepared, tc.role, "handle-"+tc.role, tc.rec)

			if adm.required() != tc.wantRequired {
				t.Errorf("required() = %v, want %v", adm.required(), tc.wantRequired)
			}
			if adm.Staged != tc.wantStaged {
				t.Errorf("Staged = %v, want %v", adm.Staged, tc.wantStaged)
			}
			if adm.Bindable != tc.wantBindable {
				t.Errorf("Bindable = %v, want %v", adm.Bindable, tc.wantBindable)
			}
			if !tc.wantRequired {
				// A non-prepared session must not produce refusal text at all; an unused
				// Reason string is how a caller ends up rendering a refusal for a fine actor.
				if adm.Reason != "" || adm.Recovery != "" {
					t.Errorf("a non-required verdict must carry no Reason/Recovery; got %q / %q", adm.Reason, adm.Recovery)
				}
				return
			}
			// The reason must name the ACTOR, so an operator with several blocked members can
			// tell which row is which.
			if !strings.Contains(adm.Reason, tc.role) {
				t.Errorf("Reason must name the role %q: %s", tc.role, adm.Reason)
			}
			if tc.wantStaged && !strings.Contains(adm.Reason, manifest.Generation) {
				t.Errorf("a staged refusal must name the generation whose reservation is required: %s", adm.Reason)
			}
			if !strings.Contains(adm.Recovery, tc.wantRecovery) {
				t.Errorf("Recovery must offer %q; got %q", tc.wantRecovery, adm.Recovery)
			}
			// The recovery command must be runnable, not a description of one.
			if !strings.HasPrefix(adm.Recovery, "amq-squad ") {
				t.Errorf("Recovery must be an exact command starting with amq-squad; got %q", adm.Recovery)
			}
			// #579 round 3 fold-in: a BLANK field must render as a visible placeholder, never as
			// '' -- an empty-quoted flag looks filled and fails at runtime.
			if strings.Contains(adm.Recovery, "''") {
				t.Errorf("Recovery contains an empty-quoted argument, which looks filled and is not: %q", adm.Recovery)
			}
		})
	}
}

// #579 finding 5: the previous version of this test asserted only that a CALL EXPRESSION
// exists, so it passed while admission called the predicate purely for its Reason string and
// computed its own verdict. A call is not consumption. That is the same shape-not-proof
// mistake this milestone has now produced five times, and it is why the half-consolidation
// shipped with a green test.
//
// This asserts the VERDICT FLOWS: at each site the predicate's result must reach a condition
// (required()) or be returned, not merely be evaluated for a field.
func TestBothSurfacesConsumeThePredicateVerdict(t *testing.T) {
	for _, tc := range []struct {
		file   string
		symbol string
	}{
		{"launch.go", "preparedRunActorAdmission"},
		{"team_resume.go", "preparedRunAdmissionForMember"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, filepath.Join(".", tc.file), nil, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// Find the identifier the call result is assigned to, then require that identifier
			// to appear in a call to required() somewhere in the same file.
			var assigned []string
			ast.Inspect(parsed, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, rhs := range as.Rhs {
					call, isCall := rhs.(*ast.CallExpr)
					if !isCall {
						continue
					}
					id, isID := call.Fun.(*ast.Ident)
					if !isID || id.Name != tc.symbol {
						continue
					}
					if len(as.Lhs) > 0 {
						if target, ok := as.Lhs[0].(*ast.Ident); ok {
							assigned = append(assigned, target.Name)
						}
					}
				}
				return true
			})
			if len(assigned) == 0 {
				t.Fatalf("%s never assigns the result of %s; #573 requires the shared predicate to "+
					"decide, and a result that is not bound cannot be consumed", tc.file, tc.symbol)
			}

			// #579 round 3 F3: the previous version accepted ANY same-named required() receiver
			// anywhere in the file, so a dead `_ = adm.required()` sitting beside an independent
			// verdict computer passed. Consumption is not control.
			//
			// The verdict must appear in the CONDITION of an if that controls a refusal, so the
			// branch is actually governed by the predicate. This is the seventh shape-versus-proof
			// item in this milestone and the review reopened for safety anyway, so it is fixed
			// rather than declared.
			consumed := false
			ast.Inspect(parsed, func(n ast.Node) bool {
				ifs, ok := n.(*ast.IfStmt)
				if !ok || ifs.Cond == nil {
					return true
				}
				// Does this if's CONDITION call required() on one of the bound identifiers?
				governed := false
				ast.Inspect(ifs.Cond, func(c ast.Node) bool {
					call, isCall := c.(*ast.CallExpr)
					if !isCall {
						return true
					}
					sel, isSel := call.Fun.(*ast.SelectorExpr)
					if !isSel || sel.Sel == nil || sel.Sel.Name != "required" {
						return true
					}
					recv, isID := sel.X.(*ast.Ident)
					if !isID {
						return true
					}
					for _, name := range assigned {
						if recv.Name == name {
							governed = true
						}
					}
					return true
				})
				if !governed {
					return true
				}
				// And does that if's body actually REFUSE -- return an error or set the blocked
				// action? A governed condition guarding nothing is still not control.
				ast.Inspect(ifs.Body, func(b ast.Node) bool {
					switch node := b.(type) {
					case *ast.ReturnStmt:
						if len(node.Results) > 0 {
							consumed = true
						}
					case *ast.Ident:
						if node.Name == "resumeBlocked" {
							consumed = true
						}
					}
					return true
				})
				return true
			})
			if !consumed {
				t.Errorf("%s calls %s but its required() verdict never CONTROLS a refusing branch -- "+
					"a dead `_ = x.required()` beside an independent verdict computer would look identical. "+
					"The predicate must govern the condition, or a predicate change moves one surface and "+
					"not the other, which is the disagreement #573 exists to end.",
					tc.file, tc.symbol)
			}
		})
	}
}

// The locked requirement for #573 was a SHARED predicate, not a parallel reimplementation, so
// that preview and admission cannot disagree. This asserts the sharing structurally: both the
// admission path and the resume planner must reference the predicate.
//
// A behavioural end-to-end test would be satisfiable by two independent implementations that
// happen to agree today, which is exactly how #573 arose -- the condition was inlined five
// times and stayed consistent until one copy was missing.
//
// DECLARED BOUND: this proves both surfaces CALL the predicate. It does not execute a full
// resume against an on-disk prepared session, so it cannot prove the planner's blocked row
// renders end-to-end; the verdict table above covers the classification and the planner's
// field-setting is reviewed by inspection at the call site.
func TestPreviewAndAdmissionShareOnePredicate(t *testing.T) {
	const predicate = "preparedRunActorAdmission"
	const loader = "preparedRunAdmissionForMember"

	want := map[string]string{
		// admission -- the authority whose refusal the preview must not contradict
		"launch.go": predicate,
		// the resume planner, via the manifest-loading wrapper
		"team_resume.go": loader,
	}

	for file, symbol := range want {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, filepath.Join(".", file), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		found := false
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, isID := call.Fun.(*ast.Ident); isID && id.Name == symbol {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s does not call %s. #573 requires preview and admission to consult ONE "+
				"predicate; a second copy of the condition is how they came to disagree.", file, symbol)
		}
	}

	// Anti-vacuity: the loader must itself route through the predicate, or the planner would be
	// calling a wrapper that answers independently and the "shared" claim would be hollow.
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(".", "prepared_run_admission.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse predicate file: %v", err)
	}
	routed := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != loader {
			return true
		}
		ast.Inspect(fn, func(x ast.Node) bool {
			call, isCall := x.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if id, isID := call.Fun.(*ast.Ident); isID && id.Name == predicate {
				routed = true
			}
			return true
		})
		return true
	})
	if !routed {
		t.Errorf("%s must delegate to %s; otherwise the planner and admission answer independently "+
			"while appearing to share a definition", loader, predicate)
	}
}

// #579 round 3 F4's falsifying input. The round-2 check asserted the command prefix and the
// --staged-spawn flag, both of which pass with the binary missing entirely or unquoted -- I
// checked the FLAGS and never the interpolated value, then claimed every emitted form was
// checked against the real command surface.
//
// This uses a binary path that BREAKS an unquoted command: a space plus a metacharacter.
func TestStagedRecoveryQuotesTheInterpolatedBinary(t *testing.T) {
	manifest := preparedRunManifest{
		Generation: "g-7", Project: "/repo", Profile: "squad", Session: "v2-25-0",
		GoalNamespace: "squad/v2-25-0", GoalDigest: "gd-7",
		StagedRoster: []string{"qa"},
	}
	rec := &launch.Record{Binary: "/opt/my tools/codex;rm -rf x"}

	adm := preparedRunActorAdmission(manifest, "d-7", true, "qa", "qa", rec)

	if !strings.Contains(adm.Recovery, shellQuote(rec.Binary)) {
		t.Errorf("the interpolated binary must be shell-quoted, or a path with spaces malforms the\n"+
			"command and a metacharacter becomes SYNTAX when the operator copies it.\ngot: %s", adm.Recovery)
	}
	// The raw form must NOT appear unquoted: asserting the quoted form alone would also pass if
	// both were present.
	if strings.Contains(adm.Recovery, "codex;rm -rf x") && !strings.Contains(adm.Recovery, shellQuote(rec.Binary)) {
		t.Errorf("recovery carries the raw unquoted binary: %s", adm.Recovery)
	}
}
