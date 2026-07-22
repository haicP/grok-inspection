package main

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	scheduleStatusOK          = "ok"
	scheduleStatusSkippedBusy = "skipped_busy"
	scheduleStatusCancelled   = "cancelled"
	scheduleStatusError       = "error"
	scheduleStatusIdle        = ""
)

// schedulePollInterval is how often the loop checks whether a run is due.
// Overridable in tests.
var schedulePollInterval = time.Second

type scheduleSnapshot struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
	NextAt          string `json:"next_at,omitempty"`
	LastStartedAt   string `json:"last_started_at,omitempty"`
	LastFinishedAt  string `json:"last_finished_at,omitempty"`
	LastStatus      string `json:"last_status,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	InProgress      bool   `json:"in_progress,omitempty"`
}

type scheduleRuntime struct {
	mu           sync.Mutex
	nextAt       time.Time
	lastStarted  time.Time
	lastFinished time.Time
	lastStatus   string
	lastError    string
	inProgress   bool
}

var (
	scheduleState scheduleRuntime

	scheduleOnce   sync.Once
	scheduleStopMu sync.Mutex
	scheduleStop   chan struct{}
	scheduleDone   chan struct{}
	scheduleWake   chan struct{}
)

func startScheduleLoop() {
	scheduleOnce.Do(func() {
		scheduleStop = make(chan struct{})
		scheduleDone = make(chan struct{})
		scheduleWake = make(chan struct{}, 1)
		// Seed next fire from current config (enabled → now+interval; disabled → zero).
		rescheduleNextFire(time.Now())
		go func() {
			defer close(scheduleDone)
			ticker := time.NewTicker(schedulePollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-scheduleStop:
					return
				case <-ticker.C:
					maybeRunSchedule(time.Now())
				case <-scheduleWake:
					// Config changed; next tick (or immediate maybe) uses new nextAt.
					maybeRunSchedule(time.Now())
				}
			}
		}()
	})
}

func stopScheduleLoop() {
	scheduleStopMu.Lock()
	defer scheduleStopMu.Unlock()
	if scheduleStop == nil {
		return
	}
	select {
	case <-scheduleStop:
		// already closed
	default:
		close(scheduleStop)
	}
	<-scheduleDone
	scheduleStop = nil
	scheduleDone = nil
	scheduleWake = nil
	// Allow tests to restart the loop after shutdown.
	scheduleOnce = sync.Once{}
}

func notifyScheduleConfigChanged() {
	rescheduleNextFire(time.Now())
	if scheduleWake == nil {
		return
	}
	select {
	case scheduleWake <- struct{}{}:
	default:
	}
}

func rescheduleNextFire(now time.Time) {
	cfg := loadedConfig()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	if !cfg.ScheduleEnabled {
		scheduleState.nextAt = time.Time{}
		return
	}
	mins := cfg.ScheduleIntervalMinutes
	if mins <= 0 {
		mins = defaultScheduleIntervalMinutes
	}
	scheduleState.nextAt = now.Add(time.Duration(mins) * time.Minute)
}

func scheduleStatus() scheduleSnapshot {
	cfg := loadedConfig()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	snap := scheduleSnapshot{
		Enabled:         cfg.ScheduleEnabled,
		IntervalMinutes: cfg.ScheduleIntervalMinutes,
		LastStatus:      scheduleState.lastStatus,
		LastError:       scheduleState.lastError,
		InProgress:      scheduleState.inProgress,
	}
	if snap.IntervalMinutes <= 0 {
		snap.IntervalMinutes = defaultScheduleIntervalMinutes
	}
	if !scheduleState.nextAt.IsZero() {
		snap.NextAt = scheduleState.nextAt.Format(time.RFC3339)
	}
	if !scheduleState.lastStarted.IsZero() {
		snap.LastStartedAt = scheduleState.lastStarted.Format(time.RFC3339)
	}
	if !scheduleState.lastFinished.IsZero() {
		snap.LastFinishedAt = scheduleState.lastFinished.Format(time.RFC3339)
	}
	return snap
}

func setScheduleOutcome(status, errMsg string) {
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	scheduleState.lastStatus = status
	scheduleState.lastError = errMsg
	scheduleState.lastFinished = time.Now()
}

func maybeRunSchedule(now time.Time) {
	cfg := loadedConfig()
	if !cfg.ScheduleEnabled {
		return
	}

	scheduleState.mu.Lock()
	if scheduleState.inProgress {
		scheduleState.mu.Unlock()
		return
	}
	if scheduleState.nextAt.IsZero() || now.Before(scheduleState.nextAt) {
		scheduleState.mu.Unlock()
		return
	}
	// Advance next fire immediately so a long run does not double-trigger.
	mins := cfg.ScheduleIntervalMinutes
	if mins <= 0 {
		mins = defaultScheduleIntervalMinutes
	}
	scheduleState.nextAt = now.Add(time.Duration(mins) * time.Minute)
	scheduleState.inProgress = true
	scheduleState.lastStarted = now
	scheduleState.lastStatus = ""
	scheduleState.lastError = ""
	scheduleState.mu.Unlock()

	defer func() {
		scheduleState.mu.Lock()
		scheduleState.inProgress = false
		if scheduleState.lastFinished.IsZero() || scheduleState.lastFinished.Before(scheduleState.lastStarted) {
			scheduleState.lastFinished = time.Now()
		}
		scheduleState.mu.Unlock()
	}()

	if scheduleStopped() {
		setScheduleOutcome(scheduleStatusCancelled, "")
		return
	}
	if engine.busyForSchedule() {
		setScheduleOutcome(scheduleStatusSkippedBusy, "")
		slog.Info("grok-inspection: scheduled inspection skipped (busy)")
		return
	}

	errStart := engine.start(startRequest{
		Lang:            string(LangZH),
		Workers:         maxWorkers,
		IncludeDisabled: false,
		OnlyDisabled:    false,
		Incremental:     false,
	})
	if errStart != nil {
		if isScheduleBusyError(errStart) {
			setScheduleOutcome(scheduleStatusSkippedBusy, errStart.Error())
			return
		}
		setScheduleOutcome(scheduleStatusError, errStart.Error())
		slog.Warn("grok-inspection: scheduled inspection failed to start", "error", errStart)
		return
	}

	waitWhile(func() bool { return engine.inspectionActive() && !scheduleStopped() })
	if scheduleStopped() {
		setScheduleOutcome(scheduleStatusCancelled, "")
		return
	}
	if engine.lastRunWasStopped() {
		setScheduleOutcome(scheduleStatusCancelled, "")
		slog.Info("grok-inspection: scheduled inspection cancelled before apply")
		return
	}

	password := resolveManagementPassword(nil)
	errApply := engine.startApply(applyRequest{Lang: string(LangZH)}, password, nil)
	if errApply != nil {
		if isNoRecommendedError(errApply) {
			setScheduleOutcome(scheduleStatusOK, "")
			return
		}
		if isScheduleBusyError(errApply) {
			setScheduleOutcome(scheduleStatusSkippedBusy, errApply.Error())
			return
		}
		setScheduleOutcome(scheduleStatusError, errApply.Error())
		slog.Warn("grok-inspection: scheduled apply failed to start", "error", errApply)
		return
	}

	waitWhile(func() bool { return engine.applyActive() && !scheduleStopped() })
	if scheduleStopped() {
		setScheduleOutcome(scheduleStatusCancelled, "")
		return
	}

	snap := engine.snapshot(false)
	if len(snap.ApplyFailures) > 0 {
		msg := strings.Join(snap.ApplyFailures, "; ")
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		setScheduleOutcome(scheduleStatusError, msg)
		slog.Warn("grok-inspection: scheduled apply finished with failures", "count", len(snap.ApplyFailures))
		return
	}
	setScheduleOutcome(scheduleStatusOK, "")
	slog.Info("grok-inspection: scheduled inspection completed")
}

func scheduleStopped() bool {
	scheduleStopMu.Lock()
	ch := scheduleStop
	scheduleStopMu.Unlock()
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func waitWhile(cond func() bool) {
	for cond() {
		if scheduleStopped() {
			return
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-scheduleStop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func isNoRecommendedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == T(LangZH, "no_recommended_actions") || msg == T(LangEN, "no_recommended_actions")
}

func isScheduleBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	keys := []string{"already_running", "busy_generic", "busy_row_action", "busy_unban", "busy_inspection", "busy_apply"}
	for _, key := range keys {
		if msg == T(LangZH, key) || msg == T(LangEN, key) {
			return true
		}
	}
	// Typed conflict status without relying on language.
	var he *httpStatusError
	if errors.As(err, &he) && he != nil && he.status == 409 {
		return true
	}
	return false
}
