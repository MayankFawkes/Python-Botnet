package monitor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"atlas.local/simulator/internal/clientupdate"
)

func TestBulkUpdateSkipsIneligibleClientsAndBoundsOffers(t *testing.T) {
	hub, _ := testHub(t)
	dir := t.TempDir()
	hub.cfg.ArtifactDir = dir
	hub.clientBuild = 2
	hub.clientRelease = 2
	key, err := clientupdate.SigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "botnet-agent-v2-linux-amd64")
	if err := os.WriteFile(path, []byte("signed binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := clientupdate.Sign(path, "v2", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	var secrets []string
	for i := 0; i < 13; i++ {
		version := "v1"
		if i == 10 {
			version = "v2"
		}
		_, secret, err := hub.Enroll(fmt.Sprintf("bulk-%d", i), "linux", "amd64", version, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if i != 11 {
			hub.Attach(secret, "127.0.0.1")
		}
		if err := hub.Beat(secret, Heartbeat{Version: version, UpdateCapable: i != 12}); err != nil {
			t.Fatal(err)
		}
		secrets = append(secrets, secret)
	}
	result, err := hub.UpdateAllClients()
	if err != nil || result.Queued != 10 || result.Skipped != 3 {
		t.Fatalf("queued=%d skipped=%d err=%v", result.Queued, result.Skipped, err)
	}
	ids := map[string]bool{}
	for _, agent := range result.Snapshot.Agents {
		if agent.UpdateStatus == "Queued" {
			if !clientupdate.ValidRequestID(agent.UpdateRequestID) || ids[agent.UpdateRequestID] {
				t.Fatal("bulk requests do not have distinct IDs")
			}
			ids[agent.UpdateRequestID] = true
		}
	}
	offers := 0
	for _, secret := range secrets {
		if hub.updateOffer(secret) != nil {
			offers++
		}
	}
	if offers != 8 {
		t.Fatalf("offered %d concurrent transfers, want 8", offers)
	}
	result, err = hub.UpdateAllClients()
	if err != nil || result.Queued != 0 || result.Skipped != 13 {
		t.Fatal("queued duplicate bulk updates")
	}
}

func TestSignedUpdateQueueAndTransfer(t *testing.T) {
	hub, _ := testHub(t)
	dir := t.TempDir()
	hub.cfg.ArtifactDir = dir
	hub.clientBuild, hub.clientRelease = 2, 2
	key, err := clientupdate.SigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "botnet-agent-v2-linux-amd64")
	if err := os.WriteFile(path, []byte("signed test binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := clientupdate.Sign(path, "v2", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	id, secret, err := hub.Enroll("update-node", "linux", "amd64", "v1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.UpdateClient(id); err == nil {
		t.Fatal("offline client update accepted")
	}
	hub.Attach(secret, "127.0.0.1")
	if _, err := hub.UpdateClient(id); err == nil {
		t.Fatal("legacy client update accepted")
	}
	if err := hub.Beat(secret, Heartbeat{Version: "v1", UpdateCapable: true, UpdateIDs: true}); err != nil {
		t.Fatal(err)
	}
	snap, err := hub.UpdateClient(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Agents[0].UpdateStatus != "Queued" || snap.Agents[0].Version != "v1" {
		t.Fatalf("wrong queued status: %+v", snap.Agents[0])
	}
	if _, err := hub.UpdateClient(id); err == nil {
		t.Fatal("duplicate update accepted")
	}
	if f, _, err := hub.updateFile("bad secret", "v2"); err == nil {
		f.Close()
		t.Fatal("unauthenticated download accepted")
	}
	requestID := snap.Agents[0].UpdateRequestID
	if !clientupdate.ValidRequestID(requestID) {
		t.Fatal("request ID missing")
	}
	if f, _, err := hub.updateFile(secret, "v2", "wrong-id"); err == nil {
		f.Close()
		t.Fatal("wrong request ID accepted")
	}
	f, m, err := hub.updateFile(secret, "v2", requestID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "signed test binary" || m.Version != "v2" {
		t.Fatal("incorrect update payload")
	}
	if f, _, err := hub.updateFile(secret, "v2", requestID); err == nil {
		f.Close()
		t.Fatal("same request started a second download")
	}
	if err := hub.Beat(secret, Heartbeat{Version: "v2", UpdateCapable: true}); err != nil {
		t.Fatal(err)
	}
	if hub.Snapshot().Agents[0].UpdateStatus != "Updated" {
		t.Fatal("update completion not reported")
	}
	if _, err := hub.UpdateClient(id); err == nil {
		t.Fatal("same-version update accepted")
	}
}
