package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// operatorHandoffCard is the post-launch operator handoff card (#493): the last
// thing a successful launch prints, telling the human who they are in this
// session, where their attention belongs, and how decisions will reach them.
//
// It exists because "launched ✓" is not a handoff. In the 2026-07-18 session
// the squad finished its goal in 23 minutes and sent the operator an ACK plus
// two gate decisions; the human found them roughly 50 minutes later and asked
// why the CTO was not speaking to them. The profile had been configured
// operator_delivery=lead_pane, notifications=false, poll_required=false — a
// coherent configuration that nothing had ever explained to the person it
// depended on.
//
// Two properties are load-bearing:
//
//   - Every field derives from the profile's actual operator block. A card that
//     printed plausible defaults would reproduce the exact failure it exists to
//     prevent, and would do it while sounding authoritative.
//   - It renders to stdout unconditionally, never through quietNotice. The
//     launch notices above it are quiet-suppressible because they are progress
//     chatter; this is the one output that tells the operator they are on the
//     hook, and --quiet must not be able to turn it off (#493 safety output).
type operatorHandoffCard struct {
	// Enabled mirrors the profile's operator block. When false the card
	// reports that no gate will reach a human, which is a different and
	// equally important thing to be told.
	Enabled bool
	Handle  string
	Profile string
	Session string
	// Console is the human-readable attention surface for the configured
	// interaction mode, naming the actual lead role where one applies.
	Console string
	// Contract is the mode's own statement of how approvals are given.
	Contract string

	NotificationsEnabled bool
	SinkTypes            []string
	PollRequired         bool

	NextCommand    string
	MonitorCommand string
	AnswerCommand  string
	StatusCommand  string

	DisabledReason string
}

// newOperatorHandoffCard derives the card from the launched team's operator
// block and the exact profile/session that was just brought up. It takes the
// resolved team rather than re-reading the profile so the card describes the
// roster that actually launched.
func newOperatorHandoffCard(t team.Team, profile, session string) operatorHandoffCard {
	d := operatorDeliveryForTeam(t)
	next, monitor, answer, status := operatorHandoffCommands(profile, session)
	card := operatorHandoffCard{
		Enabled:        d.Enabled,
		Handle:         d.Handle,
		Profile:        strings.TrimSpace(profile),
		Session:        strings.TrimSpace(session),
		NextCommand:    next,
		MonitorCommand: monitor,
		AnswerCommand:  answer,
		StatusCommand:  status,
	}
	if !d.Enabled {
		card.DisabledReason = d.Reason
		return card
	}
	card.Console = operatorConsoleSurface(t, d)
	card.Contract = d.Contract
	card.NotificationsEnabled = d.NotificationsEnabled
	card.SinkTypes = d.NotificationSinkTypes
	card.PollRequired = d.PollRequired
	return card
}

// operatorHandoffCommands builds the four scoped commands the card prints.
// They carry --profile/--session but deliberately not --project: the card is
// printed into the terminal the operator launched from, whose cwd is already
// the project, and the adjacent "next:" launch notice scopes itself the same
// way. Keeping both consistent matters more than portability to another cwd.
func operatorHandoffCommands(profile, session string) (next, monitor, answer, status string) {
	scope := commandProfileArg(profile)
	if s := strings.TrimSpace(session); s != "" {
		scope += " --session " + shellQuote(s)
	}
	next = "amq-squad next" + scope
	monitor = "amq-squad monitor" + scope
	answer = "amq-squad operator answer" + scope + " --gate <topic> --approved|--denied [--reason TEXT]"
	status = "amq-squad status" + scope
	return next, monitor, answer, status
}

// operatorConsoleSurface names where this operator's attention is supposed to
// live. lead_pane names the actual lead role: "the lead pane" is not actionable
// to someone looking at a grid of identical panes.
func operatorConsoleSurface(t team.Team, d operatorDeliveryData) string {
	switch d.InteractionMode {
	case team.OperatorInteractionLeadPane:
		if lead := strings.TrimSpace(t.Lead); lead != "" {
			return fmt.Sprintf("the lead pane (role %s)", lead)
		}
		return "the lead pane"
	case team.OperatorInteractionSeparateTerminal:
		return "a separate terminal — this one; the squad runs in its own panes"
	case team.OperatorInteractionNOC:
		return "the NOC/global board"
	case team.OperatorInteractionSelfOperator:
		return fmt.Sprintf("the lead, under self-operator policy; only allowlisted gates are delegated, the rest still come to %s", d.Handle)
	default:
		return "not configured — no console was assigned to you"
	}
}

const operatorHandoffCardRule = "────────────────────────────────────────────────────────────────────"

// operatorHandoffCardField renders one "Label   value" row. Every label shares
// an 8-column field so continuation lines can align under the value with a
// fixed 10-space indent; see operatorHandoffCardWrapIndent.
func operatorHandoffCardField(w io.Writer, label, value string) {
	fmt.Fprintf(w, " %-8s %s\n", label, value)
}

const operatorHandoffCardWrapIndent = "          "

// writeOperatorHandoffCard renders the card. Callers must pass os.Stdout
// directly rather than routing through quietNotice; see the type doc.
//
// Commands are never inlined after a label. They are long enough (a scoped
// `operator answer` runs past 110 columns) that a label prefix guarantees an
// ugly soft-wrap, and a soft-wrapped command is one a hurried operator
// copy-pastes incorrectly. Each command gets its own full-width line under a
// short comment instead.
func writeOperatorHandoffCard(w io.Writer, c operatorHandoffCard) {
	fmt.Fprintf(w, "\n%s\n", operatorHandoffCardRule)
	if !c.Enabled {
		fmt.Fprintf(w, " OPERATOR GATES ARE DISABLED FOR THIS PROFILE\n")
		fmt.Fprintf(w, "%s\n", operatorHandoffCardRule)
		if c.DisabledReason != "" {
			fmt.Fprintf(w, " %s.\n", c.DisabledReason)
		}
		fmt.Fprintf(w, " No gate thread will be routed to a human for this session; the\n")
		fmt.Fprintf(w, " team lead owns these decisions under the team rules.\n\n")
		fmt.Fprintf(w, " Check the board with:\n   %s\n", c.StatusCommand)
		fmt.Fprintf(w, "%s\n\n", operatorHandoffCardRule)
		return
	}

	fmt.Fprintf(w, " YOU ARE THE OPERATOR FOR THIS SESSION\n")
	fmt.Fprintf(w, "%s\n", operatorHandoffCardRule)
	operatorHandoffCardField(w, "Handle", fmt.Sprintf("%s — messages addressed to %q are for you.", c.Handle, c.Handle))
	operatorHandoffCardField(w, "Console", c.Console)
	operatorHandoffCardField(w, "Gates", fmt.Sprintf("durable gate/<topic> threads addressed to %q.", c.Handle))
	fmt.Fprintf(w, "%sThey do not expire, and they do not resend.\n", operatorHandoffCardWrapIndent)
	if c.Contract != "" {
		fmt.Fprintf(w, "%sHow you answer: %s.\n", operatorHandoffCardWrapIndent, c.Contract)
	}
	fmt.Fprintln(w)

	if c.NotificationsEnabled {
		sinks := "an unnamed sink"
		if len(c.SinkTypes) > 0 {
			sinks = strings.Join(c.SinkTypes, ", ")
		}
		operatorHandoffCardField(w, "ALERTS", fmt.Sprintf("ON via %s.", sinks))
		fmt.Fprintf(w, "%sThe launch-host watcher will notify you. Its health is\n", operatorHandoffCardWrapIndent)
		fmt.Fprintf(w, "%sreported by `amq-squad status`, not by this card.\n", operatorHandoffCardWrapIndent)
	} else {
		operatorHandoffCardField(w, "ALERTS", "OFF — NOTHING WILL INTERRUPT YOU.")
		fmt.Fprintf(w, "%sNo alert of any kind is configured for this session. If you\n", operatorHandoffCardWrapIndent)
		fmt.Fprintf(w, "%swalk away, the squad's questions wait silently until you come\n", operatorHandoffCardWrapIndent)
		fmt.Fprintf(w, "%sback and look. Polling is on you.\n", operatorHandoffCardWrapIndent)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, " What to run:\n")
	fmt.Fprintf(w, "   # what needs you right now\n   %s\n", c.NextCommand)
	fmt.Fprintf(w, "   # block until something needs you\n   %s\n", c.MonitorCommand)
	fmt.Fprintf(w, "   # answer a gate\n   %s\n", c.AnswerCommand)
	fmt.Fprintf(w, "   # the board\n   %s\n", c.StatusCommand)
	fmt.Fprintf(w, "%s\n\n", operatorHandoffCardRule)
}

// printOperatorHandoffCard is the single call site helper used by the launch
// routes. It writes to stdout on purpose: see operatorHandoffCard's doc for why
// --quiet must not suppress this.
func printOperatorHandoffCard(w io.Writer, t team.Team, profile, session string) {
	writeOperatorHandoffCard(w, newOperatorHandoffCard(t, profile, session))
}
