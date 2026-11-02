package clientupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedReleaseValidation(t *testing.T) {
	dir := t.TempDir()
	key, err := SigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("test binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Sign(path, "v2", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(path + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(pub, "linux", "amd64", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(path, m); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, goos, arch, current string }{
		{"", "linux", "amd64", "v1"}, {pub, "windows", "amd64", "v1"},
		{pub, "linux", "arm64", "v1"}, {pub, "linux", "amd64", "v2"}, {pub, "linux", "amd64", "v3"},
	} {
		if err := m.Verify(tc.key, tc.goos, tc.arch, tc.current); err == nil {
			t.Fatalf("accepted invalid update %+v", tc)
		}
	}
	tampered := m
	tampered.Version = "v9"
	if err := tampered.Verify(pub, "linux", "amd64", "v1"); err == nil {
		t.Fatal("tampered metadata accepted")
	}
	if err := os.WriteFile(path, []byte("tampered binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(path, m); err == nil {
		t.Fatal("tampered binary accepted")
	}
	loaded, err := SigningKey(dir)
	if err != nil || string(loaded) != string(key) {
		t.Fatal("signing key was not preserved")
	}
}

func TestCorruptKeyIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-signing.key")
	if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SigningKey(dir); err == nil {
		t.Fatal("corrupt key replaced")
	}
}
