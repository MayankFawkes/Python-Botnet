package buildclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargets(t *testing.T) {
	targets, err := Targets()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		seen[target] = true
		if !strings.HasPrefix(target, "linux/") && target != "windows/386" && target != "windows/amd64" {
			t.Fatalf("unexpected target %s", target)
		}
	}
	for _, target := range []string{"linux/386", "linux/amd64", "linux/arm", "linux/arm64", "windows/386", "windows/amd64"} {
		if !seen[target] {
			t.Errorf("missing %s", target)
		}
	}
}

func TestPruneOnlyOlderGeneratedArtifacts(t *testing.T) {
	dir := t.TempDir()
	old := []string{"botnet-agent-v1-linux-amd64", "botnet-agent-v1-linux-amd64.json", "botnet-agent-v1-windows-386.exe"}
	keep := []string{"botnet-agent-v2-linux-amd64", "botnet-agent-v3-linux-amd64", "botnet", "botnet-agent", "console.json", "update-signing.key", "notes-v1.txt"}
	for _, name := range append(append([]string{}, old...), keep...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(dir, "v2"); err != nil {
		t.Fatal(err)
	}
	for _, name := range old {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("old artifact remains: %s", name)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("protected file removed: %s", name)
		}
	}
}

func TestBundleStagesVersionedTargetsAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	targets := []string{"linux/arm64", "windows/386", "windows/amd64"}
	build := func(root, output string, servers []string, goos, goarch, version string) error {
		if version != "v1" {
			t.Fatalf("version %s", version)
		}
		return os.WriteFile(output, []byte(goos+"/"+goarch+":"+version), 0o700)
	}
	if err := bundle("", dir, []string{"localhost:34116"}, "v1", targets, build); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"botnet-agent-v1-linux-arm64", "botnet-agent-v1-windows-386.exe", "botnet-agent-v1-windows-amd64.exe"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bundle("", dir, []string{"localhost:34116"}, "v1", targets, build); err == nil {
		t.Fatal("overwrote release")
	}
}

func TestFailedBundleDoesNotPublishPartialRelease(t *testing.T) {
	dir := t.TempDir()
	build := func(root, output string, servers []string, goos, goarch, version string) error {
		if goos == "windows" {
			return fmt.Errorf("build failed")
		}
		return os.WriteFile(output, []byte("binary"), 0o700)
	}
	if err := bundle("", dir, []string{"localhost:34116"}, "v1", []string{"linux/amd64", "windows/386"}, build); err == nil {
		t.Fatal("expected failure")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial release left behind: %v %v", entries, err)
	}
}

// Opt-in integration check: compiles the real agent for every requested target.
func TestBundleCrossCompile(t *testing.T) {
	if os.Getenv("BOTNET_TEST_CROSSBUILD") != "1" {
		t.Skip("set BOTNET_TEST_CROSSBUILD=1 to build every platform")
	}
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Bundle(root, dir, []string{"127.0.0.1:34116"}, "v7"); err != nil {
		t.Fatal(err)
	}
	targets, err := Targets()
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2*len(targets)+1 {
		t.Fatalf("files=%d targets=%d err=%v", len(files), len(targets), err)
	}
}
