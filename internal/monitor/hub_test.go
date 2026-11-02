package monitor

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func testHub(t *testing.T) (*Hub, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	hub := New(Config{
		ListenAddr:   "127.0.0.1:34116",
		AdvertiseURL: "127.0.0.1:34116",
		StatePath:    filepath.Join(t.TempDir(), "console.json"),
		Now:          func() time.Time { return now },
	})
	t.Cleanup(hub.Close)
	return hub, &now
}

func TestEnrollHeartbeatRevokeAndExpiry(t *testing.T) {
	hub, _ := testHub(t)
	id, secret, err := hub.Enroll("lab-node", "linux", "amd64", "2.0.0", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Beat("wrong", Heartbeat{CPU: 1}); err == nil {
		t.Fatal("bad secret was accepted")
	}
	if err := hub.Beat(secret, Heartbeat{CPU: 150}); err == nil {
		t.Fatal("invalid cpu accepted")
	}
	if err := hub.Beat(secret, Heartbeat{CPU: 12.5, MemUsed: 100, MemTotal: 1000, Uptime: 4, Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	snap := hub.Snapshot()
	if len(snap.Agents) != 1 || snap.Agents[0].Status != "offline" || snap.Agents[0].CPU != 12.5 {
		t.Fatalf("snapshot %+v", snap.Agents)
	}
	generation := hub.Attach(secret, "127.0.0.1")
	if hub.Snapshot().Agents[0].Status != "online" {
		t.Fatal("attached agent is offline")
	}
	hub.Detach(secret, generation)
	if hub.Snapshot().Agents[0].Status != "offline" {
		t.Fatal("detached agent is still online")
	}
	snap, err = hub.Revoke(id)
	if err != nil || len(snap.Agents) != 0 {
		t.Fatal(err, len(snap.Agents))
	}
	if err := hub.Beat(secret, Heartbeat{CPU: 1, MemTotal: 1}); err == nil {
		t.Fatal("revoked agent still authorized")
	}
}

func TestStateRestoresEnrollment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.json")
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cfg := Config{AdvertiseURL: "127.0.0.1:9", StatePath: path, Now: func() time.Time { return now }}
	hub := New(cfg)
	_, secret, err := hub.Enroll("persist-host", "linux", "amd64", "1.0.0", "10.0.0.8")
	if err != nil {
		t.Fatal(err)
	}
	hub.Close()

	restored := New(cfg)
	defer restored.Close()
	if err := restored.Beat(secret, Heartbeat{CPU: 1, MemUsed: 1, MemTotal: 2, Uptime: 9, Version: LatestVersion}); err != nil {
		t.Fatal(err)
	}
	snap := restored.Snapshot()
	if len(snap.Agents) != 1 || snap.Agents[0].Version != LatestVersion || snap.Agents[0].Hostname != "persist-host" {
		t.Fatalf("%+v", snap.Agents)
	}
}

func TestTCPSession(t *testing.T) {
	hub, _ := testHub(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go hub.serve(listener)
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(map[string]string{
		"op": "enroll", "hostname": "tcp-node",
		"os": "linux", "arch": "amd64", "version": "2.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := decoder.Decode(&reply); err != nil || reply["op"] != "enrolled" {
		t.Fatalf("enroll reply %#v %v", reply, err)
	}
	if err := encoder.Encode(map[string]any{
		"op": "beat", "cpuPercent": 4.5, "memUsed": 10, "memTotal": 50, "uptimeSec": 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&reply); err != nil || reply["op"] != "ok" {
		t.Fatalf("beat reply %#v %v", reply, err)
	}
	snap := hub.Snapshot()
	if len(snap.Agents) != 1 || snap.Agents[0].Status != "online" || snap.Agents[0].CPU != 4.5 || snap.Agents[0].Remote == "" {
		t.Fatalf("%+v", snap.Agents)
	}
	if err := encoder.Encode(map[string]string{"op": "bogus"}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&reply); err != nil || reply["op"] != "err" {
		t.Fatalf("bogus reply %#v %v", reply, err)
	}
	_ = conn.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.Snapshot().Agents[0].Status == "offline" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("closed TCP session still online")
}

func TestReEnrollReusesRecord(t *testing.T) {
	hub, _ := testHub(t)
	id, first, err := hub.Enroll("dup-host", "linux", "amd64", "2.0.0", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	secondID, second, err := hub.Enroll("dup-host", "linux", "amd64", "2.0.1", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if secondID != id {
		t.Fatalf("re-enroll created a new agent: %s vs %s", secondID, id)
	}
	if second == first {
		t.Fatal("re-enroll reused the old secret")
	}
	snap := hub.Snapshot()
	if len(snap.Agents) != 1 {
		t.Fatalf("expected one agent, got %d", len(snap.Agents))
	}
	if snap.Agents[0].Version != "2.0.1" {
		t.Fatalf("version not updated: %+v", snap.Agents[0])
	}
	if err := hub.Beat(first, Heartbeat{CPU: 1, MemTotal: 1}); err == nil {
		t.Fatal("old secret still authorized")
	}
	if err := hub.Beat(second, Heartbeat{CPU: 1, MemTotal: 1}); err != nil {
		t.Fatalf("new secret rejected: %v", err)
	}
}

func TestDistinctHostsSameNameAreKept(t *testing.T) {
	hub, _ := testHub(t)
	first, _, err := hub.Enroll("shared-name", "linux", "amd64", "2.0.0", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := hub.Enroll("shared-name", "linux", "amd64", "2.0.0", "10.0.0.6")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two machines with the same hostname were merged")
	}
	if len(hub.Snapshot().Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(hub.Snapshot().Agents))
	}
}

func TestServerAddresses(t *testing.T) {
	list, err := ServerAddresses("10.0.0.1:34116", "10.0.0.2:34116, 10.0.0.1:34116")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if _, err := ServerAddresses("", ""); err == nil {
		t.Fatal("expected error")
	}
}
