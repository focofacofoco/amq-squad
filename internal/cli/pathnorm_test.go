package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #540 acceptance criterion 2: the recorded identity must be representation
// independent for ".", "./", a relative subpath, a symlinked path, and an
// absolute path.
//
// Representation independence is asserted at the level it is actually promised:
// the RECORDED value is stable and absolute for every spelling of the same
// directory, and the COMPARISON treats a symlink and its target as one location.
func TestProjectFlagRecordsOneRepresentationForEverySpelling(t *testing.T) {
	realDir := t.TempDir()
	sub := filepath.Join(realDir, "nested", "project")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-project")
	if err := os.Symlink(sub, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Every spelling below names `sub`. Run each from inside `sub` so the
	// relative forms have a meaningful base.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}

	spellings := map[string]string{
		"dot":              ".",
		"dot-slash":        "./",
		"relative-subpath": filepath.Join("..", "project"),
		"absolute":         sub,
		"symlink":          link,
	}
	for name, spelling := range spellings {
		t.Run(name, func(t *testing.T) {
			recorded := recordProjectFlag(t, spelling)
			if !filepath.IsAbs(recorded) {
				t.Fatalf("recorded --project %q as %q, want an absolute path", spelling, recorded)
			}
			if recorded != filepath.Clean(recorded) {
				t.Fatalf("recorded --project %q as unclean %q", spelling, recorded)
			}
			// The comparison must agree that this is the same project as the
			// absolute spelling, whatever was recorded. This is the property the
			// #540 identity check depends on.
			if !sameFilesystemPath(recorded, sub) {
				t.Fatalf("recorded --project %q as %q, which does not compare equal to %q", spelling, recorded, sub)
			}
		})
	}
}

// recordProjectFlag parses --project through the real shared parse helper and
// returns the value a command body would read.
func recordProjectFlag(t *testing.T, value string) string {
	t.Helper()
	fs := flag.NewFlagSet("test-command", flag.ContinueOnError)
	project := fs.String("project", "", "")
	if err := parseFlags(fs, []string{"--project", value}); err != nil {
		t.Fatalf("parseFlags(--project %q): %v", value, err)
	}
	return *project
}

// A relative --project must become absolute at parse time, because that value
// flows straight into prepared records, launch records, and identity tuples.
// This is the exact #540 root cause.
func TestProjectFlagIsAbsoluteBeforeAnyCommandBodyReadsIt(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := recordProjectFlag(t, "."); got == "." {
		t.Fatal(`--project "." reached the command body verbatim; it must be absolute before it can enter any record`)
	}
}

// An unset --project must stay unset. Commands distinguish "absent" (default to
// cwd) from "explicitly supplied", so normalization must not materialize a value.
func TestUnsetProjectFlagStaysUnset(t *testing.T) {
	fs := flag.NewFlagSet("test-command", flag.ContinueOnError)
	project := fs.String("project", "", "")
	if err := parseFlags(fs, nil); err != nil {
		t.Fatal(err)
	}
	if *project != "" {
		t.Fatalf("unset --project became %q; it must remain empty", *project)
	}
	if flagWasSet(fs, "project") {
		t.Fatal("unset --project reported as explicitly set")
	}
}

// The `list` command's --project is a comma-separated LIST of directories. It is
// an explicitly named exemption, so assert the exemption holds: single-path
// normalization would corrupt the list.
func TestListCommandProjectFlagIsExemptFromPathNormalization(t *testing.T) {
	if !commaSeparatedProjectFlagCommands["list"] {
		t.Fatal("list must remain a named exemption from --project path normalization")
	}
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	project := fs.String("project", "", "")
	value := "one,two"
	if err := parseFlags(fs, []string{"--project", value}); err != nil {
		t.Fatal(err)
	}
	if *project != value {
		t.Fatalf("list --project was rewritten to %q; the comma-separated list must survive verbatim", *project)
	}
}

// The record/compare split is load-bearing (see pathnorm.go). Recording must NOT
// resolve symlinks -- doing so rewrites the operator-chosen path and changes
// every digest computed over it -- while comparison MUST resolve them.
func TestRecordingKeepsSymlinkAndComparisonResolvesIt(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := absoluteFilesystemPath(link); got != filepath.Clean(link) {
		t.Fatalf("absoluteFilesystemPath resolved the symlink to %q; recording must preserve %q", got, link)
	}
	if absoluteFilesystemPath(link) == absoluteFilesystemPath(target) {
		t.Fatal("recording collapsed a symlink onto its target; that is the comparison's job, not the recorder's")
	}
	if canonicalFilesystemPath(link) != canonicalFilesystemPath(target) {
		t.Fatalf("canonicalFilesystemPath(%q)=%q did not resolve to the same location as %q=%q",
			link, canonicalFilesystemPath(link), target, canonicalFilesystemPath(target))
	}
}

// canonicalFilesystemPath must be total: a comparator has to canonicalize a
// recorded location that no longer exists (a removed worktree) without failing.
func TestCanonicalPathHandlesMissingLocation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "worktree")
	got := canonicalFilesystemPath(missing)
	if got != filepath.Clean(missing) {
		t.Fatalf("canonicalFilesystemPath(%q)=%q, want the absolute form of a non-existent path", missing, got)
	}
	if canonicalFilesystemPath("   ") != "" {
		t.Fatal("blank input must canonicalize to the empty string")
	}
}

// A path set persisted with RELATIVE entries must be anchored to the project it
// was recorded against, not to the process working directory. This is the
// out-of-repo invocation case: `amq-squad ... --project /path/to/repo` run from
// somewhere else must reach the same verdict as the same command run inside it.
func TestRelativePathSetAnchorsToProjectNotProcessCWD(t *testing.T) {
	project := t.TempDir()
	claudeDir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(settings, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyRelative := []string{filepath.Join(".claude", "settings.local.json")}
	absolute := []string{settings}

	elsewhere := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	for _, from := range []struct{ name, dir string }{
		{"cwd inside the project", project},
		{"cwd outside the project", elsewhere},
	} {
		t.Run(from.name, func(t *testing.T) {
			if err := os.Chdir(from.dir); err != nil {
				t.Fatal(err)
			}
			got := canonicalFilesystemPathsIn(project, legacyRelative)
			want := canonicalFilesystemPathsIn(project, absolute)
			if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
				t.Fatalf("legacy relative entry anchored to %v, absolute anchored to %v; they must agree from any cwd", got, want)
			}
			if !strings.HasPrefix(got[0], canonicalFilesystemPath(project)) {
				t.Fatalf("relative entry resolved to %q, which is outside the project %q", got[0], project)
			}
		})
	}
}

// Absolute entries must be unaffected by anchoring, and a set must be
// order-independent: the #539 symptom included a leading "." sorting differently
// from the absolute form and making an identical three-file set compare unequal.
func TestPathSetComparisonIsOrderAndRepresentationIndependent(t *testing.T) {
	project := t.TempDir()
	a := filepath.Join(project, ".claude", "settings.local.json")
	b := filepath.Join(project, ".mcp.json")
	c := "/etc/hosts"

	recordedOneOrder := canonicalFilesystemPathsIn(project, []string{c, a, b})
	recordedOtherOrder := canonicalFilesystemPathsIn(project, []string{b, c, a})
	mixedRepresentation := canonicalFilesystemPathsIn(project, []string{
		filepath.Join(".claude", "settings.local.json"), c, ".mcp.json",
	})
	if len(recordedOneOrder) != 3 {
		t.Fatalf("expected 3 canonical entries, got %v", recordedOneOrder)
	}
	for i := range recordedOneOrder {
		if recordedOneOrder[i] != recordedOtherOrder[i] {
			t.Fatalf("set comparison is order dependent: %v vs %v", recordedOneOrder, recordedOtherOrder)
		}
		if recordedOneOrder[i] != mixedRepresentation[i] {
			t.Fatalf("set comparison is representation dependent: %v vs %v", recordedOneOrder, mixedRepresentation)
		}
	}
}
