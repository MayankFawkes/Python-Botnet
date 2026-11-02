package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"atlas.local/simulator/internal/geolocation"
	"atlas.local/simulator/internal/monitor"
)

func TestClientLocationDoesNotBlockConnectionAndIsSentImmediately(t *testing.T) {
	hub := monitor.New(monitor.Config{})
	address := startHub(t, hub)
	previous := BakedServers
	BakedServers = address
	t.Cleanup(func() { BakedServers = previous; hub.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	allowLookup := make(chan struct{})
	lookup := func(ctx context.Context) (*geolocation.Place, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-allowLookup:
		}
		return &geolocation.Place{Name: "Client city", Lat: 28.6, Lon: 77.2}, nil
	}
	done := make(chan error, 1)
	path := filepath.Join(t.TempDir(), "agent.json")
	go func() {
		done <- Run(ctx, Options{StatePath: path, Hostname: "private-node", Interval: time.Hour, GeoLookup: lookup})
	}()
	wait := func(check func(monitor.Snapshot) bool) monitor.Snapshot {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			snapshot := hub.Snapshot()
			if check(snapshot) {
				return snapshot
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("timed out waiting for client state")
		return monitor.Snapshot{}
	}
	wait(func(s monitor.Snapshot) bool { return len(s.Agents) == 1 && s.Agents[0].MemTotal > 0 })
	if len(hub.Snapshot().Points) != 0 {
		t.Fatal("location invented before lookup completed")
	}
	close(allowLookup)
	snapshot := wait(func(s monitor.Snapshot) bool { return len(s.Points) == 1 })
	if snapshot.Agents[0].GeoBy != "client" || snapshot.Points[0].Name != "Client city" || snapshot.Unplaced != 0 {
		t.Fatalf("location was not reported: %+v", snapshot)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not stop")
	}
}

func TestLocationLookupFailureCanRetry(t *testing.T) {
	reporter := newLocationReporter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 2)
	attempt := 0
	lookup := func(context.Context) (*geolocation.Place, error) {
		attempt++
		calls <- struct{}{}
		if attempt == 1 {
			return nil, errors.New("temporary failure")
		}
		return &geolocation.Place{Name: "Recovered city", Lat: 1, Lon: 2}, nil
	}
	done := make(chan struct{})
	go func() { defer close(done); reporter.run(ctx, lookup) }()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("lookup not started")
	}
	reporter.mu.Lock()
	reporter.lastAttempt = time.Now().Add(-time.Minute)
	reporter.mu.Unlock()
	reporter.refresh <- struct{}{}
	select {
	case <-reporter.changed:
	case <-time.After(time.Second):
		t.Fatal("failed lookup was not retried")
	}
	if reporter.current().Name != "Recovered city" {
		t.Fatal("retry location missing")
	}
	cancel()
	<-done
}
