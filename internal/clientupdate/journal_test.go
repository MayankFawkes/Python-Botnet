package clientupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestJournalPersistsAndDoesNotReapplyCompletedID(t *testing.T) {
	dir := t.TempDir()
	key, err := SigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	t.Cleanup(func() { PublicKey = old })
	file := filepath.Join(dir, "agent")
	if err := os.WriteFile(file, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Sign(file, "v2", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(file + ".json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "update-requests.json")
	j, err := OpenJournal(path, "v1")
	if err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	if duplicate, err := j.Prepare(id, m, "v1"); err != nil || duplicate {
		t.Fatalf("prepare duplicate=%v err=%v", duplicate, err)
	}
	// A failed download can retry without creating another logical request.
	if duplicate, err := j.Prepare(id, m, "v1"); err != nil || duplicate || len(j.Requests) != 1 {
		t.Fatal("pending retry created duplicate request")
	}
	j, err = OpenJournal(path, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if j.CompletedID("v2") != id {
		t.Fatal("restart did not acknowledge applied version")
	}
	if duplicate, err := j.Prepare(id, m, "v2"); err != nil || !duplicate {
		t.Fatalf("completed request re-executed: %v %v", duplicate, err)
	}
	if err := Sign(file, "v3", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	changed, err := ReadManifest(file + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Prepare(id, changed, "v2"); err == nil {
		t.Fatal("request ID rebound to another release")
	}
}
