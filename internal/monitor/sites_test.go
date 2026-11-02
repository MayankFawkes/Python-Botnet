package monitor

import (
	"net"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	private := []string{
		"10.10.10.25", "192.168.1.5", "172.16.4.9", "127.0.0.1",
		"100.64.5.5", "169.254.1.1", "0.0.0.0", "::1", "fc00::1", "fe80::1",
	}
	for _, addr := range private {
		ip := parseIPForTest(t, addr)
		if isPublicIP(ip) {
			t.Errorf("%s was treated as public", addr)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "203.0.113.7", "2606:4700:4700::1111"}
	for _, addr := range public {
		ip := parseIPForTest(t, addr)
		if !isPublicIP(ip) {
			t.Errorf("%s was treated as private", addr)
		}
	}
}

func TestSiteResolutionPicksMostSpecific(t *testing.T) {
	hub, _ := testHub(t)
	if _, err := hub.AddSite("Site", "10.0.0.0/16", 28.6, 77.2); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.AddSite("Rack", "10.10.10.0/24", 51.5, -0.12); err != nil {
		t.Fatal(err)
	}
	_, secret, err := hub.Enroll("site-node", "linux", "amd64", "2.0.0", "10.10.10.25")
	if err != nil {
		t.Fatal(err)
	}
	hub.Attach(secret, "10.10.10.25")
	if err := hub.Beat(secret, Heartbeat{CPU: 1, MemTotal: 1}); err != nil {
		t.Fatal(err)
	}
	snap := hub.Snapshot()
	if len(snap.Agents) != 1 {
		t.Fatalf("agents %+v", snap.Agents)
	}
	if snap.Agents[0].Site != "Rack" {
		t.Fatalf("expected the narrower network, got %q", snap.Agents[0].Site)
	}
	if len(snap.Points) != 1 || snap.Points[0].Total != 1 {
		t.Fatalf("points %+v", snap.Points)
	}
	if snap.Unplaced != 0 {
		t.Fatalf("placed agent counted as unplaced: %d", snap.Unplaced)
	}
}

func TestPrivateAgentIsNotSentForLookup(t *testing.T) {
	hub, _ := testHub(t)
	_, secret, err := hub.Enroll("vlan-node", "linux", "amd64", "2.0.0", "10.10.10.25")
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Beat(secret, Heartbeat{CPU: 1, MemTotal: 1}); err != nil {
		t.Fatal(err)
	}
	snap := hub.Snapshot()
	if len(snap.Points) != 0 {
		t.Fatalf("private address produced a point: %+v", snap.Points)
	}
	if snap.Unplaced != 0 {
		t.Fatalf("offline agent counted as unplaced online: %d", snap.Unplaced)
	}
	if snap.Agents[0].Lat != 0 || snap.Agents[0].Site != "" {
		t.Fatalf("private address was placed: %+v", snap.Agents[0])
	}
}

func TestAddSiteValidation(t *testing.T) {
	hub, _ := testHub(t)
	if _, err := hub.AddSite("", "10.0.0.0/8", 0, 0); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := hub.AddSite("Bad", "not-a-network", 0, 0); err == nil {
		t.Fatal("bad network accepted")
	}
	if _, err := hub.AddSite("Bad", "10.0.0.0/8", 91, 0); err == nil {
		t.Fatal("out-of-range latitude accepted")
	}
	if _, err := hub.AddSite("Bad", "10.0.0.0/8", 0, 181); err == nil {
		t.Fatal("out-of-range longitude accepted")
	}
	if _, err := hub.AddSite("Null Island", "10.1.0.0/16", 0, 0); err == nil {
		t.Fatal("empty coordinates accepted")
	}
	if _, err := hub.AddSite("Good", "192.168.0.0/16", 48.85, 2.35); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.AddSite("Dup", "192.168.0.0/16", 0, 0); err == nil {
		t.Fatal("duplicate network accepted")
	}
}

func TestSitesPersist(t *testing.T) {
	hub, _ := testHub(t)
	if _, err := hub.AddSite("Persist", "172.20.0.0/16", 35.68, 139.69); err != nil {
		t.Fatal(err)
	}
	cfg := hub.cfg
	hub.Close()
	restored := New(cfg)
	defer restored.Close()
	sites := restored.Sites()
	if len(sites) != 1 || sites[0].Name != "Persist" {
		t.Fatalf("sites %+v", sites)
	}
}

func parseIPForTest(t *testing.T, addr string) net.IP {
	t.Helper()
	return net.ParseIP(addr)
}
