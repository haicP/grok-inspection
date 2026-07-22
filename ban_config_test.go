package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	got := defaultPluginConfig()
	if got.FallbackHours != 24 {
		t.Fatalf("fallback hours = %d, want 24", got.FallbackHours)
	}
	if !got.PersistState {
		t.Fatal("persist state = false, want true")
	}
	if got.StateFile == "" {
		t.Fatal("state file is empty, want default data/grok-inspection/bans.json")
	}
	if !got.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if !got.LogMatches {
		t.Fatal("log matches = false, want true")
	}
	if got.ScheduleEnabled {
		t.Fatal("schedule enabled = true, want false")
	}
	if got.ScheduleIntervalMinutes != 30 {
		t.Fatalf("schedule interval = %d, want 30", got.ScheduleIntervalMinutes)
	}
}

func TestDecodeConfig(t *testing.T) {
	got, err := decodeConfig([]byte("fallback_hours: 48\npersist_state: false\nstate_file: data/bans.json\nlog_matches: false\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if got.FallbackHours != 48 || got.PersistState || got.StateFile != "data/bans.json" || got.LogMatches {
		t.Fatalf("config = %#v", got)
	}
}

func TestDecodeConfigInvalidFallbackUsesDefault(t *testing.T) {
	for _, raw := range []string{"fallback_hours: 0\n", "fallback_hours: 169\n"} {
		got, err := decodeConfig([]byte(raw))
		if err != nil {
			t.Fatalf("decodeConfig(%q) error = %v", raw, err)
		}
		if got.FallbackHours != 24 {
			t.Fatalf("decodeConfig(%q) fallback = %d, want 24", raw, got.FallbackHours)
		}
	}
}

// CPA appends a top-level enabled:true for the plugin lifecycle. That must not
// override an explicit autoban_enabled:false written earlier in the same YAML.
func TestDecodeConfigIgnoresCPATopLevelEnabled(t *testing.T) {
	raw := []byte("autoban_enabled: false\nfallback_hours: 24\nenabled: true\n")
	got, err := decodeConfig(raw)
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if got.Enabled {
		t.Fatalf("Enabled=true; CPA top-level enabled must not override autoban_enabled:false")
	}

	// Reverse order: trailing enabled:true still ignored after autoban_enabled:false.
	raw2 := []byte("enabled: true\nautoban_enabled: false\n")
	got2, err := decodeConfig(raw2)
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if got2.Enabled {
		t.Fatalf("Enabled=true after enabled:true then autoban_enabled:false")
	}

	// Bare enabled without autoban_enabled keeps default (true).
	got3, err := decodeConfig([]byte("enabled: false\nfallback_hours: 24\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if !got3.Enabled {
		t.Fatalf("bare enabled:false must not disable autoban; got Enabled=false")
	}

	// Explicit true still works.
	got4, err := decodeConfig([]byte("autoban_enabled: true\nenabled: false\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if !got4.Enabled {
		t.Fatalf("autoban_enabled:true should remain true")
	}
}

func TestConfigureLoadsLifecycleYAML(t *testing.T) {
	err := configure([]byte(`{"schema_version":1,"config_yaml":"ZmFsbGJhY2tfaG91cnM6IDcyCnBlcnNpc3Rfc3RhdGU6IGZhbHNlCg=="}`))
	if err != nil {
		t.Fatalf("configure() error = %v", err)
	}
	got := loadedConfig()
	if got.FallbackHours != 72 || got.PersistState {
		t.Fatalf("loaded config = %#v", got)
	}
}

func TestDecodeConfigSchedule(t *testing.T) {
	got, err := decodeConfig([]byte("schedule_enabled: true\nschedule_interval_minutes: 60\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if !got.ScheduleEnabled || got.ScheduleIntervalMinutes != 60 {
		t.Fatalf("config = %#v", got)
	}

	// Invalid interval keeps default 30.
	got2, err := decodeConfig([]byte("schedule_interval_minutes: 2\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if got2.ScheduleIntervalMinutes != 30 {
		t.Fatalf("invalid interval should keep default 30, got %d", got2.ScheduleIntervalMinutes)
	}

	// CPA top-level enabled must not enable schedule.
	got3, err := decodeConfig([]byte("enabled: true\n"))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if got3.ScheduleEnabled {
		t.Fatal("CPA top-level enabled must not enable schedule")
	}
}

func TestUpdateScheduleSettingsPersists(t *testing.T) {
	dir := t.TempDir()
	old := loadedConfig()
	cfg := old
	cfg.StateFile = filepath.Join(dir, "bans.json")
	cfg.ScheduleEnabled = false
	cfg.ScheduleIntervalMinutes = 30
	currentConfig.Store(cfg)
	t.Cleanup(func() { currentConfig.Store(old) })

	on := true
	interval := 45
	got, err := updateScheduleSettings(&on, &interval)
	if err != nil {
		t.Fatalf("updateScheduleSettings() error = %v", err)
	}
	if !got.ScheduleEnabled || got.ScheduleIntervalMinutes != 45 {
		t.Fatalf("got = %#v", got)
	}
	// Autoban fields preserved.
	if got.Enabled != old.Enabled {
		t.Fatalf("autoban enabled changed: %#v vs %#v", got.Enabled, old.Enabled)
	}
	rs := loadRuntimeSettings(runtimeSettingsFile(got))
	if rs.ScheduleEnabled == nil || !*rs.ScheduleEnabled || rs.ScheduleIntervalMinutes == nil || *rs.ScheduleIntervalMinutes != 45 {
		t.Fatalf("runtime settings = %#v", rs)
	}

	bad := 3
	if _, err := updateScheduleSettings(nil, &bad); err == nil {
		t.Fatal("expected interval validation error")
	}
}
