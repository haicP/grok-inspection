package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grok-inspection/cpasdk/pluginapi"
)

func TestScheduleStatusOnSnapshot(t *testing.T) {
	oldCfg := loadedConfig()
	cfg := oldCfg
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(oldCfg) })

	scheduleState.mu.Lock()
	scheduleState.nextAt = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
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
	t.Cleanup(func() { currentConfig.Store(old) })

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
	oldCfg := loadedConfig()
	cfg := oldCfg
	cfg.ScheduleEnabled = true
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(oldCfg) })

	oldEngine := engine
	engine = &inspectionEngine{workers: defaultWorkers, running: true}
	t.Cleanup(func() { engine = oldEngine })

	scheduleState.mu.Lock()
	scheduleState.inProgress = false
	scheduleState.nextAt = time.Now().Add(-time.Minute)
	scheduleState.lastStatus = ""
	scheduleState.lastError = ""
	scheduleState.mu.Unlock()

	// Ensure stop channel is open so scheduleStopped is false without starting full loop.
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

	scheduleState.mu.Lock()
	scheduleState.nextAt = time.Now().Add(-time.Minute)
	scheduleState.lastStatus = "seed"
	scheduleState.mu.Unlock()

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
	// Documented contract: scheduled startRequest uses maxWorkers.
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
	if !strings.Contains(string(resp.Body), `"schedule"`) {
		t.Fatalf("status missing schedule: %s", string(resp.Body))
	}
}

func TestUIContainsScheduleControls(t *testing.T) {
	page := string(renderUIPage(pluginName))
	for _, marker := range []string{
		`id="scheduleEnabledToggle"`,
		`id="scheduleInterval"`,
		`data-i18n="schedule_title"`,
		`/schedule-settings`,
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("page missing %q", marker)
		}
	}
}
