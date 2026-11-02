package monitor

import (
	"fmt"
	"testing"
)

func TestReleaseNumberSurvivesRestartAndFailure(t *testing.T) {
	hub, _ := testHub(t)
	var releases []string
	build := func(root, dir string, servers []string, version string) error {
		releases = append(releases, version)
		return nil
	}
	if _, err := hub.buildClients("", "", nil, build); err != nil {
		t.Fatal(err)
	}
	if hub.Snapshot().ClientVersion != "v1" {
		t.Fatal("first release must be v1")
	}
	restored := New(hub.cfg)
	defer restored.Close()
	if _, err := restored.buildClients("", "", nil, build); err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().ClientVersion != "v2" {
		t.Fatal("second release must be v2")
	}
	failed := func(root, dir string, servers []string, version string) error { return fmt.Errorf("failed") }
	if _, err := restored.buildClients("", "", nil, failed); err == nil {
		t.Fatal("expected failure")
	}
	if restored.Snapshot().ClientVersion != "v2" {
		t.Fatal("failed build became latest release")
	}
	if _, err := restored.buildClients("", "", nil, build); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(releases) != "[v1 v2 v4]" {
		t.Fatalf("reused reserved number: %v", releases)
	}
}
