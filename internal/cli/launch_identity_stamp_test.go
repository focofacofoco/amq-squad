package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #572: the external-lead adoption path built bootstrapack.Expectation as a struct literal
// and so recorded an EMPTY LaunchID, while every other site goes through
// bootstrapack.NewExpectation which always generates one. Live identity then refused with
// "no exact launch id" and an adopted lead could not receive goal delivery.
//
// This is the class fix rather than the instance fix: the constructor is the only sanctioned
// way to build an Expectation, because it is the only thing that guarantees the launch id.
// A composite literal anywhere in production Go can omit it again.
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
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		inspected++
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			// `Expectation{}, err` is a zero-value return on an error path: it stamps
			// nothing and is discarded by the caller, so it is not a construction site.
			if strings.Contains(line, "bootstrapack.Expectation{}, err") {
				continue
			}
			if strings.Contains(line, "bootstrapack.Expectation{") {
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: a broken walk would report perfect compliance.
	if inspected < 50 {
		t.Fatalf("inspected only %d non-test .go files; the walk is broken", inspected)
	}
	if len(offenders) != 0 {
		t.Errorf("production code builds bootstrapack.Expectation as a literal at %v; use "+
			"bootstrapack.NewExpectation so the launch id is always stamped (#572)", offenders)
	}
}

// The refusal must name the FAILURE CLASS, because #571 counts a worker launched only
// after identity is verified and must distinguish "cannot verify" from "verifiably wrong".
func TestIncompleteRecordRefusalIsNotAMismatch(t *testing.T) {
	msg := incompleteLaunchRecordError().Error()
	for _, want := range []string{"INCOMPLETE", "cannot be verified", "not an identity conflict"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal must contain %q so the reader knows the class of failure:\n  %s", want, msg)
		}
	}
	if strings.Contains(msg, "mismatch:") {
		t.Errorf("refusal must not read as a mismatch:\n  %s", msg)
	}
}
