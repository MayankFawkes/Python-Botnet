package monitor

import (
	"fmt"

	"atlas.local/simulator/internal/buildclient"
	"atlas.local/simulator/internal/clientconfig"
)

func (h *Hub) clientVersionLocked() string {
	if h.clientRelease == 0 {
		return "v1"
	}
	return fmt.Sprintf("v%d", h.clientRelease)
}

// BuildClients serializes release creation. The counter is saved before building
// so a failed/interrupted build never causes an existing release to be replaced.
func (h *Hub) BuildClients(root, dir string, servers []string) (string, error) {
	return h.BuildClientsConfigured(root, dir, servers, h.Settings().ClientDefaults)
}

func (h *Hub) BuildClientsConfigured(root, dir string, servers []string, config clientconfig.Config) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	build := func(root, dir string, servers []string, version string) error {
		return buildclient.BundleConfigured(root, dir, servers, version, config)
	}
	path, err := h.buildClients(root, dir, servers, build)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	release := h.clientVersionLocked()
	h.mu.Unlock()
	if err := buildclient.Prune(dir, release); err != nil {
		return path, fmt.Errorf("release built, but older release cleanup failed: %w", err)
	}
	return path, nil
}

func (h *Hub) buildClients(root, dir string, servers []string, build func(string, string, []string, string) error) (string, error) {
	h.buildMu.Lock()
	defer h.buildMu.Unlock()
	h.mu.Lock()
	h.clientBuild++
	release := fmt.Sprintf("v%d", h.clientBuild)
	if err := h.saveLocked(); err != nil {
		h.clientBuild--
		h.mu.Unlock()
		return "", err
	}
	h.mu.Unlock()
	if err := build(root, dir, servers, release); err != nil {
		return "", fmt.Errorf("release %s failed (number reserved): %w", release, err)
	}
	h.mu.Lock()
	h.clientRelease = h.clientBuild
	h.event("success", "Client release "+release+" built for Linux and Windows")
	err := h.saveLocked()
	h.markDirtyLocked()
	h.mu.Unlock()
	return dir, err
}
