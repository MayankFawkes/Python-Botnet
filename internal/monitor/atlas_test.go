package monitor

import (
	"testing"
	"time"

	"atlas.local/simulator/internal/version"
)

func TestAtlasGroupsConnectedClientsByCoordinates(t *testing.T) {
	agents := []Agent{
		{Hostname: "a", Status: "online", Site: "City", Lat: 10, Lon: 20},
		{Hostname: "b", Status: "online", Site: "Other label", Lat: 10, Lon: 20},
		{Hostname: "c", Status: "online", Site: "City", Lat: 30, Lon: 40},
		{Hostname: "offline", Status: "offline", Site: "City", Lat: 10, Lon: 20},
		{Hostname: "private", Status: "online"},
	}
	points, unplaced := buildPoints(agents)
	if len(points) != 2 || points[0].Total != 2 || unplaced != 1 {
		t.Fatalf("points=%+v unplaced=%d", points, unplaced)
	}
}

func TestAttachResolvesWithoutSnapshotAndMovesLocation(t *testing.T) {
	hub, _ := testHub(t)
	hub.geo.cache["8.8.8.8"] = &Place{Name: "First", Lat: 10, Lon: 20}
	hub.geo.cache["1.1.1.1"] = &Place{Name: "Second", Lat: 30, Lon: 40}
	id, secret, err := hub.Enroll("moving", "linux", "amd64", version.Client, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"8.8.8.8", "1.1.1.1"} {
		generation := hub.Attach(secret, addr)
		deadline := time.Now().Add(time.Second)
		for {
			hub.mu.Lock()
			placed := hub.agents[id].Place != nil
			hub.mu.Unlock()
			if placed {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("Attach did not trigger location lookup")
			}
			time.Sleep(time.Millisecond)
		}
		snap := hub.Snapshot()
		if len(snap.Points) != 1 || snap.Points[0].Name != hub.geo.cache[addr].Name {
			t.Fatalf("wrong location: %+v", snap.Points)
		}
		if snap.ServerVersion != version.Server || snap.ClientVersion != version.Client {
			t.Fatal("version fields do not match their independent releases")
		}
		hub.Detach(secret, generation)
		if len(hub.Snapshot().Points) != 0 {
			t.Fatal("disconnected client remains on map")
		}
	}
}
