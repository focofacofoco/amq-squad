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
			// A zero-value literal stamps nothing: it is the `Expectation{}, err` shape on
			// an error path, discarded by the caller. Judged on the AST node's own fields,
			// so nothing else on the line can smuggle a populated literal past this.
			if len(lit.Elts) == 0 {
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
