package cli

import (
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
)

type notificationWatcherView struct {
	PolicyEnabled   bool      `json:"policy_enabled"`
	Expected        bool      `json:"expected"`
	Running         bool      `json:"running"`
	Health          string    `json:"health"`
	Reason          string    `json:"reason,omitempty"`
	RuntimePath     string    `json:"runtime_path"`
	SchemaVersion   int       `json:"schema_version,omitempty"`
	PID             int       `json:"pid,omitempty"`
	Host            string    `json:"host,omitempty"`
	Owner           string    `json:"owner,omitempty"`
	LeaseTTL        string    `json:"lease_ttl,omitempty"`
	LeaseExpiresAt  time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt     time.Time `json:"heartbeat_at,omitempty"`
	LastScanAt      time.Time `json:"last_scan_at,omitempty"`
	LastEventAt     time.Time `json:"last_event_at,omitempty"`
	WatchBackend    string    `json:"watch_backend,omitempty"`
	WatchRoot       string    `json:"watch_root,omitempty"`
	WatchMailbox    string    `json:"watch_mailbox,omitempty"`
	WatchTimeout    string    `json:"watch_timeout,omitempty"`
	WatchRunning    bool      `json:"watch_running,omitempty"`
	WatchRestarts   int       `json:"watch_restarts,omitempty"`
	WatchFailures   int       `json:"watch_failure_streak,omitempty"`
	CollectPending  bool      `json:"collect_pending,omitempty"`
	CollectRetries  int       `json:"collect_retries,omitempty"`
	WatchMaxRetries int       `json:"watch_max_retries,omitempty"`
	LastWatchAt     time.Time `json:"last_watch_at,omitempty"`
	LastCollectAt   time.Time `json:"last_collect_at,omitempty"`
	StatePath       string    `json:"state_path,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

func buildNotificationWatcherView(expected bool, watcher notificationWatcherStatus, now time.Time) notificationWatcherView {
	rec := watcher.record
	runtimeExpected := rec.SchemaVersion == notificationWatcherSchema && rec.Expected
	backendRunning := rec.WatchBackend == "" || rec.WatchRunning
	running := runtimeExpected && backendRunning && watcher.PID > 0 && now.Before(watcher.LeaseExpiresAt) && (watcher.Health == "healthy" || watcher.Health == "external-active" || watcher.Health == "degraded")
	return notificationWatcherView{
		PolicyEnabled: expected, Expected: runtimeExpected, Running: running, Health: watcher.Health, Reason: boundedNotificationText(watcher.Reason),
		RuntimePath: watcher.RuntimePath, SchemaVersion: watcher.SchemaVersion, PID: watcher.PID,
		Host: watcher.Host, Owner: watcher.Owner, LeaseTTL: rec.LeaseTTL,
		LeaseExpiresAt: watcher.LeaseExpiresAt, HeartbeatAt: watcher.HeartbeatAt,
		LastScanAt: watcher.LastScanAt, LastEventAt: rec.LastEventAt, StatePath: watcher.StatePath,
		WatchBackend: rec.WatchBackend, WatchRoot: rec.WatchRoot, WatchMailbox: rec.WatchMailbox,
		WatchTimeout: rec.WatchTimeout, WatchRunning: rec.WatchRunning,
		WatchRestarts: rec.WatchRestarts, WatchFailures: rec.WatchFailures,
		CollectPending: rec.CollectPending, CollectRetries: rec.CollectRetries,
		WatchMaxRetries: rec.WatchMaxRetries,
		LastWatchAt:     rec.LastWatchAt, LastCollectAt: rec.LastCollectAt,
		LastError: boundedNotificationText(rec.LastError),
	}
}

func boundedNotificationText(text string) string {
	return attention.NormalizeDeliveryError(text)
}
