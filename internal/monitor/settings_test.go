package monitor

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestSettingsPersistenceValidationAndRestart(t *testing.T) {
	hub, _ := testHub(t)
	s := hub.Settings()
	s.ListenAddr = "127.0.0.1:45678"
	s.AdvertiseURL = "127.0.0.1:45678"
	s.MaxClients = 12000
	s.ClientDefaults.RetrySeconds = 12
	if snap, err := hub.SaveSettings(s); err != nil || !snap.RestartRequired {
		t.Fatalf("settings: %+v %v", snap, err)
	}
	bad := s
	bad.ClientDefaults.HeartbeatSeconds = 31
	if _, err := hub.SaveSettings(bad); err == nil {
		t.Fatal("invalid client defaults accepted")
	}
	bad = s
	bad.AdvertiseURL = "localhost:fake"
	if _, err := hub.SaveSettings(bad); err == nil {
		t.Fatal("invalid server port accepted")
	}
	hub.Close()
	cfg := hub.cfg
	cfg.UseSavedSettings = true
	restored := New(cfg)
	defer restored.Close()
	if restored.Settings() != s || restored.Snapshot().RestartRequired {
		t.Fatalf("settings not restored: %+v", restored.Settings())
	}
}

func TestHeartbeatBurstsCoalesceAndCloseFlushesState(t *testing.T) {
	hub, _ := testHub(t)
	var notifications atomic.Int32
	hub.cfg.OnChange = func(Snapshot) { notifications.Add(1) }
	_, secret, err := hub.Enroll("batch-node", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := hub.Beat(secret, Heartbeat{CPU: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	if n := notifications.Load(); n > 2 {
		t.Fatalf("heartbeat burst produced %d snapshots", n)
	}
	hub.Close()
	data, err := os.ReadFile(hub.cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Agents) != 1 || state.Agents[0].CPU != 99 {
		t.Fatal("final metrics not flushed")
	}
}
