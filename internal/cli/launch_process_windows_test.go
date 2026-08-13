//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

func TestWindowsLaunchEnvPinsSessionfulIdentity(t *testing.T) {
	rec := launch.Record{
		Root:     `C:\repo\.agent-mail\work`,
		BaseRoot: `C:\repo\.agent-mail`,
		Session:  "work",
		Handle:   "qa",
	}
	env := windowsLaunchEnv([]string{"KEEP=yes", "AM_ROOT=stale", "AM_SESSION=stale"}, rec)
	for key, want := range map[string]string{
		"KEEP": "yes", "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot,
		"AM_SESSION": rec.Session, "AM_ME": rec.Handle,
	} {
		if !envHas(env, key, want) {
			t.Fatalf("%s=%q missing from %#v", key, want, env)
		}
	}
}

func TestWindowsLaunchEnvOmitsSessionForExactRoot(t *testing.T) {
	rec := launch.Record{Root: `C:\repo\.agent-mail\review\work`, BaseRoot: `C:\repo\.agent-mail\review\work`, Session: "work", Handle: "qa"}
	env := windowsLaunchEnv([]string{"AM_SESSION=stale"}, rec)
	if envHasPrefix(env, "AM_SESSION", "") {
		t.Fatalf("exact-root environment retained AM_SESSION: %#v", env)
	}
}

func TestRunPlatformAgentUpdatesRecordWithRealPID(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agents", "qa")
	rec := launch.Record{CWD: root, Root: root, BaseRoot: root, Handle: "qa", Binary: "cmd.exe"}
	snapshot, err := writeLaunchRecordWithSnapshot(agentDir, rec)
	if err != nil {
		t.Fatal(err)
	}
	handled, keepRecord, err := runPlatformAgent("cmd.exe", []string{"/d", "/c", "exit", "0"}, rec, agentDir, &snapshot)
	if !handled || !keepRecord || err != nil {
		t.Fatalf("handled=%v keepRecord=%v err=%v", handled, keepRecord, err)
	}
	written, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if written.AgentPID <= 0 || written.AgentPID == os.Getpid() {
		t.Fatalf("AgentPID=%d, want child pid", written.AgentPID)
	}
	if written.StartedAt.IsZero() {
		t.Fatal("StartedAt is zero")
	}
	if !strings.EqualFold(written.Binary, "cmd.exe") {
		t.Fatalf("Binary=%q", written.Binary)
	}
}
