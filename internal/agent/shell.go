package agent

import (
	"encoding/base64"
	"io"
	"sync"
)

type shellSession struct {
	mux    *serverMux
	mu     sync.Mutex
	active bool
	gen    uint64
	stop   func()
	stdin  io.WriteCloser
}

func newShellSession(m *serverMux) *shellSession {
	return &shellSession{mux: m}
}

func (s *shellSession) handle(msg wireMessage) {
	switch msg.Op {
	case "shell-open":
		s.open(msg.ShellCols, msg.ShellRows)
	case "shell-in":
		data, err := base64.StdEncoding.DecodeString(msg.ShellData)
		if err != nil {
			return
		}
		s.writeInput(data)
	case "shell-resize":
		s.resize(msg.ShellCols, msg.ShellRows)
	case "shell-close":
		s.close("")
	}
}

func (s *shellSession) disconnect() {
	s.close("")
}

func (s *shellSession) close(reason string) {
	s.mu.Lock()
	s.gen++
	stop := s.stop
	s.stop = nil
	s.active = false
	s.stdin = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	s.mux.sendShellClosed(reason)
}

// closeIfGen ends the session only if it is still the current generation.
// Goroutines from a session replaced by a newer open must not clobber or
// kill the new session's state.
func (s *shellSession) closeIfGen(gen uint64, reason string) {
	s.mu.Lock()
	if s.gen != gen || !s.active {
		s.mu.Unlock()
		return
	}
	stop := s.stop
	s.stop = nil
	s.active = false
	s.stdin = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	s.mux.sendShellClosed(reason)
}

func (s *shellSession) writeInput(data []byte) {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if !active {
		return
	}
	s.writeInputLocked(data)
}
