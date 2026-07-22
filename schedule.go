package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
	// ServerNow is the plugin host clock (RFC3339); clients should not use browser time for countdown.
	ServerNow      string `json:"server_now"`
	NextAt         string `json:"next_at,omitempty"`
	LastStartedAt  string `json:"last_started_at,omitempty"`
	LastFinishedAt string `json:"last_finished_at,omitempty"`
	LastStatus     string `json:"last_status,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	InProgress     bool   `json:"in_progress,omitempty"`
}

type scheduleRuntime struct {
	mu           sync.Mutex
	nextAt       time.Time
	lastStarted  time.Time
	lastFinished time.Time
	lastStatus   string
	lastError    string
	inProgress   bool
	loaded       bool
}

// schedulePersisted is durable server-side schedule bookkeeping (not browser-local).
type schedulePersisted struct {
	NextAt         string `json:"next_at,omitempty"`
	LastStartedAt  string `json:"last_started_at,omitempty"`
	LastFinishedAt string `json:"last_finished_at,omitempty"`
	LastStatus     string `json:"last_status,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

var (
	scheduleState scheduleRuntime

	scheduleOnce   sync.Once
	scheduleStopMu sync.Mutex
	scheduleStop   chan struct{}
	scheduleDone   chan struct{}
	scheduleWake   chan struct{}
)

func scheduleStateFile(cfg pluginConfig) string {
	if strings.TrimSpace(cfg.StateFile) != "" {
		return filepath.Join(filepath.Dir(cfg.StateFile), "schedule.json")
	}
	if dir := strings.TrimSpace(os.Getenv("GROK_INSPECTION_DATA_DIR")); dir != "" {
		return filepath.Join(dir, "schedule.json")
	}
	return filepath.Join("data", "grok-inspection", "schedule.json")
}

func parseRFC3339(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func loadScheduleStateLocked(cfg pluginConfig) {
	if scheduleState.loaded {
		return
	}
	scheduleState.loaded = true
	path := scheduleStateFile(cfg)
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return
	}
	var p schedulePersisted
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	scheduleState.nextAt = parseRFC3339(p.NextAt)
	scheduleState.lastStarted = parseRFC3339(p.LastStartedAt)
	scheduleState.lastFinished = parseRFC3339(p.LastFinishedAt)
	scheduleState.lastStatus = strings.TrimSpace(p.LastStatus)
	scheduleState.lastError = strings.TrimSpace(p.LastError)
}

func persistScheduleStateLocked(cfg pluginConfig) {
	path := scheduleStateFile(cfg)
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Warn("grok-inspection: schedule state mkdir failed", "path", path, "error", err)
		return
	}
	payload := schedulePersisted{
		NextAt:         formatRFC3339(scheduleState.nextAt),
		LastStartedAt:  formatRFC3339(scheduleState.lastStarted),
		LastFinishedAt: formatRFC3339(scheduleState.lastFinished),
		LastStatus:     scheduleState.lastStatus,
		LastError:      scheduleState.lastError,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		slog.Warn("grok-inspection: schedule state save failed", "path", tmp, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		slog.Warn("grok-inspection: schedule state rename failed", "path", path, "error", err)
	}
}

func wakeScheduleLoop() {
	if scheduleWake == nil {
		return
	}
	select {
	case scheduleWake <- struct{}{}:
	default:
	}
}

// ensureScheduleRuntime loads durable state once and seeds next_at only when missing.
// Does NOT reset an existing future next_at (plugin.reconfigure / page open must not slide the timer).
func ensureScheduleRuntime(now time.Time) {
	cfg := loadedConfig()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	loadScheduleStateLocked(cfg)
	if !cfg.ScheduleEnabled {
		if !scheduleState.nextAt.IsZero() {
			scheduleState.nextAt = time.Time{}
			persistScheduleStateLocked(cfg)
		}
		return
	}
	mins := cfg.ScheduleIntervalMinutes
	if mins <= 0 {
		mins = defaultScheduleIntervalMinutes
	}
	// First enable / empty state: schedule once from server clock.
	if scheduleState.nextAt.IsZero() {
		scheduleState.nextAt = now.Add(time.Duration(mins) * time.Minute)
		persistScheduleStateLocked(cfg)
	}
}

func startScheduleLoop() {
	scheduleOnce.Do(func() {
		scheduleStop = make(chan struct{})
		scheduleDone = make(chan struct{})
		scheduleWake = make(chan struct{}, 1)
		ensureScheduleRuntime(time.Now())
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
	default:
		close(scheduleStop)
	}
	<-scheduleDone
	scheduleStop = nil
	scheduleDone = nil
	scheduleWake = nil
	scheduleOnce = sync.Once{}
	scheduleState.mu.Lock()
	scheduleState.loaded = false
	scheduleState.mu.Unlock()
}

// applyScheduleSettingsChange updates next_at only when the operator changes
// enable/interval. Background reconfigure must not call this.
func applyScheduleSettingsChange(prev pluginConfig, cfg pluginConfig) {
	now := time.Now()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	loadScheduleStateLocked(cfg)

	if !cfg.ScheduleEnabled {
		scheduleState.nextAt = time.Time{}
		persistScheduleStateLocked(cfg)
		// No wake: timer is idle until next config/enable.
		return
	}

	mins := cfg.ScheduleIntervalMinutes
	if mins <= 0 {
		mins = defaultScheduleIntervalMinutes
	}
	enabledNow := !prev.ScheduleEnabled && cfg.ScheduleEnabled
	intervalChanged := prev.ScheduleEnabled && prev.ScheduleIntervalMinutes != cfg.ScheduleIntervalMinutes
	if enabledNow || intervalChanged || scheduleState.nextAt.IsZero() {
		// Always schedule into the future; ticker will fire without an immediate wake.
		scheduleState.nextAt = now.Add(time.Duration(mins) * time.Minute)
		persistScheduleStateLocked(cfg)
	}
}

func scheduleStatus() scheduleSnapshot {
	cfg := loadedConfig()
	now := time.Now().UTC()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	loadScheduleStateLocked(cfg)
	snap := scheduleSnapshot{
		Enabled:         cfg.ScheduleEnabled,
		IntervalMinutes: cfg.ScheduleIntervalMinutes,
		ServerNow:       now.Format(time.RFC3339),
		LastStatus:      scheduleState.lastStatus,
		LastError:       scheduleState.lastError,
		InProgress:      scheduleState.inProgress,
	}
	if snap.IntervalMinutes <= 0 {
		snap.IntervalMinutes = defaultScheduleIntervalMinutes
	}
	if !scheduleState.nextAt.IsZero() {
		snap.NextAt = scheduleState.nextAt.UTC().Format(time.RFC3339)
	}
	if !scheduleState.lastStarted.IsZero() {
		snap.LastStartedAt = scheduleState.lastStarted.UTC().Format(time.RFC3339)
	}
	if !scheduleState.lastFinished.IsZero() {
		snap.LastFinishedAt = scheduleState.lastFinished.UTC().Format(time.RFC3339)
	}
	return snap
}

func setScheduleOutcome(status, errMsg string) {
	cfg := loadedConfig()
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	scheduleState.lastStatus = status
	scheduleState.lastError = errMsg
	scheduleState.lastFinished = time.Now()
	persistScheduleStateLocked(cfg)
}

func maybeRunSchedule(now time.Time) {
	cfg := loadedConfig()
	if !cfg.ScheduleEnabled {
		return
	}

	scheduleState.mu.Lock()
	loadScheduleStateLocked(cfg)
	if scheduleState.inProgress {
		scheduleState.mu.Unlock()
		return
	}
	if scheduleState.nextAt.IsZero() {
		// Enabled but no next yet (should be rare after ensureScheduleRuntime).
		mins := cfg.ScheduleIntervalMinutes
		if mins <= 0 {
			mins = defaultScheduleIntervalMinutes
		}
		scheduleState.nextAt = now.Add(time.Duration(mins) * time.Minute)
		persistScheduleStateLocked(cfg)
		scheduleState.mu.Unlock()
		return
	}
	if now.Before(scheduleState.nextAt) {
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
	persistScheduleStateLocked(cfg)
	scheduleState.mu.Unlock()

	defer func() {
		scheduleState.mu.Lock()
		scheduleState.inProgress = false
		if scheduleState.lastFinished.IsZero() || scheduleState.lastFinished.Before(scheduleState.lastStarted) {
			scheduleState.lastFinished = time.Now()
		}
		persistScheduleStateLocked(loadedConfig())
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
	var he *httpStatusError
	if errors.As(err, &he) && he != nil && he.status == 409 {
		return true
	}
	return false
}
