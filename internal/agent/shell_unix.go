//go:build unix

package agent

import (
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/creack/pty"
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
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = os.Environ()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		s.mu.Unlock()
		s.mux.sendShellClosed(err.Error())
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = ptmx.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			close(done)
		})
	}
	s.stdin = ptmx
	s.stop = stop
	s.active = true
	s.mu.Unlock()

	qr := &termQueryResponder{until: time.Now().Add(termQueryWindow)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if resp := qr.feed(buf[:n]); len(resp) > 0 {
					_, _ = ptmx.Write(resp)
				}
				s.mux.sendShellOut(buf[:n])
			}
			if readErr != nil {
				if readErr != io.EOF {
					s.closeIfGen(gen, readErr.Error())
				} else {
					s.closeIfGen(gen, "")
				}
				return
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		s.closeIfGen(gen, "")
	}()
}

// termQueries are terminal capability queries a shell may emit at startup,
// paired with the replies a real terminal would send. The console UI renders
// plain text and cannot respond, but fish 4+ blocks its first prompt until
// every startup query is answered, so without these replies the remote
// shell appears dead.
var termQueries = []struct {
	re   *regexp.Regexp
	resp string
}{
	{regexp.MustCompile(`\x1b\[\?[0-9;]*u`), "\x1b[?0u"},
	{regexp.MustCompile(`\x1b\[>[0-9;]*q`), "\x1bP>|BOTNET\x1b\\"},
	{regexp.MustCompile(`\x1b\]11;\?(?:\x1b\\|\x07)`), "\x1b]11;rgb:0000/0000/0000\x1b\\"},
	{regexp.MustCompile(`\x1bP\+q[0-9a-fA-F]*\x1b\\`), "\x1bP0+r\x1b\\"},
	{regexp.MustCompile(`\x1b\[>[0-9;]*c`), "\x1b[>1;10;0c"},
	{regexp.MustCompile(`\x1b\[6n`), "\x1b[1;1R"},
	{regexp.MustCompile(`\x1b\[5n`), "\x1b[0n"},
	{regexp.MustCompile(`\x1b\[[0-9;]*c`), "\x1b[?1;2c"},
}

// termQueryWindow limits auto-answering to session startup so later output
// that happens to contain these byte sequences is left alone.
const termQueryWindow = 15 * time.Second

type queryHit struct {
	lo, hi int
	resp   string
}

type termQueryResponder struct {
	until time.Time
	buf   []byte
}

// feed scans shell output for capability queries and returns the replies to
// inject into the shell's stdin. The output itself is forwarded unmodified.
func (q *termQueryResponder) feed(d []byte) []byte {
	if q == nil || time.Now().After(q.until) {
		return nil
	}
	q.buf = append(q.buf, d...)
	var hits []queryHit
	for _, tq := range termQueries {
		for _, m := range tq.re.FindAllIndex(q.buf, -1) {
			hits = append(hits, queryHit{m[0], m[1], tq.resp})
		}
	}
	if len(hits) == 0 {
		if len(q.buf) > 64 {
			q.buf = q.buf[len(q.buf)-64:]
		}
		return nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].lo < hits[j].lo })
	var resp []byte
	last := 0
	for _, h := range hits {
		if h.lo < last {
			continue
		}
		resp = append(resp, h.resp...)
		last = h.hi
	}
	q.buf = q.buf[last:]
	if len(q.buf) > 64 {
		q.buf = q.buf[len(q.buf)-64:]
	}
	return resp
}

func (s *shellSession) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	f, ok := s.stdin.(*os.File)
	s.mu.Unlock()
	if !ok || f == nil {
		return
	}
	_ = pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (s *shellSession) writeInputLocked(data []byte) {
	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()
	if stdin == nil {
		return
	}
	_, _ = stdin.Write(data)
}
