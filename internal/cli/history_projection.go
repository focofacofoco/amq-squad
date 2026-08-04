package cli

import (
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

type historyRecord struct {
	Role         string    `json:"role"`
	Handle       string    `json:"handle"`
	Binary       string    `json:"binary"`
	Session      string    `json:"session"`
	Conversation string    `json:"conversation,omitempty"`
	Source       string    `json:"source"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	// Tmux is the persisted tmux runtime identity for the launch, plus a
	// computed pane_alive, so clients can tell which historical launches still
	// have a live pane to focus/resume. Omitted for records without tmux.
	Tmux *tmuxRuntimeJSON `json:"tmux,omitempty"`
	// liveness retains the source launch record and its shared runtime identity
	// for pane_alive rendering. It is intentionally not serialized.
	liveness agentLiveness
}

func historyRecordsFromEntries(entries []launch.Entry) []historyRecord {
	rows := make([]historyRecord, 0, len(entries))
	for _, e := range entries {
		r := e.Record
		runtimeIdentity := classifyLaunchRuntimeIdentity(r, r.Binary, "", launchRuntimeProbeFromDuplicate(defaultDuplicateLaunchProbe))
		rows = append(rows, historyRecord{
			Role:         r.Role,
			Handle:       r.Handle,
			Binary:       r.Binary,
			Session:      r.Session,
			Conversation: r.Conversation,
			Source:       sourceLabel(e.Source),
			CWD:          r.CWD,
			StartedAt:    r.StartedAt,
			Tmux:         tmuxRuntimeFromInfo(r.Tmux),
			liveness: agentLiveness{
				LaunchFound:     true,
				LaunchRecord:    r,
				RuntimeIdentity: runtimeIdentity,
			},
		})
	}
	// Resolve pane liveness once across all rows that recorded a tmux pane.
	var livePanes map[string]bool
	for i := range rows {
		if rows[i].Tmux != nil {
			if livePanes == nil {
				livePanes = livePaneIDSet(statusPaneLister)
			}
			fillPaneAliveFromLiveness(rows[i].Tmux, livePanes, &rows[i].liveness)
		}
	}
	return rows
}
