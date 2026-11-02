package monitor

import (
	"math"
	"testing"

	"atlas.local/simulator/internal/geolocation"
)

func TestClientReportedLocationPlacesPrivateAddressAndPersists(t *testing.T) {
	hub, _ := testHub(t)
	_, secret, err := hub.Enroll("nat-client", "linux", "amd64", "v1", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	hub.attach(secret, "10.0.0.5", true)
	place := &geolocation.Place{Name: "Client city", Lat: 51.5, Lon: -0.12}
	if err := hub.Beat(secret, Heartbeat{LocationCapable: true, Location: place}); err != nil {
		t.Fatal(err)
	}
	place.Name = "Mutated outside hub"
	snapshot := hub.Snapshot()
	if len(snapshot.Points) != 1 || snapshot.Points[0].Name != "Client city" || snapshot.Agents[0].GeoBy != "client" || snapshot.Agents[0].Remote != "10.0.0.5" {
		t.Fatalf("wrong client location: %+v", snapshot)
	}
	if err := hub.Beat(secret, Heartbeat{Location: &geolocation.Place{Name: "Invalid", Lat: math.NaN()}}); err == nil {
		t.Fatal("invalid location accepted")
	}
	hub.Close()
	restored := New(hub.cfg)
	defer restored.Close()
	restored.attach(secret, "10.0.0.5", true)
	if points := restored.Snapshot().Points; len(points) != 1 || points[0].Name != "Client city" {
		t.Fatalf("location was not persisted: %+v", points)
	}
}
