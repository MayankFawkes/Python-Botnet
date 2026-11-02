package monitor

import "fmt"

type CleanupResult struct {
	Snapshot Snapshot `json:"snapshot"`
	Removed  int      `json:"removed"`
}

// ClearOfflineClients removes only records that are disconnected while the hub
// lock is held. A concurrent reconnect is either retained or must re-enroll.
func (h *Hub) ClearOfflineClients() (CleanupResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	removed := make(map[string]*record)
	for id, agent := range h.agents {
		if h.onlineLocked(agent) {
			continue
		}
		removed[id] = agent
		delete(h.agents, id)
		delete(h.bySecret, agent.Secret)
	}
	if len(removed) == 0 {
		return CleanupResult{Snapshot: h.snapshotLocked()}, nil
	}
	if err := h.saveLocked(); err != nil {
		for id, agent := range removed {
			h.agents[id] = agent
			h.bySecret[agent.Secret] = agent
		}
		return CleanupResult{}, err
	}
	for _, agent := range removed {
		delete(h.updateOffers, agent.Secret)
	}
	h.event("info", fmt.Sprintf("Cleared %d offline clients", len(removed)))
	h.markDirtyLocked()
	return CleanupResult{Snapshot: h.snapshotLocked(), Removed: len(removed)}, nil
}
