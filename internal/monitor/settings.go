package monitor

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"atlas.local/simulator/internal/clientconfig"
)

type Settings struct {
	ListenAddr     string              `json:"listenAddr"`
	AdvertiseURL   string              `json:"advertiseURL"`
	MaxClients     int                 `json:"maxClients"`
	ClientDefaults clientconfig.Config `json:"clientDefaults"`
}

func (s Settings) Validate() error {
	_, port, err := net.SplitHostPort(s.ListenAddr)
	n, conv := strconv.Atoi(port)
	if err != nil || conv != nil || n < 1 || n > 65535 {
		return fmt.Errorf("listen address must be host:port with port 1–65535")
	}
	if _, err := ServerAddresses(s.AdvertiseURL, ""); err != nil {
		return err
	}
	_, advertisedPort, err := net.SplitHostPort(s.AdvertiseURL)
	n, conv = strconv.Atoi(advertisedPort)
	if err != nil || conv != nil || n < 1 || n > 65535 {
		return fmt.Errorf("client-facing address must be host:port with port 1–65535")
	}
	if s.MaxClients < 1 || s.MaxClients > 100000 {
		return fmt.Errorf("client limit must be 1–100000")
	}
	return s.ClientDefaults.Validate()
}

func (h *Hub) Settings() Settings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

func (h *Hub) SaveSettings(settings Settings) (Snapshot, error) {
	settings.ListenAddr = strings.TrimSpace(settings.ListenAddr)
	settings.AdvertiseURL = strings.TrimSpace(settings.AdvertiseURL)
	if err := settings.Validate(); err != nil {
		return Snapshot{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.settings
	h.settings = settings
	if err := h.saveLocked(); err != nil {
		h.settings = old
		return Snapshot{}, err
	}
	h.cfg.AdvertiseURL = settings.AdvertiseURL
	h.event("success", "Server settings saved")
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}
