package monitor

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"atlas.local/simulator/internal/clientupdate"
)

// UpdateClient queues a signed release for one online, update-capable client.
func (h *Hub) UpdateClient(id string) (Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agents[id]
	if a == nil {
		return Snapshot{}, errors.New("unknown client")
	}
	if !a.Connected {
		return Snapshot{}, errors.New("client must be online")
	}
	if !a.UpdateCapable {
		return Snapshot{}, errors.New("this client needs one manual replacement with an updater-capable build")
	}
	if a.PendingUpdate != nil || a.UpdateStatus == "Installing" {
		return Snapshot{}, errors.New("an update is already in progress")
	}
	m, err := h.releaseLocked(a)
	if err != nil {
		return Snapshot{}, err
	}
	requestID, err := randomHex(16)
	if err != nil {
		return Snapshot{}, err
	}
	previous := *a
	a.UpdateRequestID = requestID
	a.PendingUpdate = &m
	a.UpdateTarget = m.Version
	a.UpdateStatus = "Queued"
	if err := h.saveLocked(); err != nil {
		*a = previous
		return Snapshot{}, err
	}
	h.event("info", "Update to "+m.Version+" requested for "+a.Hostname)
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}

type BulkUpdateResult struct {
	Snapshot Snapshot `json:"snapshot"`
	Queued   int      `json:"queued"`
	Skipped  int      `json:"skipped"`
}

// UpdateAllClients queues eligible online clients. Each platform's signed
// artifact is checked once, not once per machine; transfers are bounded below.
func (h *Hub) UpdateAllClients() (BulkUpdateResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := BulkUpdateResult{}
	releases := map[string]clientupdate.Manifest{}
	previous := map[*record]record{}
	invalid := map[string]bool{}
	for _, a := range h.agents {
		current, err := clientupdate.Number(a.Version)
		if !a.Connected || !a.UpdateCapable || err != nil || current >= h.clientRelease || a.PendingUpdate != nil || a.UpdateStatus == "Installing" {
			result.Skipped++
			continue
		}
		platform := a.OS + "/" + a.Arch
		m, ok := releases[platform]
		if !ok && !invalid[platform] {
			m, err = h.releaseLocked(a)
			if err != nil {
				invalid[platform] = true
			} else {
				releases[platform] = m
			}
		}
		if invalid[platform] {
			result.Skipped++
			continue
		}
		copy := m
		id, err := randomHex(16)
		if err != nil {
			result.Skipped++
			continue
		}
		previous[a] = *a
		a.UpdateRequestID = id
		a.PendingUpdate = &copy
		a.UpdateTarget = m.Version
		a.UpdateStatus = "Queued"
		result.Queued++
	}
	if result.Queued > 0 {
		if err := h.saveLocked(); err != nil {
			for a, old := range previous {
				*a = old
			}
			return BulkUpdateResult{}, err
		}
		h.event("info", fmt.Sprintf("Bulk update: %d clients queued, %d skipped", result.Queued, result.Skipped))
		h.markDirtyLocked()
	}
	result.Snapshot = h.snapshotLocked()
	return result, nil
}

func (h *Hub) releaseLocked(a *record) (clientupdate.Manifest, error) {
	if h.cfg.ArtifactDir == "" || h.clientRelease == 0 {
		return clientupdate.Manifest{}, errors.New("build a new client release first")
	}
	m := clientupdate.Manifest{Version: h.clientVersionLocked(), OS: a.OS, Arch: a.Arch}
	name, err := m.Name()
	if err != nil {
		return m, err
	}
	m, err = clientupdate.ReadManifest(filepath.Join(h.cfg.ArtifactDir, name+".json"))
	if err != nil {
		return m, fmt.Errorf("signed release unavailable: %w", err)
	}
	keyData, err := os.ReadFile(filepath.Join(h.cfg.ArtifactDir, "update-signing.key"))
	if err != nil || len(keyData) != ed25519.PrivateKeySize {
		return m, errors.New("release signing key unavailable")
	}
	key := ed25519.PrivateKey(keyData)
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if err := m.Verify(pub, a.OS, a.Arch, a.Version); err != nil {
		return m, err
	}
	if m.Version != h.clientVersionLocked() {
		return m, errors.New("release manifest version mismatch")
	}
	if err := clientupdate.CheckFile(filepath.Join(h.cfg.ArtifactDir, name), m); err != nil {
		return m, err
	}
	return m, nil
}

func (h *Hub) updateOffer(secret string) *clientupdate.Manifest {
	m, _ := h.updateEnvelope(secret)
	return m
}

func (h *Hub) updateEnvelope(secret string) (*clientupdate.Manifest, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.agentBySecret(secret); a != nil && a.PendingUpdate != nil {
		now := time.Now()
		for token, expiry := range h.updateOffers {
			if !now.Before(expiry) {
				delete(h.updateOffers, token)
			}
		}
		if _, reserved := h.updateOffers[secret]; !reserved {
			if len(h.updateOffers)+len(h.updateSlots) >= cap(h.updateSlots) {
				return nil, ""
			}
			h.updateOffers[secret] = now.Add(30 * time.Second)
		}
		copy := *a.PendingUpdate
		return &copy, a.UpdateRequestID
	}
	return nil, ""
}

func (h *Hub) updateFile(secret, release string, ids ...string) (*os.File, clientupdate.Manifest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agentBySecret(secret)
	delete(h.updateOffers, secret)
	if a == nil || a.PendingUpdate == nil || a.PendingUpdate.Version != release {
		return nil, clientupdate.Manifest{}, errors.New("no matching update request")
	}
	if a.UpdateIDs && (len(ids) != 1 || ids[0] != a.UpdateRequestID || !clientupdate.ValidRequestID(ids[0])) {
		return nil, clientupdate.Manifest{}, errors.New("update request ID does not match")
	}
	m := *a.PendingUpdate
	name, err := m.Name()
	if err != nil {
		return nil, m, err
	}
	f, err := os.Open(filepath.Join(h.cfg.ArtifactDir, name))
	if err != nil {
		a.UpdateStatus = "Failed: release file unavailable"
		a.PendingUpdate = nil
		h.markDirtyLocked()
		return nil, m, err
	}
	a.PendingUpdate = nil
	a.UpdateStatus = "Installing"
	h.markDirtyLocked()
	return f, m, nil
}
