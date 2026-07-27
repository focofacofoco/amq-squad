package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #577 finding 1 was a FOURTH production path that still typed its command, and finding 6
// asked that a fifth cannot hide. The pairing test in team_launch_pane_delivery_test.go
// structurally cannot catch either: it enumerates deliverPaneCommand sites, and a path that
// types is precisely a path that never calls it. An enumeration keyed on the CORRECT
// mechanism is blind to code using the wrong one.
//
// This enumerates the wrong mechanism instead. Every production `send-keys` must be named
// here with a reason it is exempt from the #571 fix. A new one fails until someone either
// converts it or justifies it, which is the check that would have caught finding 1.
func TestEveryTypedDeliverySiteIsJustified(t *testing.T) {
	// Exemptions carry the JUSTIFICATION, not just the location, so the next reader can tell
	// a deliberate exemption from an unreviewed one.
	exempt := map[string]string{
		// The NOC dispatch keeps send-keys deliberately: exec must preserve the pane PID
		// that beginGlobalNOCLaunch already persisted, and respawn-pane would replace it.
		// The 1,024-byte boundary is avoided by keeping the payload off the tty (the
		// bootstrap is written to a file and substituted in), so the typed line is a fixed
		// short length regardless of bootstrap size. See orchestrate.go.
		"orchestrate.go": "exec must preserve the pane PID; payload is substituted from a file, not typed",
	}

	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	found := map[string]int{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, `"send-keys"`) {
				found[filepath.Base(path)]++
			}
		}
	}

	for file, n := range found {
		if _, ok := exempt[file]; !ok {
			t.Errorf("%s issues tmux send-keys at %d site(s) with no recorded justification.\n"+
				"send-keys delivers through the pane tty, which drops any line over MAX_CANON=1024 "+
				"and still reports success (#571). Either deliver the command as the pane process "+
				"(deliverPaneCommand) or add an exemption here stating why typing is required and how "+
				"the length boundary is avoided.", file, n)
		}
	}

	// Self-destructing exemptions: an exemption for a file that no longer types is stale, and
	// a stale exemption is how the next real one gets waved through.
	for file := range exempt {
		if found[file] == 0 {
			t.Errorf("exemption for %s is stale -- it no longer issues send-keys. Remove it, so the "+
				"list keeps meaning what it says.", file)
		}
	}

	// Anti-vacuity: this test is only meaningful while it can still SEE typed delivery. If
	// the scan finds nothing at all, the pattern or the layout changed and the check is blind
	// rather than satisfied.
	if len(found) == 0 {
		t.Fatal("found no send-keys sites anywhere in production code; the scan is broken, not the tree clean")
	}
}

// The boundary itself, asserted rather than trusted: the NOC dispatch line must stay short no
// matter how large the bootstrap grows. #577 finding 1 was not "someone typed a long string",
// it was "the typed length is proportional to a payload that only grows".
func TestNOCDispatchLineDoesNotScaleWithBootstrapSize(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", 64*1024)

	path, err := writeGlobalNOCBootstrapPayload(dir, "launch-1", huge)
	if err != nil {
		t.Fatalf("write payload: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(onDisk) != huge {
		t.Fatalf("payload must round-trip byte-identical: got %d bytes, want %d", len(onDisk), len(huge))
	}

	// PRODUCTION's construction, not a copy of it. Rebuilding the string here would prove the
	// technique works while leaving this green if production reverted to typing the payload --
	// so the argv is passed to the real function, bootstrap as the last element exactly as
	// appendGeneratedBootstrapPrompt leaves it.
	command := nocDispatchCommand([]string{"claude", "--model", "opus", "--", huge}, path)
	if len(command) > 512 {
		t.Errorf("dispatch line is %d bytes for a 64KiB bootstrap; it must not scale with the payload:\n%s",
			len(command), command)
	}
	// MAX_CANON on macOS/BSD. The margin matters: a line at 1000 bytes is one flag away from
	// silently truncating.
	if len(command) >= 1024 {
		t.Errorf("dispatch line is %d bytes, at or over the MAX_CANON=1024 tty boundary", len(command))
	}
	// Quoting is load-bearing: bare $(cat ...) word-splits the prompt into hundreds of args.
	if !strings.Contains(command, `"$(cat `) {
		t.Errorf("substitution must be double-quoted or the prompt is word-split: %s", command)
	}
}
