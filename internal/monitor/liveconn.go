package monitor

import (
	"errors"
	"net"
	"sync"
)

var errAgentOffline = errors.New("client is offline")

// liveConn is one enrolled agent TCP session. Writes are serialized so heartbeats
// and shell traffic can share the connection safely.
type liveConn struct {
	id     string
	secret string
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *liveConn) write(msg wireMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errAgentOffline
	}
	return writeMessage(c.conn, msg)
}

func (c *liveConn) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (h *Hub) registerLiveConn(id, secret string, conn net.Conn) *liveConn {
	lc := &liveConn{id: id, secret: secret, conn: conn}
	h.mu.Lock()
	if h.liveConns == nil {
		h.liveConns = map[string]*liveConn{}
	}
	h.liveConns[id] = lc
	h.mu.Unlock()
	return lc
}

func (h *Hub) unregisterLiveConn(id string, lc *liveConn) {
	lc.close()
	h.mu.Lock()
	if cur := h.liveConns[id]; cur == lc {
		delete(h.liveConns, id)
	}
	h.mu.Unlock()
	h.shellConnEnded(id)
}

func (h *Hub) liveConnByID(id string) (*liveConn, *record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.agents[id]
	if agent == nil {
		return nil, nil, errors.New("unknown client")
	}
	if !agent.Connected {
		return nil, agent, errAgentOffline
	}
	lc := h.liveConns[id]
	if lc == nil {
		return nil, agent, errAgentOffline
	}
	return lc, agent, nil
}
