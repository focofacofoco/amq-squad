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
	manifest := preparedRunManifest{
		Generation:   "g-7",
		Project:      "/repo",
		Profile:      "squad",
		Session:      "v2-25-0",
		StagedRoster: []string{"qa"},
	}
	// A record whose token is COMPLETE. Bindable turns on this and nothing else, because
	// execRestoreRecord takes the token from the record and falls back to an empty token --
	// which admission refuses -- when the record has none.
	bound := &launch.Record{
		PreparedRunGeneration:    "g-7",
		PreparedRunDigest:        "d-7",
		PreparedRunGoalNamespace: "squad/v2-25-0",
		PreparedRunGoalDigest:    "gd-7",
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
			name: "staged actor whose record carries a complete token", prepared: true, role: "qa", rec: bound,
			wantRequired: true, wantStaged: true, wantBindable: true, wantRecovery: "restore",
		},
		{
			name: "prepared actor whose record carries a complete token", prepared: true, role: "cto", rec: bound,
			wantRequired: true, wantStaged: false, wantBindable: true, wantRecovery: "restore",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adm := preparedRunActorAdmission(manifest, tc.prepared, tc.role, "handle-"+tc.role, tc.rec)

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
