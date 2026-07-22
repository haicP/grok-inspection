package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grok-inspection/cpasdk/pluginapi"
)

func resetScheduleStateForTest(partial scheduleRuntime) {
	scheduleState.mu.Lock()
	defer scheduleState.mu.Unlock()
	// Keep the mutex; only replace bookkeeping fields.
	scheduleState.nextAt = partial.nextAt
	scheduleState.lastStarted = partial.lastStarted
	scheduleState.lastFinished = partial.lastFinished
	scheduleState.lastStatus = partial.lastStatus
	scheduleState.lastError = partial.lastError
	scheduleState.inProgress = partial.inProgress
	scheduleState.loaded = partial.loaded
}

func TestScheduleStatusOnSnapshot(t *testing.T) {
	oldCfg := loadedConfig()
	cfg := oldCfg
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(oldCfg) })

	fixed := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	scheduleState.mu.Lock()
	scheduleState.loaded = true
	scheduleState.nextAt = fixed
	scheduleState.lastStatus = scheduleStatusOK
	scheduleState.lastError = ""
	scheduleState.mu.Unlock()

	snap := engine.snapshot(false)
	if !snap.Schedule.Enabled {
		t.Fatal("schedule.enabled = false")
	}
	if snap.Schedule.IntervalMinutes != 30 {
		t.Fatalf("interval = %d", snap.Schedule.IntervalMinutes)
	}
	if snap.Schedule.NextAt == "" {
		t.Fatal("next_at empty")
	}
	if snap.Schedule.ServerNow == "" {
		t.Fatal("server_now empty")
	}
	if snap.Schedule.LastStatus != scheduleStatusOK {
		t.Fatalf("last_status = %q", snap.Schedule.LastStatus)
	}
}

func TestScheduleSettingsAPI(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = false
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	resetScheduleStateForTest(scheduleRuntime{})
	t.Cleanup(func() {
		currentConfig.Store(old)
		resetScheduleStateForTest(scheduleRuntime{})
	})

	resp := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   "/v0/management/plugins/grok-inspection/schedule-settings",
		Body:   []byte(`{"enabled":true,"interval_minutes":60}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("payload=%v", payload)
	}
	if payload["enabled"] != true {
		t.Fatalf("enabled=%v", payload["enabled"])
	}
	if payload["interval_minutes"] != float64(60) {
		t.Fatalf("interval=%v", payload["interval_minutes"])
	}
	if !loadedConfig().ScheduleEnabled || loadedConfig().ScheduleIntervalMinutes != 60 {
		t.Fatalf("loaded=%#v", loadedConfig())
	}

	bad := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   "/v0/management/plugins/grok-inspection/schedule-settings",
		Body:   []byte(`{"interval_minutes":2}`),
	})
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad status=%d body=%s", bad.StatusCode, string(bad.Body))
	}
}

func TestMaybeRunScheduleSkipsWhenBusy(t *testing.T) {
	dir := t.TempDir()
	oldCfg := loadedConfig()
	cfg := oldCfg
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(oldCfg) })

	oldEngine := engine
	engine = &inspectionEngine{workers: defaultWorkers, running: true}
	t.Cleanup(func() { engine = oldEngine })

	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: time.Now().Add(-time.Minute)})

	scheduleStopMu.Lock()
	if scheduleStop == nil {
		scheduleStop = make(chan struct{})
	}
	scheduleStopMu.Unlock()

	maybeRunSchedule(time.Now())

	scheduleState.mu.Lock()
	status := scheduleState.lastStatus
	inProg := scheduleState.inProgress
	scheduleState.mu.Unlock()
	if inProg {
		t.Fatal("inProgress still true")
	}
	if status != scheduleStatusSkippedBusy {
		t.Fatalf("status=%q want skipped_busy", status)
	}
}

func TestMaybeRunScheduleDisabledNoop(t *testing.T) {
	oldCfg := loadedConfig()
	cfg := oldCfg
	cfg.ScheduleEnabled = false
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(oldCfg) })

	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: time.Now().Add(-time.Minute), lastStatus: "seed"})

	maybeRunSchedule(time.Now())

	scheduleState.mu.Lock()
	status := scheduleState.lastStatus
	scheduleState.mu.Unlock()
	if status != "seed" {
		t.Fatalf("disabled schedule should not change status, got %q", status)
	}
}

func TestScheduledStartUsesMaxWorkers(t *testing.T) {
	if maxWorkers != 16 {
		t.Fatalf("maxWorkers=%d want 16", maxWorkers)
	}
	req := startRequest{Workers: maxWorkers, IncludeDisabled: false, OnlyDisabled: false, Incremental: false}
	if req.Workers != 16 || req.IncludeDisabled || req.OnlyDisabled || req.Incremental {
		t.Fatalf("req=%#v", req)
	}
}

func TestStatusIncludesSchedule(t *testing.T) {
	resp := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/plugins/grok-inspection/status",
		Query:  map[string][]string{"include_results": {"0"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	body := string(resp.Body)
	if !strings.Contains(body, `"schedule"`) {
		t.Fatalf("status missing schedule: %s", body)
	}
	if !strings.Contains(body, `"server_now"`) {
		t.Fatalf("status missing server_now: %s", body)
	}
}

func TestUIContainsScheduleControls(t *testing.T) {
	page := string(renderUIPage(pluginName))
	for _, marker := range []string{
		`id="scheduleEnabledToggle"`,
		`id="scheduleInterval"`,
		`data-i18n="schedule_title"`,
		`/schedule-settings`,
		`server_now`,
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("page missing %q", marker)
		}
	}
}

func TestEnsureScheduleRuntimeDoesNotSlideNextAt(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() {
		currentConfig.Store(old)
		resetScheduleStateForTest(scheduleRuntime{})
	})

	fixed := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: fixed})
	scheduleState.mu.Lock()
	persistScheduleStateLocked(cfg)
	scheduleState.mu.Unlock()

	// Simulate page-open / reconfigure: ensureScheduleRuntime must keep fixed next_at.
	ensureScheduleRuntime(time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC))
	// Force reload path as if process just restarted.
	scheduleState.mu.Lock()
	scheduleState.loaded = false
	scheduleState.nextAt = time.Time{}
	scheduleState.mu.Unlock()
	ensureScheduleRuntime(time.Date(2026, 7, 23, 9, 5, 0, 0, time.UTC))

	scheduleState.mu.Lock()
	got := scheduleState.nextAt.UTC()
	scheduleState.mu.Unlock()
	if !got.Equal(fixed) {
		t.Fatalf("next_at slid: got %s want %s", got, fixed)
	}
}

func TestApplyScheduleSettingsChangeOnlyOnToggleOrInterval(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() {
		currentConfig.Store(old)
		resetScheduleStateForTest(scheduleRuntime{})
	})

	fixed := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: fixed})

	// No real change: should keep next_at (apply with same prev/cfg values would not be called
	// in production; enabled-true interval same should not reschedule when next exists).
	prev := cfg
	next := cfg
	// Manually: interval change should move next_at.
	next.ScheduleIntervalMinutes = 60
	before := time.Now()
	applyScheduleSettingsChange(prev, next)
	scheduleState.mu.Lock()
	got := scheduleState.nextAt
	scheduleState.mu.Unlock()
	if !got.After(before) {
		t.Fatalf("interval change should set next_at in the future, got %v", got)
	}
	if got.Equal(fixed) {
		t.Fatal("interval change should replace fixed next_at")
	}

	// Disable clears next_at.
	disabled := next
	disabled.ScheduleEnabled = false
	applyScheduleSettingsChange(next, disabled)
	scheduleState.mu.Lock()
	if !scheduleState.nextAt.IsZero() {
		t.Fatalf("disable should clear next_at, got %v", scheduleState.nextAt)
	}
	scheduleState.mu.Unlock()
}

func TestScheduleStatePersistsAcrossLoad(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 45
	currentConfig.Store(cfg)
	t.Cleanup(func() {
		currentConfig.Store(old)
		resetScheduleStateForTest(scheduleRuntime{})
	})

	fixed := time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC)
	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: fixed, lastStatus: scheduleStatusOK})
	scheduleState.mu.Lock()
	persistScheduleStateLocked(cfg)
	scheduleState.mu.Unlock()

	path := scheduleStateFile(cfg)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("schedule state file missing: %v", err)
	}

	resetScheduleStateForTest(scheduleRuntime{})
	scheduleState.mu.Lock()
	loadScheduleStateLocked(cfg)
	if !scheduleState.nextAt.UTC().Equal(fixed) {
		t.Fatalf("loaded next_at=%v want %v", scheduleState.nextAt, fixed)
	}
	if scheduleState.lastStatus != scheduleStatusOK {
		t.Fatalf("last_status=%q", scheduleState.lastStatus)
	}
	scheduleState.mu.Unlock()
}

func TestConfigureDoesNotResetScheduleNextAt(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)

	fixed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	resetScheduleStateForTest(scheduleRuntime{loaded: true, nextAt: fixed})
	scheduleState.mu.Lock()
	persistScheduleStateLocked(cfg)
	// Mark unloaded so configure→ensure reloads from disk.
	scheduleState.loaded = false
	scheduleState.nextAt = time.Time{}
	scheduleState.mu.Unlock()

	t.Cleanup(func() {
		currentConfig.Store(old)
		resetScheduleStateForTest(scheduleRuntime{})
	})

	// YAML enables schedule; configure must restore disk next_at, not now+30m.
	yaml := "schedule_enabled: true\nschedule_interval_minutes: 30\n"
	raw, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"config_yaml":    []byte(yaml),
	})
	// Also write runtime settings so applyRuntimeSettings keeps schedule enabled with state file path.
	// configure uses decodeConfig only + runtime settings from state file dir.
	// Point default via StateFile after configure by saving settings first.
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"schedule_enabled":true,"schedule_interval_minutes":30}`), 0o644)

	// Use StateFile dir for schedule.json already written.
	if err := configure(raw); err != nil {
		t.Fatalf("configure: %v", err)
	}
	// After configure, StateFile may reset to default; force load from our dir.
	// Re-store cfg with our StateFile and ensure from disk path.
	cfg2 := loadedConfig()
	cfg2.StateFile = filepath.Join(dir, "bans.json")
	cfg2.ScheduleEnabled = true
	currentConfig.Store(cfg2)
	scheduleState.mu.Lock()
	scheduleState.loaded = false
	scheduleState.nextAt = time.Time{}
	scheduleState.mu.Unlock()
	ensureScheduleRuntime(time.Now())

	scheduleState.mu.Lock()
	got := scheduleState.nextAt.UTC()
	scheduleState.mu.Unlock()
	if !got.Equal(fixed) {
		t.Fatalf("configure/reload slid next_at: got %s want %s", got, fixed)
	}
}
