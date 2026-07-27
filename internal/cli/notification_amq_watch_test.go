package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestManagedNotificationAMQWatchCollectsExactMailboxOncePerSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	project := t.TempDir()
	execution := notificationWatcherExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "run",
		BaseRoot:   filepath.Join(project, "mail"),
	}
	amqCtx := notificationAMQContext(execution, team.DefaultProfile, "user")
	var watches atomic.Int32
	var collects atomic.Int32
	watch := func(ctx context.Context, got amqContext, timeout time.Duration) (bool, error) {
		if got.Root != filepath.Join(execution.BaseRoot, execution.Session) || got.Me != "user" || got.PinMode != amqPinExactRoot {
			t.Errorf("watch context = %+v", got)
		}
		if timeout != 20*time.Millisecond {
			t.Errorf("watch timeout = %s", timeout)
		}
		if watches.Add(1) == 1 {
			return true, nil
		}
		<-ctx.Done()
		return false, ctx.Err()
	}
	collect := func(_ context.Context, got amqContext) (bool, error) {
		if got.Root != amqCtx.Root || got.Me != amqCtx.Me {
			t.Errorf("collect context = %+v", got)
		}
		collects.Add(1)
		return true, nil
	}
	results := make(chan notificationAMQWatchResult, 1)
	go runManagedNotificationAMQWatch(ctx, amqCtx, 20*time.Millisecond, 3, time.Millisecond, 2*time.Millisecond, watch, collect, results)
	result := <-results
	if !result.Signaled || !result.CollectAttempted || !result.Collected || result.Err != nil {
		t.Fatalf("result = %+v", result)
	}
	if collects.Load() != 1 {
		t.Fatalf("collect calls = %d", collects.Load())
	}
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("unexpected result after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("managed watch did not stop after cancellation")
	}
}

func TestManagedNotificationAMQWatchBoundsCrashRestarts(t *testing.T) {
	results := make(chan notificationAMQWatchResult, 1)
	var calls atomic.Int32
	var collectCalled atomic.Bool
	go runManagedNotificationAMQWatch(
		context.Background(),
		amqContext{},
		time.Millisecond,
		3,
		time.Millisecond,
		2*time.Millisecond,
		func(context.Context, amqContext, time.Duration) (bool, error) {
			calls.Add(1)
			return false, errors.New("watch crashed")
		},
		func(context.Context, amqContext) (bool, error) {
			collectCalled.Store(true)
			return false, errors.New("unexpected collect")
		},
		results,
	)
	for want := 1; want <= 3; want++ {
		result := <-results
		if result.Failures != want || result.Exhausted != (want == 3) || !strings.Contains(result.Err.Error(), "watch crashed") {
			t.Fatalf("attempt %d result = %+v", want, result)
		}
	}
	if _, ok := <-results; ok {
		t.Fatal("managed watch remained active after bounded exhaustion")
	}
	if calls.Load() != 3 {
		t.Fatalf("watch calls = %d", calls.Load())
	}
	if collectCalled.Load() {
		t.Fatal("collect called after failed watch")
	}
}

func TestManagedNotificationAMQWatchRecoveryResetsFailureCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan notificationAMQWatchResult, 2)
	var calls atomic.Int32
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		time.Millisecond,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			switch calls.Add(1) {
			case 1:
				return false, errors.New("temporary")
			case 2:
				return false, nil
			default:
				<-ctx.Done()
				return false, ctx.Err()
			}
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	failed := <-results
	recovered := <-results
	if failed.Failures != 1 || failed.Err == nil {
		t.Fatalf("failure result = %+v", failed)
	}
	if recovered.Err != nil || recovered.Failures != 0 || recovered.Signaled {
		t.Fatalf("recovery result = %+v", recovered)
	}
	cancel()
}

func TestManagedNotificationAMQWatchOwnershipLossIsFatalWithoutRetry(t *testing.T) {
	results := make(chan notificationAMQWatchResult, 1)
	var calls atomic.Int32
	go runManagedNotificationAMQWatch(
		context.Background(),
		amqContext{},
		time.Second,
		5,
		time.Millisecond,
		time.Millisecond,
		func(context.Context, amqContext, time.Duration) (bool, error) {
			calls.Add(1)
			return false, fmt.Errorf("%w: replaced generation", errNotificationAMQOwnershipLost)
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	result := <-results
	if !result.Fatal || !result.Exhausted || result.Failures != 1 ||
		!errors.Is(result.Err, errNotificationAMQOwnershipLost) {
		t.Fatalf("ownership-loss result = %+v", result)
	}
	if _, ok := <-results; ok {
		t.Fatal("ownership loss did not stop managed watch")
	}
	if calls.Load() != 1 {
		t.Fatalf("ownership-loss retries = %d", calls.Load())
	}
}

func TestManagedNotificationAMQWatchStopCancelsBlockedWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	results := make(chan notificationAMQWatchResult, 1)
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		time.Hour,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			close(started)
			<-ctx.Done()
			return false, ctx.Err()
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	<-started
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("cancellation produced a retry result")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked managed watch did not stop")
	}
}

func TestValidateNotificationAMQContextRejectsWrongRoot(t *testing.T) {
	project := t.TempDir()
	execution := notificationWatcherExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "run",
		BaseRoot:   filepath.Join(project, "mail"),
	}
	ctx := notificationAMQContext(execution, team.DefaultProfile, "user")
	ctx.Root = filepath.Join(project, "other", "run")
	if err := validateNotificationAMQContext(ctx, execution, team.DefaultProfile, "user"); err == nil || !strings.Contains(err.Error(), "exact project/profile/session/operator") {
		t.Fatalf("wrong-root validation error = %v", err)
	}
}

func TestRunNotificationAMQWatchWaitsForMissingRootWithoutCreatingIt(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, "mail", "run")
	signaled, err := runNotificationAMQWatch(context.Background(), amqContext{
		ProjectDir: project,
		Root:       root,
		Me:         "user",
		PinMode:    amqPinExactRoot,
	}, time.Millisecond)
	if err != nil || signaled {
		t.Fatalf("missing-root watch signaled=%v err=%v", signaled, err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("managed watch created or altered missing root: %v", statErr)
	}
}

func TestExecuteCollectDrainContextStopsBeforeJournalMutation(t *testing.T) {
	project := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	amqCtx := amqContext{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Env:        amqEnv{SessionName: "run"},
		Root:       filepath.Join(project, "mail", "run"),
		Me:         "user",
		Session:    "run",
		PinMode:    amqPinExactRoot,
	}
	if _, err := executeCollectDrainContext(ctx, io.Discard, amqCtx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled collect error = %v", err)
	}
	if _, err := os.Stat(newCollectJournal(amqCtx).Root); !os.IsNotExist(err) {
		t.Fatalf("canceled collect mutated journal: %v", err)
	}
}

func TestNotificationWatcherViewIncludesManagedAMQBackendState(t *testing.T) {
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion:   notificationWatcherSchema,
		Expected:        true,
		LeaseTTL:        time.Minute.String(),
		WatchBackend:    "amq-watch",
		WatchRoot:       "/tmp/mail/run",
		WatchMailbox:    "user",
		WatchTimeout:    "30s",
		WatchRestarts:   2,
		WatchMaxRetries: 5,
		LastWatchAt:     now.Add(-2 * time.Second),
		LastCollectAt:   now.Add(-time.Second),
	}
	status := notificationWatcherStatus{
		Enabled:         true,
		Health:          "degraded",
		PID:             42,
		LeaseExpiresAt:  now.Add(time.Minute),
		WatchBackend:    rec.WatchBackend,
		WatchRoot:       rec.WatchRoot,
		WatchMailbox:    rec.WatchMailbox,
		WatchRestarts:   rec.WatchRestarts,
		WatchMaxRetries: rec.WatchMaxRetries,
		LastWatchAt:     rec.LastWatchAt,
		LastCollectAt:   rec.LastCollectAt,
		record:          rec,
	}
	view := buildNotificationWatcherView(true, status, now)
	if !view.Running || view.WatchBackend != "amq-watch" || view.WatchMailbox != "user" ||
		view.WatchRestarts != 2 || view.WatchMaxRetries != 5 ||
		!view.LastWatchAt.Equal(rec.LastWatchAt) || !view.LastCollectAt.Equal(rec.LastCollectAt) {
		t.Fatalf("managed watcher view = %+v", view)
	}
}

func TestNotificationWatcherDuplicateAMQSignalsDoNotNotifyWithoutCollection(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	var watchCalls atomic.Int32
	var collectCalls atomic.Int32
	var scans atomic.Int32
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "amq-duplicate",
			TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: time.Hour,
			Stop: stop,
			WatchAMQ: func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
				if watchCalls.Add(1) <= 2 {
					return true, nil
				}
				<-ctx.Done()
				return false, ctx.Err()
			},
			CollectAMQ: func(context.Context, amqContext) (bool, error) {
				collectCalls.Add(1)
				return false, nil
			},
			AMQWatchTimeout: time.Second, AMQWatchMaxRetries: 3,
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				scans.Add(1)
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "healthy" && collectCalls.Load() == 2 && !r.LastCollectAt.IsZero()
	})
	if scans.Load() != 1 {
		t.Fatalf("duplicate empty AMQ signals triggered %d notification scans; record=%+v", scans.Load(), rec)
	}
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherAMQExhaustionIsVisibleAndKeepsFallbackActive(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "amq-exhausted",
			TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: 20 * time.Millisecond,
			Stop: stop,
			WatchAMQ: func(context.Context, amqContext, time.Duration) (bool, error) {
				return false, errors.New("amq watch unsupported")
			},
			CollectAMQ: func(context.Context, amqContext) (bool, error) {
				return false, errors.New("collect must not run")
			},
			AMQWatchTimeout: time.Second, AMQWatchMaxRetries: 2,
			AMQWatchBackoff: time.Millisecond, AMQWatchMaxBackoff: time.Millisecond,
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "degraded" && r.WatchRestarts == 2 &&
			strings.Contains(r.LastError, "managed AMQ watch exhausted") &&
			!r.LastScanAt.IsZero()
	})
	if rec.WatchBackend != "amq-watch" || rec.WatchMailbox != "user" ||
		rec.WatchMaxRetries != 2 || !strings.Contains(rec.LastError, "fsnotify/rescan fallback remains active") {
		t.Fatalf("exhausted watcher record = %+v", rec)
	}
	firstScan := rec.LastScanAt
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.LastScanAt.After(firstScan)
	})
	stopTestNotificationWatcher(t, stop, done)
}
