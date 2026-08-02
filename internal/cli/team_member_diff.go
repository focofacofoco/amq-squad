package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// memberFieldChange is one field-level before/after pair in a `team member
// update --dry-run` preview (#616).
type memberFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// memberFieldUnset is what an empty field renders as. An empty string in a
// diff column is ambiguous — it reads as "unchanged" or as "the renderer had
// nothing to say" — and clearing a field is one of the edits an operator most
// wants confirmed before it happens.
const memberFieldUnset = "(unset)"

// memberDiffFields is the operator-visible surface of `team member update`, in
// a fixed display order. It is deliberately a curated list rather than
// reflection over team.Member: the struct also carries derived and internal
// bookkeeping (spawn origin/depth, tool policy drift) that this command cannot
// change and that would be noise in a review of a risky lead edit.
//
// Values are read from the ACTUAL before/after members rather than from the
// flags that were set. That matters for --effort, which the operator passes as
// one flag but which lands in claude_args/codex_args: a flag-driven diff would
// report "effort" and hide what was really written to the profile.
var memberDiffFields = []struct {
	name  string
	value func(team.Member) string
}{
	{"binary", func(m team.Member) string { return m.Binary }},
	{"handle", func(m team.Member) string { return m.Handle }},
	{"session", func(m team.Member) string { return m.Session }},
	{"model", func(m team.Member) string { return m.Model }},
	{"cwd", func(m team.Member) string { return m.CWD }},
	{"actor_mode", func(m team.Member) string { return m.ActorMode }},
	{"claude_args", func(m team.Member) string { return strings.Join(m.ClaudeArgs, " ") }},
	{"codex_args", func(m team.Member) string { return strings.Join(m.CodexArgs, " ") }},
}

// memberFieldChanges returns only the fields that actually differ. An update
// that changes nothing returns an empty slice, which callers must report as
// such rather than printing an empty table.
func memberFieldChanges(before, after team.Member) []memberFieldChange {
	var changes []memberFieldChange
	for _, field := range memberDiffFields {
		was, now := field.value(before), field.value(after)
		if was == now {
			continue
		}
		changes = append(changes, memberFieldChange{
			Field:  field.name,
			Before: displayMemberFieldValue(was),
			After:  displayMemberFieldValue(now),
		})
	}
	return changes
}

func displayMemberFieldValue(v string) string {
	if strings.TrimSpace(v) == "" {
		return memberFieldUnset
	}
	return v
}

// writeMemberUpdatePreview renders the dry-run preview: a header naming the
// member and profile, then one aligned row per changed field.
//
// Column widths are computed from the actual values because member cwd is an
// absolute worktree path and model/args are unbounded; a fixed-width table
// would wrap exactly on the rows an operator most needs to read.
func writeMemberUpdatePreview(w io.Writer, role, profile string, changes []memberFieldChange) {
	if len(changes) == 0 {
		fmt.Fprintf(w, "# preview: %s in profile %s is already in the requested state — no field would change, nothing would be written.\n", role, profile)
		return
	}
	noun := "fields"
	if len(changes) == 1 {
		noun = "field"
	}
	fmt.Fprintf(w, "# preview: update %s in profile %s — %d %s would change, nothing written yet.\n\n", role, profile, len(changes), noun)

	fieldWidth, beforeWidth := len("FIELD"), len("BEFORE")
	for _, c := range changes {
		if len(c.Field) > fieldWidth {
			fieldWidth = len(c.Field)
		}
		if len(c.Before) > beforeWidth {
			beforeWidth = len(c.Before)
		}
	}
	fmt.Fprintf(w, "  %-*s  %-*s  %s\n", fieldWidth, "FIELD", beforeWidth, "BEFORE", "AFTER")
	for _, c := range changes {
		fmt.Fprintf(w, "  %-*s  %-*s  %s\n", fieldWidth, c.Field, beforeWidth, c.Before, c.After)
	}
	fmt.Fprintf(w, "\nRe-run without --dry-run to apply.\n")
}
