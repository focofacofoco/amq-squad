package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #597 guard 4: --effort was unusable with --from-profile.
//
// --roles and --from-profile are mutually exclusive roster sources, so under
// --from-profile the --roles-derived selection is empty and every --effort key
// read as "not selected by --roles". The guard was not wrong about its own
// predicate; it was being asked the wrong question.

func seedEffortSourceProfile(t *testing.T, project string) {
	t.Helper()
	if err := team.WriteProfile(project, "source-squad", team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "earlier"},
			{Role: "challenger", Binary: "claude", Handle: "challenger", Session: "earlier"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// preflightEffortIssue runs the real preflight entry point and returns any
// invalid-effort issue.
//
// It goes through runStartPreflight rather than calling the validator directly
// ON PURPOSE. The fix changes the validator's signature, so a direct unit test
// cannot compile at the parent commit, and a test that only fails to BUILD
// proves nothing about behavior. runStartPreflight's signature is stable, so
// these cases compile at the parent and fail there for the right reason.
func preflightEffortIssue(project, roles, fromProfile, effort string) string {
	res := runStartPreflight(runStartPreflightInput{
		Project: project, Profile: "target-squad", Session: "tonight",
		Roles: roles, FromProfile: fromProfile, FromProfileSet: fromProfile != "",
		Effort: effort, EffortSet: effort != "",
	})
	for _, issue := range res.Issues {
		if issue.Code == runStartPreflightInvalidEffort {
			return issue.Detail
		}
	}
	return ""
}

// TestRunStartEffortAcceptsRolesFromClonedProfile is the relaxation: an effort
// key naming a role that exists in the cloned roster is now accepted.
func TestRunStartEffortAcceptsRolesFromClonedProfile(t *testing.T) {
	project := t.TempDir()
	seedEffortSourceProfile(t, project)

	for _, effort := range []string{"cto=xhigh", "challenger=high", "cto=xhigh,challenger=low"} {
		if detail := preflightEffortIssue(project, "", "source-squad", effort); detail != "" {
			t.Errorf("--effort %q with --from-profile must be accepted, got: %s", effort, detail)
		}
	}
}

// TestRunStartEffortStillRefusesRolesInNeitherSource is the protection that had
// to survive the relaxation. Relaxing a guard is not deleting it: a typo'd role
// silently doing nothing is the failure this check exists to prevent, and it is
// still refused.
func TestRunStartEffortStillRefusesRolesInNeitherSource(t *testing.T) {
	project := t.TempDir()
	seedEffortSourceProfile(t, project)

	detail := preflightEffortIssue(project, "", "source-squad", "ctoo=xhigh")
	if detail == "" {
		t.Fatal("an --effort key naming a role in neither --roles nor the cloned roster must still be refused")
	}
	if !strings.Contains(detail, "ctoo") {
		t.Errorf("refusal must name the unknown role, got: %s", detail)
	}

	// And the pre-existing --roles path is untouched.
	if detail := preflightEffortIssue(project, "cto", "", "nope=high"); detail == "" {
		t.Error("--effort naming a role outside --roles must still be refused")
	}
}

// TestRunStartEffortMissingSourceProfileDefersToItsOwnRefusal keeps this check
// from burying a better error. A --from-profile that cannot be read has a
// dedicated refusal that says so; reporting it here as an effort problem would
// point the operator at the wrong flag.
func TestRunStartEffortMissingSourceProfileDefersToItsOwnRefusal(t *testing.T) {
	project := t.TempDir()
	if detail := preflightEffortIssue(project, "", "does-not-exist", "cto=xhigh"); detail != "" {
		t.Errorf("a missing source profile must not surface as an effort error: %s", detail)
	}
}
