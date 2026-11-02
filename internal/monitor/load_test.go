package monitor

import (
	"testing"
	"time"
)

func TestStartLoad(t *testing.T) {
	hub, _ := testHub(t)
	snap, err := hub.StartLoad("10.100.0.10", 80, "tcp", 20, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Load.Active || snap.Load.Task == nil || snap.Load.Task.Target != "10.100.0.10" {
		t.Fatalf("snapshot %+v", snap.Load)
	}
	if snap.Load.Task.DeadlineUnix == 0 || snap.Load.Task.ID == "" {
		t.Fatal("task is missing its deadline or id")
	}
}

func TestStartLoadValidatesShape(t *testing.T) {
	hub, _ := testHub(t)
	if _, err := hub.StartLoad("https://example.com/x", 80, "tcp", 20, 30); err == nil {
		t.Fatal("accepted a URL as target")
	}
	for _, args := range []struct {
		port, percent, duration int
		method                  string
	}{
		{0, 20, 30, "tcp"},       // bad port
		{80, 0, 30, "tcp"},       // bad percent
		{80, 101, 30, "tcp"},     // percent above ceiling
		{80, 20, 0, "tcp"},       // bad duration
		{80, 20, 301, "tcp"},     // duration above cap
		{80, 20, 30, "synflood"}, // unknown method
	} {
		if _, err := hub.StartLoad("10.100.0.10", args.port, args.method, args.percent, args.duration); err == nil {
			t.Fatalf("accepted %+v", args)
		}
	}
}

func TestLoadByteAndRateAggregation(t *testing.T) {
	hub, now := testHub(t)
	_, secretA, err := hub.Enroll("node-a", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, secretB, err := hub.Enroll("node-b", "linux", "amd64", "v1", "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	hub.Attach(secretA, "127.0.0.1")
	snap, err := hub.StartLoad("10.100.0.10", 443, "udp", 50, 60)
	if err != nil {
		t.Fatal(err)
	}
	taskID := snap.Load.Task.ID
	if err := hub.Beat(secretA, Heartbeat{LoadTaskID: taskID, LoadBytes: 1000, LoadRateBps: 500}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Beat(secretB, Heartbeat{LoadTaskID: taskID, LoadBytes: 2500, LoadRateBps: 700}); err != nil {
		t.Fatal(err)
	}
	snap = hub.Snapshot()
	if snap.Load.TotalBytes != 3500 || snap.Load.Agents != 2 {
		t.Fatalf("totals %+v", snap.Load)
	}
	// Only the connected client's live rate counts.
	if snap.Load.TotalRateBps != 500 {
		t.Fatalf("rate %d", snap.Load.TotalRateBps)
	}
	// After the deadline the run is no longer offered and shows inactive.
	*now = now.Add(61 * time.Second)
	if offer := hub.loadOffer(); offer != nil {
		t.Fatal("expired task is still offered")
	}
	if hub.Snapshot().Load.Active {
		t.Fatal("expired run is still active")
	}
	if _, err := hub.StopLoad(); err == nil {
		t.Fatal("stopping an expired run should fail")
	}
}

func TestStopLoad(t *testing.T) {
	hub, _ := testHub(t)
	if _, err := hub.StartLoad("10.100.0.10", 80, "http", 10, 30); err != nil {
		t.Fatal(err)
	}
	snap, err := hub.StopLoad()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Load.Active {
		t.Fatal("run still active after stop")
	}
	if hub.loadOffer() != nil {
		t.Fatal("stopped task is still offered")
	}
}

func TestBandwidthReport(t *testing.T) {
	hub, _ := testHub(t)
	_, secret, err := hub.Enroll("node-a", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Beat(secret, Heartbeat{BandwidthMbps: 93.5}); err != nil {
		t.Fatal(err)
	}
	if got := hub.Snapshot().Agents[0].BandwidthMbps; got != 93.5 {
		t.Fatalf("bandwidth %v", got)
	}
	if err := hub.Beat(secret, Heartbeat{BandwidthMbps: -1}); err == nil {
		t.Fatal("accepted negative bandwidth")
	}
}
