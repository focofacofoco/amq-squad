package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// GUARD ON AN ADJACENT PROPERTY — NOT EVIDENCE FOR THIS PR'S FIX.
//
// Both tests in this file PASS at this PR's parent. They do not bisect anything
// here and are not offered as evidence. They are kept because #622's fix DEPENDS
// on the property they assert: rendering from accepted state is only correct if
// the accepted state's routing block is itself correct, and that rests on the
// path canonicalization #617/#621 landed. Nothing else asserts it.
//
// A defensive samePath/sameLaunchTarget change originally shipped alongside
// these. It was dropped: #621 canonicalizes the profile path and run start
// restamps recorded member CWDs on ingestion, so both operands are already
// canonical by the time the comparison runs and the defensive change had no
// reachable failing state. Hardening with no failing test does not ship.
//
// TestAcceptedPromptNeverRendersUnroutablePeers asserts a property worth stating
// independently of digest equality.
//
// The digest tests would catch a REGRESSION of this, but only as "the two
// prompts differ". They would not catch both renders collapsing together, which
// is a strictly worse outcome: agreeing prompts that both tell every agent its
// peers are unreachable would pass a byte-equality test while shipping a squad
// that cannot coordinate.
//
// The failure this pins is real and was live at f8b0911: samePath compared
// rec.CWD (resolved via canonicalDir) against m.EffectiveCWD (raw from
// team.json). Under a symlinked or differently-cased path those are two
// spellings of one directory, the comparison failed, routeCommandFor took the
// !sameCWD branch, project identity read as unknown, and EVERY peer line became
// "send: unavailable (AMQ project identity is missing ...)".
//
// The fixture runs under t.TempDir(), which on macOS lives beneath the
// /var -> /private/var symlink, so it exercises the resolved-vs-raw divergence
// naturally rather than by contriving one.
func TestAcceptedPromptNeverRendersUnroutablePeers(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		SharedCwdException: "probe fixture: single-directory squad matching the #618 field-hit profile",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Accepted prompts must route to peers sharing the launch cwd",
			"--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare fixture: %v", err)
	}

	profile, session := team.DefaultProfile, "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = initial
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}

	for _, member := range initial {
		prompt, err := preparedBootstrap(dir, profile, session, binding, tm, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		if err != nil {
			t.Fatalf("accepted render for %s: %v", member.Role, err)
		}
		if !strings.Contains(prompt, "Current team routing:") {
			t.Fatalf("role %s: accepted prompt has no routing block at all", member.Role)
		}
		if strings.Contains(prompt, "send: unavailable") {
			t.Errorf("role %s: ACCEPTED prompt reports peers as unroutable while every member"+
				" shares the launch cwd; the routing comparison is trusting a recorded"+
				" spelling instead of a canonical one (#618)", member.Role)
			for _, line := range strings.Split(prompt, "\n") {
				if strings.Contains(line, "send:") {
					t.Errorf("  %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestAcceptedPromptRoutesPeersWithExplicitlyRecordedCWD is the bisecting
// regression for the canonical-comparison fix, and it exists because the
// ORIGINAL one stopped bisecting.
//
// The first version of this regression used members with no explicit CWD, so
// both operands of the samePath comparison derived from the profile's Project
// path. #617/#621 then canonicalized that path, which made both sides agree and
// fixed the routing collapse from the other side of the same seam. The test kept
// passing at its parent and therefore proved nothing about THIS change.
//
// A member with an EXPLICITLY RECORDED CWD is the case #621 does not reach. The
// recorded spelling comes straight from team.json and is compared against a
// canonicalDir-resolved rec.CWD, so a member whose recorded path is the
// symlinked spelling of the same directory still compares unequal without this
// fix. On macOS t.TempDir() lives under /var -> /private/var, so recording the
// unresolved spelling exercises it without contriving anything.
func TestAcceptedPromptRoutesPeersWithExplicitlyRecordedCWD(t *testing.T) {
	dir := t.TempDir()
	// Record the UNRESOLVED spelling deliberately: this is what an operator's
	// shell hands the tool, and what os.Getwd reports when $PWD carries it.
	if err := team.Write(dir, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		SharedCwdException: "regression fixture: one checkout, recorded unresolved",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: dir},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: dir},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: dir, ToolProfile: team.ToolProfileFull},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Peers sharing an explicitly recorded cwd must stay routable",
			"--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare fixture: %v", err)
	}

	profile, session := team.DefaultProfile, "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = initial
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	for _, member := range initial {
		prompt, err := preparedBootstrap(dir, profile, session, binding, tm, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		if err != nil {
			t.Fatalf("accepted render for %s: %v", member.Role, err)
		}
		if strings.Contains(prompt, "send: unavailable") {
			t.Errorf("role %s: peers reported unroutable although every member records the SAME"+
				" directory; the comparison is trusting the recorded spelling (#618)", member.Role)
			for _, line := range strings.Split(prompt, "\n") {
				if strings.Contains(line, "send:") {
					t.Errorf("  %s", strings.TrimSpace(line))
				}
			}
		}
	}
}
