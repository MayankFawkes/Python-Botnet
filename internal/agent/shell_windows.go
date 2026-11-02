//go:build windows

package agent

import (
	"io"
	"os/exec"
	"sync"
)

func (s *shellSession) open(cols, rows int) {
	s.mu.Lock()
	s.gen++
	gen := s.gen
	if s.active {
		stop := s.stop
		s.stop = nil
		s.active = false
		s.stdin = nil
		s.mu.Unlock()
		if stop != nil {
			stop()
		}
		s.mu.Lock()
	}
	cmd := exec.Command("cmd.exe")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.mu.Unlock()
		s.mux.sendShellClosed(err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.mu.Unlock()
		s.mux.sendShellClosed(err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.mu.Unlock()
		s.mux.sendShellClosed(err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		s.mux.sendShellClosed(err.Error())
		return
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}
	s.stdin = stdin
	s.stop = stop
	s.active = true
	s.mu.Unlock()

	pump := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				s.mux.sendShellOut(buf[:n])
			}
			if readErr != nil {
				s.closeIfGen(gen, "")
				return
			}
		}
	}
	go pump(stdout)
	go pump(stderr)
	go func() {
		_ = cmd.Wait()
		s.closeIfGen(gen, "")
	}()
}

func (s *shellSession) resize(cols, rows int) {}

func (s *shellSession) writeInputLocked(data []byte) {
	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()
	if stdin == nil {
		return
	}
	_, _ = stdin.Write(data)
}
