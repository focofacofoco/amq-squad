package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestSimpleStartCommandPinsCanonicalInputsAndExactInstruction(t *testing.T) {
	prompt := "Read .amq-squad/team-rules.md and your brief at /repo/.amq-squad/briefs/work.md."
	command := emitTeamCommand(emitTeamCommandInput{
		CWD:           "/repo/wt",
		SquadBin:      "/bin/amq-squad",
		TeamHome:      "/repo",
		Member:        team.Member{Role: "dev", Handle: "dev", Binary: "codex"},
		NoBootstrap:   true,
		Workstream:    "work",
		TrustMode:     trustModeSandboxed,
		Profile:       team.DefaultProfile,
		SimpleStart:   true,
		CanonicalRoot: "/repo/.agent-mail/work",
		StartupPrompt: prompt,
	})
	for _, want := range []string{
		"--simple-start",
		"--root /repo/.agent-mail/work",
		"--team-profile default",
		"--no-bootstrap",
		prompt,
	} {
		if !strings.Contains(command, want) {
			t.Errorf("simple start command missing %q:\n%s", want, command)
		}
	}
	if strings.Count(command, prompt) != 1 {
		t.Fatalf("startup instruction must occur exactly once:\n%s", command)
	}
}

func TestParseSimpleStartRequestAcceptsOneSessionSurface(t *testing.T) {
	project := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "flag", args: []string{"--project", project, "--session", "work"}},
		{name: "positional", args: []string{"--project", project, "work"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := parseSimpleStartRequest(tc.args)
			if err != nil {
				t.Fatalf("parseSimpleStartRequest: %v", err)
			}
			if req.Session != "work" || !req.SessionExplicit {
				t.Fatalf("session = %q explicit=%t, want work/true", req.Session, req.SessionExplicit)
			}
		})
	}
	if _, err := parseSimpleStartRequest([]string{"--project", project, "--session", "flag", "positional"}); err == nil {
		t.Fatal("flag and positional session together must be rejected")
	}
}

func TestBootstrapContextCanonicalizesRuntimeCoordinates(t *testing.T) {
	realProject := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realProject, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root := filepath.Join(link, ".agent-mail", "work")
	agentDir := filepath.Join(root, "agents", "dev")
	ctx := bootstrapContextFor(launch.Record{
		Role: "dev", Handle: "dev", Binary: "codex", Session: "work",
		TeamHome: link, CWD: link, Root: root, BaseRoot: filepath.Dir(root),
	}, agentDir, link)
	wantProject := canonicalFilesystemPath(realProject)
	wantRoot := filepath.Join(wantProject, ".agent-mail", "work")
	wantAgentDir := filepath.Join(wantRoot, "agents", "dev")
	for name, gotWant := range map[string][2]string{
		"TeamHome":      {ctx.TeamHome, wantProject},
		"CWD":           {ctx.CWD, wantProject},
		"Root":          {ctx.Root, wantRoot},
		"AgentDir":      {ctx.AgentDir, wantAgentDir},
		"TeamRulesPath": {ctx.TeamRulesPath, filepath.Join(wantProject, ".amq-squad", "team-rules.md")},
		"BriefPath":     {ctx.BriefPath, filepath.Join(wantProject, ".amq-squad", "briefs", "work.md")},
		"LaunchPath":    {ctx.LaunchPath, launch.Path(wantAgentDir)},
	} {
		if gotWant[0] != gotWant[1] {
			t.Errorf("%s = %q, want %q", name, gotWant[0], gotWant[1])
		}
	}
}

func TestSelectStatusLaunchRecordRequiresExactHandleIdentity(t *testing.T) {
	tm := team.Team{Project: "/repo"}
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	entries := []launch.Entry{{
		AgentDir: "/repo/.agent-mail/work/agents/outsider",
		Record: launch.Record{
			TeamHome: "/repo", TeamProfile: team.DefaultProfile, Session: "work",
			Role: "dev", Handle: "outsider", Binary: "codex",
			Root: "/repo/.agent-mail/work",
		},
	}}
	selection := selectStatusLaunchRecord(tm, team.DefaultProfile, member, "work", duplicateLaunchProbe{}, entries)
	if selection.Found || len(selection.DuplicatePaths) != 0 {
		t.Fatalf("wrong-handle same-role record was selected: %+v", selection)
	}
	warnings := statusUnmanagedLaunchRecordWarningsFromEntries(team.Team{Project: "/repo", Members: []team.Member{member}}, team.DefaultProfile, "work", entries)
	if len(warnings) != 1 || warnings[0].Kind != "unmanaged_launch_record" ||
		!strings.Contains(warnings[0].Detail, "outsider") || !strings.Contains(warnings[0].Detail, launch.ExistingPath(entries[0].AgentDir)) {
		t.Fatalf("wrong-handle record was not surfaced as unmanaged: %+v", warnings)
	}
}

func TestReconcileSimpleStartRolesUsesRecordedRuntimeAndClassifiesDrift(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex"},
			{Role: "dev", Handle: "dev", Binary: "codex"},
		},
	}
	started := time.Unix(100, 0).UTC()
	records := []simpleStartRecord{
		{AgentDir: "/root/agents/cto", Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/repo", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "cto", Handle: "cto", Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: 41, StartedAt: started,
		}},
		{AgentDir: "/root/agents/dev", Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/old-worktree", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "dev", Handle: "dev", Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: 42, StartedAt: started,
		}},
	}
	probe := launchRuntimeProbe{
		PIDAlive:         func(pid int) bool { return pid == 42 },
		ProcessMatch:     func(int, func(string) bool) bool { return true },
		ProcessTTY:       func(int) (string, bool) { return "", false },
		ProcessStartTime: func(int) (time.Time, bool) { return started, true },
	}
	rows, _, err := reconcileSimpleStartRoles(tm, team.DefaultProfile, "work", "/root", records, teamLaunchOptions{Trust: trustModeSandboxed}, probe)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, row := range rows {
		states[row.Member.Role] = row.State
	}
	if states["cto"] != "stopped" {
		t.Errorf("dead recorded cto state = %q, want stopped", states["cto"])
	}
	if states["dev"] != "live/config-diverged" {
		t.Errorf("live recorded dev state = %q, want live/config-diverged", states["dev"])
	}
}

func TestReconcileSimpleStartRolesRejectsDuplicateLive(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "dev", Handle: "dev", Binary: "codex"}}}
	makeRecord := func(handle string, pid int) simpleStartRecord {
		return simpleStartRecord{AgentDir: "/root/agents/" + handle, Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/repo", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "dev", Handle: handle, Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: pid,
		}}
	}
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "", false },
	}
	_, _, err := reconcileSimpleStartRoles(tm, team.DefaultProfile, "work", "/root", []simpleStartRecord{makeRecord("dev", 1), makeRecord("legacy-dev", 2)}, teamLaunchOptions{Trust: trustModeSandboxed}, probe)
	var conflict *simpleStartConflictError
	if !errors.As(err, &conflict) || conflict.Class != "duplicate_live" {
		t.Fatalf("error = %v, want duplicate_live conflict", err)
	}
}

func TestSimpleStartCheckpointWrapsSentinel(t *testing.T) {
	sentinel := errors.New("crash")
	err := callSimpleStartCheckpoint(func(got simpleStartCheckpoint) error {
		if got != simpleStartCheckpointPaneCreation {
			t.Fatalf("checkpoint = %q", got)
		}
		return sentinel
	}, simpleStartCheckpointPaneCreation)
	var checkpointErr *simpleStartCheckpointError
	if !errors.As(err, &checkpointErr) || !errors.Is(err, sentinel) {
		t.Fatalf("checkpoint error = %v", err)
	}
}
