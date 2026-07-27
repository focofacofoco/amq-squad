package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #572: the external-lead adoption path built bootstrapack.Expectation as a struct literal
// and so recorded an EMPTY LaunchID, while bootstrapack.NewExpectation always generates one.
// Live identity then refused with "no exact launch id" and an adopted lead could not receive
// goal delivery. A second site on the prepared-run path had the identical defect.
//
// This is the class fix: the constructor is the only sanctioned way to build an Expectation,
// because it is the only thing that guarantees the launch id.
//
// Implemented with go/ast rather than string matching. #575 review found that a
// substring-and-skip-the-line exemption hides a real literal sharing a line with the
// zero-value error return, e.g.
//
//	return bootstrapack.Expectation{}, errorFor(&bootstrapack.Expectation{Required: true})
//
// The AST sees each CompositeLit separately, so the whole string-matching hole class is gone
// rather than narrowed.
func TestNoProductionCodeBuildsExpectationLiterals(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	var offenders []string
	inspected := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		inspected++
		// Exempting EVERY empty literal was too broad (#575 round 3): production code could
		// write rec.BootstrapExpectation = &bootstrapack.Expectation{} and recreate the
		// exact empty-LaunchID defect while passing. The exemption is now POSITIONAL - only
		// a zero-value literal returned alongside a non-nil error, which is the
		// `return Expectation{}, err` shape whose value the caller discards.
		exempt := map[ast.Node]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok || len(ret.Results) < 2 {
				return true
			}
			for _, res := range ret.Results {
				lit, isLit := res.(*ast.CompositeLit)
				if !isLit || len(lit.Elts) != 0 {
					continue
				}
				// The exemption requires a non-nil value in the ERROR POSITION, i.e. the
				// LAST result. Accepting any non-nil identifier anywhere let
				// `return Expectation{}, nil, cached` through on the strength of `cached`
				// while the error result was nil (#575 round 4).
				last := ret.Results[len(ret.Results)-1]
				if id, isID := last.(*ast.Ident); isID && id.Name != "nil" {
					exempt[lit] = true
				}
				if _, isCall := last.(*ast.CallExpr); isCall {
					exempt[lit] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Expectation" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "bootstrapack" {
				return true
			}
			if exempt[lit] {
				return true
			}
			offenders = append(offenders, fset.Position(lit.Pos()).String())
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: a broken walk or a parse that matched nothing would report compliance.
	if inspected < 50 {
		t.Fatalf("parsed only %d non-test .go files; the walk is broken", inspected)
	}
	if len(offenders) != 0 {
		t.Errorf("production code builds a populated bootstrapack.Expectation literal at %v; "+
			"use bootstrapack.NewExpectation so the launch id is always stamped (#572)", offenders)
	}
}

// The refusal must name the FAILURE CLASS: #571 counts a worker launched only after identity
// is verified and must distinguish "cannot verify" from "verifiably wrong".
func TestIncompleteRecordRefusalIsNotAMismatch(t *testing.T) {
	msg := incompleteLaunchRecordError().Error()
	for _, want := range []string{"incomplete launch record", "cannot be verified", "not an identity conflict"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal must contain %q so the reader knows the class:\n  %s", want, msg)
		}
	}
}

// #575 review finding 1: the helper said "not mismatched" while the OUTER wrapper still
// labelled it a mismatch, so the operator-visible string contradicted itself and the
// cannot-verify classification was destroyed at exactly the surface that matters.
//
// This asserts the COMPLETE RENDERED string on the promotion path, not the helper in
// isolation. Testing the helper alone is what let the contradiction ship.
func TestRenderedPromotionRefusalDoesNotCallAnIncompleteRecordAMismatch(t *testing.T) {
	project := t.TempDir()
	// A real roster is required or the resolver fails earlier on a missing team.json and the
	// assertion passes or fails for the wrong reason. First draft of this test did exactly
	// that: vacuous fixture, caught because the failure message named team.json.
	if err := team.WriteProfile(project, "default", team.Team{Project: project,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s1"}}}); err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{
		Role: "cto", Handle: "cto", Binary: "codex", Session: "s1",
		Root: filepath.Join(project, ".agent-mail", "s1"), TeamHome: project, TeamProfile: "default",
		PreparedRunGeneration: "g1", PreparedRunDigest: "d1", PreparedRunLaunchAttempt: "a1",
		// BootstrapExpectation deliberately nil: the incomplete-record case.
	}
	// The resolver reads launch.json from disk rather than trusting the passed record, so
	// the record must actually exist there with its BootstrapExpectation absent.
	root := filepath.Join(project, ".agent-mail", "s1")
	if err := launch.Write(filepath.Join(root, "agents", "cto"), rec); err != nil {
		t.Fatal(err)
	}
	_, _, err := verifyRuntimeActionWithRecord("status promotion", project, "default", "s1", "cto", rec)
	if err == nil {
		t.Fatal("an incomplete record must be refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "mismatch") {
		t.Errorf("rendered refusal must not label an incomplete record a mismatch:\n  %s", msg)
	}
	if !strings.Contains(msg, "could not be verified") {
		t.Errorf("rendered refusal must say identity could not be verified:\n  %s", msg)
	}
}

// Three review rounds found the same shape: the cannot-verify classification exists, and
// some surface routes around it and relabels the error a mismatch. Sites found so far:
// two in live_identity's wrapper, four in the refusal family, one pre-validator that ran
// BEFORE the sentinel, one control-continue wrapper, and one no-verdict case.
//
// This makes the acceptance criterion permanent instead of a grep someone remembers to run:
// every site that wraps an error with the mismatch label must first route the sentinel away.
func TestEveryMismatchWrapperGuardsTheIncompleteSentinel(t *testing.T) {
	const label = "verified live identity mismatch"
	const sentinel = "errIncompleteLaunchRecord"
	root := filepath.Join("..", "..", "internal")

	var unguarded []string
	sites := 0

	// guardsSentinel reports whether an if-statement tests errors.Is(..., sentinel).
	guardsSentinel := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(x ast.Node) bool {
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Is" {
				return true
			}
			for _, a := range call.Args {
				if id, isID := a.(*ast.Ident); isID && id.Name == sentinel {
					found = true
				}
			}
			return true
		})
		return found
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			guarded := false
			for _, stmt := range block.List {
				if ifs, isIf := stmt.(*ast.IfStmt); isIf && guardsSentinel(ifs.Cond) {
					guarded = true
					continue
				}
				// Does this statement ITSELF wrap with the mismatch label? Stop at nested
				// block boundaries: an enclosing `if err != nil { ... }` contains the label
				// in a child block that this walk visits separately, and judging the
				// enclosing statement flagged correctly-guarded code.
				wraps := false
				ast.Inspect(stmt, func(x ast.Node) bool {
					if x == nil {
						return false
					}
					if b, isBlock := x.(*ast.BlockStmt); isBlock && b != stmt {
						return false
					}
					if lit, isLit := x.(*ast.BasicLit); isLit && strings.Contains(lit.Value, label) {
						wraps = true
					}
					return true
				})
				if !wraps {
					continue
				}
				sites++
				if !guarded {
					unguarded = append(unguarded, fset.Position(stmt.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: if the label is renamed this must fail rather than pass by finding nothing.
	if sites == 0 {
		t.Fatalf("found no %q wrap sites; the label was renamed and this test is now blind", label)
	}
	if len(unguarded) != 0 {
		t.Errorf("these sites wrap an error as %q without an errors.Is(%s) guard DOMINATING them "+
			"in the same block: %v. An incomplete record is cannot-verify, not verifiably-wrong",
			label, sentinel, unguarded)
	}
}
