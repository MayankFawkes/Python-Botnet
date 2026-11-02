package monitor

import (
	"encoding/base64"
	"errors"
)

// ShellEvent is one chunk of remote shell output or a session end notice.
type ShellEvent struct {
	AgentID string `json:"agentId"`
	Data    string `json:"data,omitempty"`
	Closed  bool   `json:"closed,omitempty"`
	Error   string `json:"error,omitempty"`
}

const shellDefaultCols = 120
const shellDefaultRows = 32

func (h *Hub) pushShellEvent(ev ShellEvent) {
	if h.cfg.OnShell != nil {
		h.cfg.OnShell(ev)
	}
	h.shellMu.Lock()
	defer h.shellMu.Unlock()
	if h.shellOut == nil {
		h.shellOut = map[string][]ShellEvent{}
	}
	// Keep the closed marker in the queue. Dropping the buffer here erased
	// output the UI had not drained yet, so the shell tab opened blank.
	if ev.Closed {
		h.shellOut[ev.AgentID] = append(h.shellOut[ev.AgentID], ev)
		return
	}
	h.shellOut[ev.AgentID] = append(h.shellOut[ev.AgentID], ev)
	const maxQueued = 64
	if len(h.shellOut[ev.AgentID]) > maxQueued {
		h.shellOut[ev.AgentID] = h.shellOut[ev.AgentID][len(h.shellOut[ev.AgentID])-maxQueued:]
	}
}

// ShellDrain returns queued shell output for browser preview polling.
func (h *Hub) ShellDrain(id string) []ShellEvent {
	h.shellMu.Lock()
	defer h.shellMu.Unlock()
	if h.shellOut == nil {
		return nil
	}
	out := append([]ShellEvent{}, h.shellOut[id]...)
	delete(h.shellOut, id)
	if out == nil {
		return []ShellEvent{}
	}
	return out
}

func (h *Hub) deliverShellOut(id, dataB64 string) {
	if dataB64 == "" {
		return
	}
	h.pushShellEvent(ShellEvent{AgentID: id, Data: dataB64})
}

func (h *Hub) shellConnEnded(id string) {
	h.pushShellEvent(ShellEvent{AgentID: id, Closed: true})
}

func (h *Hub) shellSessionEnded(id, reason string) {
	ev := ShellEvent{AgentID: id, Closed: true}
	if reason != "" {
		ev.Error = reason
	}
	h.pushShellEvent(ev)
}

// ShellOpen starts an interactive shell on an online client.
// cols and rows are the visible terminal size; zero uses the defaults.
func (h *Hub) ShellOpen(id string, cols, rows int) error {
	if cols <= 0 {
		cols = shellDefaultCols
	}
	if rows <= 0 {
		rows = shellDefaultRows
	}
	lc, _, err := h.liveConnByID(id)
	if err != nil {
		return err
	}
	return lc.write(wireMessage{Op: "shell-open", ShellCols: cols, ShellRows: rows})
}

// ShellWrite sends keystrokes or input to the remote shell session.
func (h *Hub) ShellWrite(id, data string) error {
	if data == "" {
		return nil
	}
	lc, _, err := h.liveConnByID(id)
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(data))
	return lc.write(wireMessage{Op: "shell-in", ShellData: encoded})
}

// ShellResize updates the remote PTY size when supported.
func (h *Hub) ShellResize(id string, cols, rows int) error {
	if cols <= 0 {
		cols = shellDefaultCols
	}
	if rows <= 0 {
		rows = shellDefaultRows
	}
	lc, _, err := h.liveConnByID(id)
	if err != nil {
		return err
	}
	return lc.write(wireMessage{Op: "shell-resize", ShellCols: cols, ShellRows: rows})
}

// ShellClose ends the remote shell session.
func (h *Hub) ShellClose(id string) error {
	lc, _, err := h.liveConnByID(id)
	if errors.Is(err, errAgentOffline) {
		return nil
	}
	if err != nil {
		return err
	}
	return lc.write(wireMessage{Op: "shell-close"})
}
