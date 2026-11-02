package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"atlas.local/simulator/internal/clientupdate"
	"atlas.local/simulator/internal/geolocation"
	"atlas.local/simulator/internal/loadgen"
)

// maxLine bounds one JSON line. Shell output is base64, so keep headroom
// above the agent's 2048-byte raw chunks (~2.8 KB on the wire).
const maxLine = 65536

type wireMessage struct {
	RequestID       string                 `json:"requestId,omitempty"`
	UpdateIDs       bool                   `json:"updateIds,omitempty"`
	UpdateAckID     string                 `json:"updateAckId,omitempty"`
	Location        *geolocation.Place     `json:"location,omitempty"`
	LocationCapable bool                   `json:"locationCapable,omitempty"`
	Update          *clientupdate.Manifest `json:"update,omitempty"`
	Load            *loadgen.Task          `json:"load,omitempty"`
	LoadBytes       uint64                 `json:"loadBytes,omitempty"`
	LoadRateBps     int64                  `json:"loadRateBps,omitempty"`
	LoadTaskID      string                 `json:"loadTaskId,omitempty"`
	BandwidthMbps   float64                `json:"bandwidthMbps,omitempty"`
	UpdateCapable   bool                   `json:"updateCapable,omitempty"`
	UpdateError     string                 `json:"updateError,omitempty"`
	ShellCols       int                    `json:"shellCols,omitempty"`
	ShellRows       int                    `json:"shellRows,omitempty"`
	ShellData       string                 `json:"shellData,omitempty"`
	ShellErr        string                 `json:"shellErr,omitempty"`
	Op              string                 `json:"op"`
	Token           string                 `json:"token,omitempty"`
	Secret          string                 `json:"secret,omitempty"`
	ID              string                 `json:"id,omitempty"`
	Hostname        string                 `json:"hostname,omitempty"`
	OS              string                 `json:"os,omitempty"`
	Arch            string                 `json:"arch,omitempty"`
	Version         string                 `json:"version,omitempty"`
	CPUPercent      float64                `json:"cpuPercent,omitempty"`
	MemUsed         uint64                 `json:"memUsed,omitempty"`
	MemTotal        uint64                 `json:"memTotal,omitempty"`
	UptimeSec       int                    `json:"uptimeSec,omitempty"`
	Error           string                 `json:"error,omitempty"`
}

// Serve accepts long-lived agent TCP connections. Each session is one JSON
// object per line. The console does not speak HTTP on this port.
func (h *Hub) Serve(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		h.SetListener("", err)
		return
	}
	h.serve(listener)
}

// ServeListener accepts agent connections on an existing socket.
func (h *Hub) ServeListener(listener net.Listener) { h.serve(listener) }

func (h *Hub) serve(listener net.Listener) {
	h.mu.Lock()
	h.ln = listener
	h.mu.Unlock()
	h.SetListener(listener.Addr().String(), nil)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

func (h *Hub) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, maxLine)
	remote := remoteHost(conn.RemoteAddr().String())
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	msg, err := readMessage(reader)
	if err != nil {
		return
	}
	var secret string
	var agentID string
	switch msg.Op {
	case "enroll":
		id, enrolledSecret, enrollErr := h.Enroll(msg.Hostname, msg.OS, msg.Arch, msg.Version, remote)
		if enrollErr != nil {
			_ = writeMessage(conn, wireMessage{Op: "err", Error: enrollErr.Error()})
			return
		}
		secret = enrolledSecret
		agentID = id
		if err := writeMessage(conn, wireMessage{Op: "enrolled", ID: id, Secret: secret}); err != nil {
			return
		}
	case "auth":
		rec := h.agentBySecretLocked(msg.Secret)
		if msg.Secret == "" || rec == nil {
			_ = writeMessage(conn, wireMessage{Op: "err", Error: "unauthorized"})
			return
		}
		secret = msg.Secret
		agentID = rec.ID
		if err := writeMessage(conn, wireMessage{Op: "ok"}); err != nil {
			return
		}
	default:
		_ = writeMessage(conn, wireMessage{Op: "err", Error: "expected enroll or auth"})
		return
	}
	generation := h.attach(secret, remote, msg.LocationCapable)
	lc := h.registerLiveConn(agentID, secret, conn)
	defer func() {
		h.unregisterLiveConn(agentID, lc)
		h.Detach(secret, generation)
	}()

	incoming := make(chan wireMessage, 8)
	go func() {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			next, readErr := readMessage(reader)
			if readErr != nil {
				close(incoming)
				return
			}
			incoming <- next
		}
	}()

	for msg = range incoming {
		switch msg.Op {
		case "get-update":
			select {
			case h.updateSlots <- struct{}{}:
			default:
				_ = lc.write(wireMessage{Op: "update-busy"})
				continue
			}
			f, manifest, err := h.updateFile(secret, msg.Version, msg.RequestID)
			if err != nil {
				<-h.updateSlots
				_ = lc.write(wireMessage{Op: "err", Error: err.Error()})
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Minute))
			if err := lc.write(wireMessage{Op: "update", Update: &manifest, RequestID: msg.RequestID}); err != nil {
				f.Close()
				<-h.updateSlots
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Minute))
			_, err = io.CopyN(conn, f, manifest.Size)
			f.Close()
			<-h.updateSlots
			if err != nil {
				return
			}
		case "shell-out":
			h.deliverShellOut(agentID, msg.ShellData)
		case "shell-closed":
			h.shellSessionEnded(agentID, msg.ShellErr)
		case "beat":
			err = h.Beat(secret, Heartbeat{
				UpdateIDs: msg.UpdateIDs, UpdateAckID: msg.UpdateAckID,
				Location: msg.Location, LocationCapable: msg.LocationCapable,
				Hostname: msg.Hostname, Version: msg.Version, CPU: msg.CPUPercent,
				MemUsed: msg.MemUsed, MemTotal: msg.MemTotal, Uptime: msg.UptimeSec,
				LoadBytes: msg.LoadBytes, LoadTaskID: msg.LoadTaskID, LoadRateBps: msg.LoadRateBps,
				BandwidthMbps: msg.BandwidthMbps,
				RemoteAddr:    remote,
				UpdateCapable: msg.UpdateCapable, UpdateError: msg.UpdateError,
			})
			if err != nil {
				_ = lc.write(wireMessage{Op: "err", Error: err.Error()})
				return
			}
			offer, id := h.updateEnvelope(secret)
			if err := lc.write(wireMessage{Op: "ok", Update: offer, RequestID: id, Load: h.loadOffer()}); err != nil {
				return
			}
		default:
			_ = lc.write(wireMessage{Op: "err", Error: "unknown operation"})
			return
		}
	}
}

// loadOffer returns the active load-test task for a beat reply.
func (h *Hub) loadOffer() *loadgen.Task {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loadOfferLocked()
}

func (h *Hub) agentBySecretLocked(secret string) *record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agentBySecret(secret)
}

func readMessage(reader *bufio.Reader) (wireMessage, error) {
	line, err := reader.ReadSlice('\n')
	if len(line) > maxLine {
		return wireMessage{}, errors.New("message too long")
	}
	if err != nil {
		return wireMessage{}, err
	}
	var msg wireMessage
	if jsonErr := json.Unmarshal(line, &msg); jsonErr != nil {
		return wireMessage{}, jsonErr
	}
	return msg, nil
}

func writeMessage(conn net.Conn, msg wireMessage) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
