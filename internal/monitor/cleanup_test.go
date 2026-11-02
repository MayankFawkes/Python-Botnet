package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearOfflineClientsKeepsConnectedAndPersists(t *testing.T) {
	hub, _ := testHub(t)
	id, onlineSecret, err := hub.Enroll("connected", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	hub.Attach(onlineSecret, "127.0.0.1")
	_, offlineSecret, err := hub.Enroll("disconnected", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	hub.updateOffers[offlineSecret] = hub.now()
	result, err := hub.ClearOfflineClients()
	if err != nil || result.Removed != 1 || len(result.Snapshot.Agents) != 1 || result.Snapshot.Agents[0].ID != id {
		t.Fatalf("cleanup: %+v %v", result, err)
	}
	if err := hub.Beat(onlineSecret, Heartbeat{CPU: 1}); err != nil {
		t.Fatal("connected client lost authorization", err)
	}
	if err := hub.Beat(offlineSecret, Heartbeat{}); err == nil {
		t.Fatal("removed credentials still work")
	}
	if _, ok := hub.updateOffers[offlineSecret]; ok {
		t.Fatal("removed update reservation remains")
	}
	result, err = hub.ClearOfflineClients()
	if err != nil || result.Removed != 0 {
		t.Fatal("empty cleanup was not idempotent")
	}
	hub.Close()
	restored := New(hub.cfg)
	defer restored.Close()
	if agents := restored.Snapshot().Agents; len(agents) != 1 || agents[0].ID != id {
		t.Fatalf("cleanup was not saved: %+v", agents)
	}
}

func TestClearOfflineClientsRollsBackOnSaveFailure(t *testing.T) {
	hub, _ := testHub(t)
	_, secret, err := hub.Enroll("offline", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	previous := hub.cfg.StatePath
	hub.cfg.StatePath = filepath.Join(blocker, "console.json")
	hub.mu.Unlock()
	_, err = hub.ClearOfflineClients()
	hub.mu.Lock()
	hub.cfg.StatePath = previous
	hub.mu.Unlock()
	if err == nil {
		t.Fatal("expected save failure")
	}
	if len(hub.Snapshot().Agents) != 1 {
		t.Fatal("record lost on save failure")
	}
	if err := hub.Beat(secret, Heartbeat{}); err != nil {
		t.Fatal("credential lost on save failure", err)
	}
}
