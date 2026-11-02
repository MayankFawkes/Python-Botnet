//go:build linux && amd64

package monitor

import (
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"atlas.local/simulator/internal/buildclient"
	"atlas.local/simulator/internal/clientupdate"
)

func TestLiveSignedSelfUpdate(t *testing.T) {
	if os.Getenv("BOTNET_TEST_SELFUPDATE") != "1" {
		t.Skip("set BOTNET_TEST_SELFUPDATE=1 for the live process update test")
	}
	root, err := buildclient.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	clients := filepath.Join(dir, "client")
	if err := os.Mkdir(clients, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := clientupdate.SigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	hub := New(Config{ListenAddr: addr, AdvertiseURL: addr, StatePath: filepath.Join(dir, "console.json"), ArtifactDir: dir})
	defer hub.Close()
	hub.clientBuild, hub.clientRelease = 2, 2
	go hub.ServeListener(listener)
	installed := filepath.Join(clients, "botnet-agent")
	newFile := filepath.Join(dir, "botnet-agent-v2-linux-amd64")
	for _, item := range []struct{ path, version string }{{installed, "v1"}, {newFile, "v2"}} {
		flags := "-X atlas.local/simulator/internal/agent.BakedServers=" + addr + " -X atlas.local/simulator/internal/version.Client=" + item.version + " -X atlas.local/simulator/internal/clientupdate.PublicKey=" + pub
		cmd := exec.Command("go", "build", "-ldflags", flags, "-o", item.path, "./cmd/agent")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	if err := clientupdate.Sign(newFile, "v2", "linux", "amd64", key); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(dir, "agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(installed)
	// Self-geolocation must not make external network calls in this integration test.
	cmd.Env = append(os.Environ(), "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Includes replacement/helper, only this test's process group. Wait for
		// Detach's final state write before TempDir removes the test directory.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			online := false
			for _, a := range hub.Snapshot().Agents {
				online = online || a.Status == "online"
			}
			if !online {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	go cmd.Wait()
	wait := func(check func(Snapshot) bool) Snapshot {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			snap := hub.Snapshot()
			if check(snap) {
				return snap
			}
			time.Sleep(50 * time.Millisecond)
		}
		out, _ := os.ReadFile(filepath.Join(dir, "agent.log"))
		t.Fatalf("timed out; agent log: %s; state: %+v", out, hub.Snapshot())
		return Snapshot{}
	}
	before := wait(func(s Snapshot) bool { return len(s.Agents) == 1 && s.Agents[0].UpdateCapable })
	credentialBefore, err := os.ReadFile(filepath.Join(clients, "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.UpdateClient(before.Agents[0].ID); err != nil {
		t.Fatal(err)
	}
	after := wait(func(s Snapshot) bool {
		return len(s.Agents) == 1 && s.Agents[0].Version == "v2" && s.Agents[0].UpdateStatus == "Updated" && s.Agents[0].Status == "online"
	})
	if before.Agents[0].ID != after.Agents[0].ID {
		t.Fatal("update changed client identity")
	}
	if !clientupdate.ValidRequestID(after.Agents[0].UpdateRequestID) {
		t.Fatal("update request ID missing")
	}
	journal, err := clientupdate.OpenJournal(filepath.Join(clients, "update-requests.json"), "v2")
	if err != nil || journal.CompletedID("v2") != after.Agents[0].UpdateRequestID {
		t.Fatalf("update receipt not preserved after restart: %v", err)
	}
	credentialAfter, err := os.ReadFile(filepath.Join(clients, "agent.json"))
	if err != nil || string(credentialBefore) != string(credentialAfter) {
		t.Fatal("update changed credentials")
	}
	out, err := exec.Command(installed, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "v2" {
		t.Fatalf("installed version: %s %v", out, err)
	}
}
