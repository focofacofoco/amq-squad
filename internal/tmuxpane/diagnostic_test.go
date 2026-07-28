package tmuxpane

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// #577 r10 STEP 1: the Unavailable detail must carry the tmux stderr that explains the failure.
//
// This is a DIAGNOSTIC change and these tests pin it as one. The classification-unchanged test at
// the bottom is the load-bearing companion: without it, a step-3 marker fix could leak into this
// window and its behaviour change would ride in under a "diagnostics only" label.

// exitErrorWithStderr builds an *exec.ExitError carrying stderr, which is the ONLY place tmux's
// actual message lives for a failed capture. err.Error() is just "exit status N".
func exitErrorWithStderr(t *testing.T, stderr string) error {
	t.Helper()
	// `false` exits 1 without output; we attach the stderr the way exec.Cmd.Output does.
	cmd := exec.Command("false")
	runErr := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) {
		t.Fatalf("fixture: expected an ExitError from `false`, got %v", runErr)
	}
	ee.Stderr = []byte(stderr)
	return ee
}

// THE FALSIFIER FOR THE WHOLE ROUND: an unrecognized stderr must reach the detail. If it does not,
// the next CI run reports "exit status 1" again and r10 step 3 stays a guess.
func TestUnavailableDetailCarriesTheUnrecognizedStderr(t *testing.T) {
	const stderr = "no server running on /tmp/tmux-501/default"
	restore := swapCapture(t, "", exitErrorWithStderr(t, stderr))
	_ = restore

	result := InspectPaneExactByID("%2")

	if result.State != PaneInspectionUnavailable {
		t.Fatalf("state = %q, want unavailable: this test is about the DETAIL of an unavailable "+
			"result, and a different state means the classification changed under it", result.State)
	}
	if !strings.Contains(result.Detail, stderr) {
		t.Errorf("the detail must carry the tmux stderr that explains the failure.\n"+
			"Without it the operator, the CI log and the reviewer all see THAT classification fell "+
			"through to unavailable and never see what it fell through ON -- which is exactly why "+
			"#577 r10's cause could only be hypothesised.\ngot: %q\nwant it to contain: %q",
			result.Detail, stderr)
	}
	// The bare exec text must remain too: dropping it would lose the exit status.
	if !strings.Contains(result.Detail, "exit status") {
		t.Errorf("the detail must keep the exec error alongside the stderr; got %q", result.Detail)
	}
}

// SANITIZATION, both halves. A detail that reaches operator output must not break the report it
// lands in, and must not be able to flood a log.
func TestUnavailableDetailSanitizesTheStderr(t *testing.T) {
	t.Run("collapses newlines and tabs to one line", func(t *testing.T) {
		restore := swapCapture(t, "", exitErrorWithStderr(t, "line one\nline two\tand three\n\n"))
		_ = restore

		detail := InspectPaneExactByID("%2").Detail

		if strings.ContainsAny(detail, "\n\t") {
			t.Errorf("the detail must be a single line: it is embedded in structured operator "+
				"reports and a raw multi-line tmux message would break them.\ngot: %q", detail)
		}
		for _, want := range []string{"line one", "line two", "and three"} {
			if !strings.Contains(detail, want) {
				t.Errorf("collapsing must preserve content; %q missing from %q", want, detail)
			}
		}
	})

	t.Run("bounds a runaway", func(t *testing.T) {
		restore := swapCapture(t, "", exitErrorWithStderr(t, strings.Repeat("x", 5000)))
		_ = restore

		detail := InspectPaneExactByID("%2").Detail

		if len(detail) > tmuxErrorDetailLimit+128 {
			t.Errorf("a runaway stderr must be bounded; detail length %d", len(detail))
		}
		if !strings.Contains(detail, "truncated") {
			t.Errorf("truncation must be VISIBLE, or a reader cannot tell a bounded message from a "+
				"complete one and may conclude tmux said nothing more; got %q", detail)
		}
	})
}

// THE LOAD-BEARING COMPANION. This window changes DIAGNOSTICS ONLY. Nothing that is Unavailable
// today may become Gone, and a permission denial must stay Unavailable.
//
// Without this pin, the narrow no-server marker from step 3 -- or any broader exit-1 => Gone --
// could ride into this window under a diagnostics label, and the wake lanes going green would look
// like confirmation of a hypothesis I have not yet observed. cto's point applies here in advance:
// a broad wrong fix produces the same green as the right one, so the pins that must hold are the
// ones asserting what does NOT change.
func TestDiagnosticsWindowChangesNoClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		want   PaneInspectionState
		why    string
	}{
		{
			name: "no server running is STILL unavailable in this window", stderr: "no server running on /tmp/tmux-501/default",
			want: PaneInspectionUnavailable,
			why:  "this is r10's HYPOTHESIZED cause; narrowing it to Gone is step 3 and must not land here",
		},
		{
			name: "generic transient stays unavailable", stderr: "error connecting to /tmp/tmux-501/default (No such file or directory)",
			want: PaneInspectionUnavailable,
			why:  "an unrecognized failure is unproven, never proven-absent",
		},
		{
			name: "permission denial stays unavailable", stderr: "error connecting to /tmp/tmux-501/default (Operation not permitted)",
			want: PaneInspectionUnavailable,
			why:  "a sandboxed agent cannot see the pane; that is not evidence the pane is gone",
		},
		{
			name: "a genuine missing pane is STILL gone", stderr: "can't find pane: %2",
			want: PaneInspectionGone,
			why:  "the existing proven-gone markers must keep working; this pin fails if the diagnostic change broke them",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := swapCapture(t, "", exitErrorWithStderr(t, tc.stderr))
			_ = restore

			got := InspectPaneExactByID("%2").State

			if got != tc.want {
				t.Errorf("state = %q, want %q.\n%s\nDiagnostics-only means diagnostics only: a "+
					"classification change riding in under this label would be confirmed by the same "+
					"green CI that a correct fix produces.", got, tc.want, tc.why)
			}
		})
	}
}
