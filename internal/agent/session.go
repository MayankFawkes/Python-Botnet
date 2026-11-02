package agent

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"sync"
	"time"
)

// serverMux demultiplexes console replies from asynchronous shell commands.
type serverMux struct {
	conn    net.Conn
	reader  *bufio.Reader
	readMu  sync.Mutex
	writeMu sync.Mutex
	replies chan wireMessage
	shell   *shellSession
}

func newServerMux(conn net.Conn, reader *bufio.Reader) *serverMux {
	m := &serverMux{
		conn:    conn,
		reader:  reader,
		replies: make(chan wireMessage, 1),
	}
	m.shell = newShellSession(m)
	go m.loop()
	return m
}

// muxReadTimeout must exceed the longest heartbeat interval (30 s) plus slack;
// the 10 s request timeout would tear down healthy idle connections.
const muxReadTimeout = 90 * time.Second

func (m *serverMux) loop() {
	for {
		m.readMu.Lock()
		msg, err := readReplyTimeout(m.conn, m.reader, muxReadTimeout)
		m.readMu.Unlock()
		if err != nil {
			m.shell.disconnect()
			close(m.replies)
			return
		}
		switch msg.Op {
		case "ok", "update", "update-busy", "err", "enrolled":
			select {
			case m.replies <- msg:
			default:
			}
		case "shell-open", "shell-in", "shell-resize", "shell-close":
			m.shell.handle(msg)
		}
	}
}

func (m *serverMux) waitReply() (wireMessage, error) {
	msg, ok := <-m.replies
	if !ok {
		return wireMessage{}, net.ErrClosed
	}
	return msg, nil
}

func (m *serverMux) write(msg wireMessage) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return writeMessage(m.conn, msg)
}

func (m *serverMux) readExclusive() (wireMessage, error) {
	m.readMu.Lock()
	msg, err := readReply(m.conn, m.reader)
	if err != nil {
		m.readMu.Unlock()
		return wireMessage{}, err
	}
	return msg, nil
}

func (m *serverMux) readBinaryTo(w io.Writer, n int64) (int64, error) {
	defer m.readMu.Unlock()
	return io.CopyN(w, m.reader, n)
}

func (m *serverMux) sendShellOut(data []byte) {
	if len(data) == 0 {
		return
	}
	// Keep one wire line well under the console's maxLine after base64 (+33%).
	const chunk = 2048
	for len(data) > 0 {
		partLen := chunk
		if len(data) < partLen {
			partLen = len(data)
		}
		part := data[:partLen]
		data = data[partLen:]
		_ = m.write(wireMessage{
			Op:        "shell-out",
			ShellData: base64.StdEncoding.EncodeToString(part),
		})
	}
}

func (m *serverMux) sendShellClosed(reason string) {
	_ = m.write(wireMessage{Op: "shell-closed", ShellErr: reason})
}
