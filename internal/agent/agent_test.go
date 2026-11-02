package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"atlas.local/simulator/internal/geolocation"
	"atlas.local/simulator/internal/monitor"
)

func TestRunEnrollsAndReports(t *testing.T) {
	now := time.Now()
	hub := monitor.New(monitor.Config{
		AdvertiseURL: "127.0.0.1:1",
		Now:          func() time.Time { return now },
	})
	address := startHub(t, hub)
	prev := BakedServers
	BakedServers = address
	t.Cleanup(func() {
		BakedServers = prev
		hub.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			StatePath: filepath.Join(t.TempDir(), "agent.json"),
			Interval:  20 * time.Millisecond, Hostname: "agent-test", Version: "2.0.0",
			GeoLookup: func(context.Context) (*geolocation.Place, error) {
				return &geolocation.Place{Name: "Test city", Lat: 51.5, Lon: -0.12}, nil
			},
		})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := hub.Snapshot()
		if len(snap.Agents) == 1 && snap.Agents[0].Status == "online" && snap.Agents[0].MemTotal > 0 {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent did not report: %+v", hub.Snapshot().Agents)
}
