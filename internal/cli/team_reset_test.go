package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDiscoverAMQResetRootsOnlyReturnsOperationalDirectories(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".agent-mail", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad", "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte("root: .agent-mail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amq-faco.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAMQResetRoots(parent)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(project, ".agent-mail"), filepath.Join(project, ".amq-squad")}
	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Fatalf("roots=%#v, missing %s", got, path)
		}
	}
	for _, path := range got {
		base := filepath.Base(path)
		if base != ".agent-mail" && base != ".amq-squad" {
			t.Fatalf("unsafe reset root %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".amqrc")); err != nil {
		t.Fatalf("discovery touched config: %v", err)
	}
}

func TestRemoveAMQResetRootIsIdempotentAndPreservesProjectFiles(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(project, ".amqrc")
	if err := os.WriteFile(keep, []byte("root: .agent-mail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := removeAMQResetRoot(root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("project config removed: %v", err)
	}
}

func TestResetSquadRemovesTeamAndPreservesProjectFiles(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".amq-squad")
	for _, path := range []string{
		filepath.Join(root, "tasks", "work", "t.json"),
		filepath.Join(root, "team.json"),
		filepath.Join(root, "team-rules.md"),
		filepath.Join(root, "roles", "qa.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep-or-delete"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(project, "README.md")
	if err := os.WriteFile(keep, []byte("project work"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeAMQResetRoot(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("team root survived: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("project work removed: %v", err)
	}
}
