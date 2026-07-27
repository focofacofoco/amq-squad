package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #534 moved the skill identity out of a "Skill version: X.Y.Z" body preamble and into
// frontmatter stamped by scripts/generate-plugin-skills.py. These tests protect the two
// properties that move had to preserve: the shipped bundle still verifies, and a bundle
// that cannot be verified still WARNS rather than passing quietly.

// TestDoctorAcceptsTheRealShippedBundle reads the actual mirror rather than a fixture.
// A synthetic string proves the regex matches something the test author wrote; only the
// real file proves the binary agrees with what the generator emits.
func TestDoctorAcceptsTheRealShippedBundle(t *testing.T) {
	for _, mirror := range []string{"claude", "codex"} {
		t.Run(mirror, func(t *testing.T) {
			skillPath := filepath.Join("..", "..", "plugins", mirror, "skills", "amq-squad", "SKILL.md")
			raw, err := os.ReadFile(skillPath)
			if err != nil {
				// Not skipped: the mirror is committed, so an unreadable one is a
				// broken repository rather than an absent optional fixture.
				t.Fatalf("read shipped bundle: %v", err)
			}
			body := string(raw)

			if strings.Contains(body, "Skill version:") {
				t.Fatalf("%s still carries the deleted body preamble; #534 removes it", skillPath)
			}

			m := skillVersionFrontmatterRE.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("%s: frontmatter version not readable by the binary's own pattern", skillPath)
			}

			got := doctorCheckSkillVersion(doctorExecution{
				RunningVersion: "v" + m[1],
				SkillMDContent: func(string) (string, string, bool) { return body, skillPath, true },
			})
			if got.Status != doctorOK {
				t.Errorf("doctor on the real shipped bundle = %q, want OK\ndetail: %s", got.Status, got.Detail)
			}
		})
	}
}

// TestNoGoCodeReadsTheDeletedMarker is an enumeration test derived from the tree rather
// than from a maintained list, so a NEW consumer of the deleted marker cannot be added
// without this failing. Three production consumers existed when the preamble was removed
// (doctor.go, version_alignment.go, team_rules.go); nothing should reintroduce a fourth.
//
// Comment lines are ignored: prose explaining what was removed is documentation, not a
// consumer. Excluding them by rule rather than by an exemption list keeps this test from
// needing maintenance of its own.
func TestNoGoCodeReadsTheDeletedMarker(t *testing.T) {
	const marker = "Skill version:"
	root := filepath.Join("..", "..", "internal")

	var offenders []string
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
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, marker) {
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// Anti-vacuity: prove the walk actually inspected the files it claims to cover. An
	// empty result from a walk that visited nothing would otherwise read as a pass.
	if got := countGoFiles(t, root); got < 50 {
		t.Fatalf("walk inspected only %d non-test .go files; the traversal is broken", got)
	}

	if len(offenders) != 0 {
		t.Errorf("production Go still reads the deleted %q marker in %d place(s):\n  %s\n"+
			"Read the frontmatter version via skillVersionFrontmatterRE instead.",
			marker, len(offenders), strings.Join(offenders, "\n  "))
	}
}

func countGoFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("count walk: %v", err)
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
