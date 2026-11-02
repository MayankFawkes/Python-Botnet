// Package agent is the monitoring client installed by an operator.
// It keeps one TCP connection open and reports hostname, OS, CPU, and memory.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"atlas.local/simulator/internal/clientconfig"
	"atlas.local/simulator/internal/clientupdate"
	"atlas.local/simulator/internal/geolocation"
	"atlas.local/simulator/internal/hoststat"
	"atlas.local/simulator/internal/loadgen"
	"atlas.local/simulator/internal/version"
)

// Options configures one agent process. Console addresses come from the linked
// binary (BakedServers); operators do not pass them on the command line.
type Options struct {
	journal   *clientupdate.Journal
	loads     *loadRunner
	bandwidth *bandwidthProbe
	// GeoLookup may be supplied by an embedded client or tests; binaries use ipwho.is.
	GeoLookup      func(context.Context) (*geolocation.Place, error)
	locations      *locationReporter
	UpdateError    string
	updateError    *string
	connectTimeout time.Duration
	StatePath      string
	Interval       time.Duration
	Hostname       string
	Version        string
}

type savedCreds struct {
	Server  string   `json:"server,omitempty"`
	Servers []string `json:"servers,omitempty"`
	ID      string   `json:"id"`
	Secret  string   `json:"secret"`
}

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

// Run dials baked-in consoles with failover, enrolls or resumes, and sends
// metrics on the same TCP connection until cancelled.
func Run(ctx context.Context, opt Options) error {
	config, err := clientconfig.Read()
	if err != nil {
		return err
	}
	opt.connectTimeout = time.Duration(config.ConnectTimeoutSeconds) * time.Second
	updateError := opt.UpdateError
	opt.updateError = &updateError
	servers, err := serversFromBaked()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("this agent binary has no console addresses embedded; rebuild it from the console")
	}
	if opt.Interval <= 0 {
		opt.Interval = time.Duration(config.HeartbeatSeconds) * time.Second
	}
	if opt.Version == "" {
		opt.Version = version.Client
	}
	if opt.StatePath != "" {
		opt.journal, err = clientupdate.OpenJournal(filepath.Join(filepath.Dir(opt.StatePath), "update-requests.json"), opt.Version)
		if err != nil {
			return err
		}
	}
	creds, err := loadCreds(opt.StatePath, servers)
	if err != nil {
		return err
	}
	geoCtx, stopGeo := context.WithCancel(ctx)
	defer stopGeo()
	lookup := opt.GeoLookup
	if lookup == nil {
		lookup = geolocation.LookupSelf
	}
	opt.locations = newLocationReporter()
	go opt.locations.run(geoCtx, lookup)
	opt.loads = &loadRunner{}
	bandwidth := newBandwidthProbe()
	go bandwidth.run(ctx, func() bool {
		id, _, _ := opt.loads.report()
		return id != ""
	})
	opt.bandwidth = bandwidth
	backoff := time.Duration(config.RetrySeconds) * time.Second
	maxBackoff := time.Duration(config.MaxRetrySeconds) * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		server, sessionErr := runSession(ctx, opt, servers, creds)
		var ready *clientupdate.Ready
		if errors.As(sessionErr, &ready) {
			return ready
		}
		if sessionErr == nil {
			return nil
		}
		if errors.Is(sessionErr, errUnauthorized) {
			return sessionErr
		}
		if creds != nil && server != "" {
			creds.Server = server
			_ = storeCreds(opt.StatePath, *creds)
		}
		// Say why we are retrying, otherwise a stopped console looks like a hang.
		log.Printf("console unreachable (%s); retrying in %s", sessionErr, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < maxBackoff {
			backoff += 2 * time.Second
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func runSession(ctx context.Context, opt Options, servers []string, creds *savedCreds) (string, error) {
	conn, server, err := dialWithTimeout(ctx, servers, opt.connectTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()
	reader := bufio.NewReaderSize(conn, 8192)
	if creds == nil {
		enrolled, err := enroll(conn, reader, opt)
		if err != nil {
			return server, err
		}
		enrolled.Servers = servers
		enrolled.Server = server
		creds = enrolled
		if err := storeCreds(opt.StatePath, *creds); err != nil {
			return server, err
		}
	} else if err := auth(conn, reader, creds.Secret); err != nil {
		if errors.Is(err, errUnauthorized) && opt.StatePath != "" {
			_ = os.Remove(opt.StatePath)
		}
		return server, err
	} else {
		creds.Server = server
	}
	mux := newServerMux(conn, reader)
	sampler := hoststat.Sampler{}
	loads := opt.loads
	defer loads.stop()
	opt.locations.reconnect()
	var locationChanges <-chan struct{}
	if opt.locations != nil {
		locationChanges = opt.locations.changed
	}
	for {
		if err := beat(mux, opt, &sampler, loads, opt.bandwidth); err != nil {
			if errors.Is(err, errUnauthorized) && opt.StatePath != "" {
				_ = os.Remove(opt.StatePath)
			}
			if ctx.Err() != nil {
				return server, nil
			}
			return server, err
		}
		// During a load run, beat more often so the console tracks live
		// throughput instead of a stale sample.
		interval := opt.Interval
		if id, _, _ := loads.report(); id != "" && interval > loadBeatInterval {
			interval = loadBeatInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return server, nil
		case <-timer.C:
		case <-locationChanges:
			timer.Stop()
		}
	}
}

func dialFailover(ctx context.Context, servers []string) (net.Conn, string, error) {
	return dialWithTimeout(ctx, servers, 5*time.Second)
}

func dialWithTimeout(ctx context.Context, servers []string, timeout time.Duration) (net.Conn, string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	var last error
	for _, addr := range servers {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, addr, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("no servers configured")
	}
	return nil, "", last
}

var errUnauthorized = errors.New("unauthorized")

// loadBeatInterval is the heartbeat cadence while a load test is running.
const loadBeatInterval = 5 * time.Second

func enroll(conn net.Conn, reader *bufio.Reader, opt Options) (*savedCreds, error) {
	hostname := opt.Hostname
	if hostname == "" {
		var err error
		hostname, err = os.Hostname()
		if err != nil || hostname == "" {
			return nil, errors.New("hostname is unavailable")
		}
	}
	if err := writeMessage(conn, wireMessage{
		Op: "enroll", Hostname: hostname,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Version: opt.Version,
		LocationCapable: opt.locations != nil,
	}); err != nil {
		return nil, err
	}
	reply, err := readReply(conn, reader)
	if err != nil {
		return nil, err
	}
	if reply.Op == "err" {
		return nil, errors.New(reply.Error)
	}
	if reply.ID == "" || reply.Secret == "" {
		return nil, errors.New("controller returned an incomplete enrollment")
	}
	return &savedCreds{ID: reply.ID, Secret: reply.Secret}, nil
}

func auth(conn net.Conn, reader *bufio.Reader, secret string) error {
	if err := writeMessage(conn, wireMessage{Op: "auth", Secret: secret, LocationCapable: true}); err != nil {
		return err
	}
	reply, err := readReply(conn, reader)
	if err != nil {
		return err
	}
	if reply.Op == "err" {
		if reply.Error == "unauthorized" {
			return errUnauthorized
		}
		return errors.New(reply.Error)
	}
	return nil
}

func beat(mux *serverMux, opt Options, sampler *hoststat.Sampler, loads *loadRunner, bandwidth *bandwidthProbe) error {
	sample, err := sampler.Read()
	if err != nil {
		sample = hoststat.Sample{}
	}
	hostname := opt.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	loadTaskID, loadBytes, loadRate := loads.report()
	if err := mux.write(wireMessage{
		Op: "beat", Hostname: hostname, Version: opt.Version,
		Location: opt.locations.current(), LocationCapable: opt.locations != nil,
		UpdateCapable: clientupdate.PublicKey != "", UpdateError: *opt.updateError,
		UpdateIDs: opt.journal != nil, UpdateAckID: opt.journal.CompletedID(opt.Version),
		LoadTaskID: loadTaskID, LoadBytes: loadBytes, LoadRateBps: loadRate,
		BandwidthMbps: probeMbps(bandwidth),
		CPUPercent:    sample.CPUPercent, MemUsed: sample.MemUsed, MemTotal: sample.MemTotal,
		UptimeSec: int(time.Since(started).Seconds()),
	}); err != nil {
		return err
	}
	reply, err := mux.waitReply()
	if err != nil {
		return err
	}
	loads.sync(reply.Load)
	*opt.updateError = ""
	if reply.Update != nil {
		if err := reply.Update.VerifySignature(clientupdate.PublicKey, runtime.GOOS, runtime.GOARCH); err != nil {
			*opt.updateError = err.Error()
			return err
		}
		if opt.journal != nil {
			duplicate, err := opt.journal.Prepare(reply.RequestID, *reply.Update, opt.Version)
			if err != nil {
				*opt.updateError = err.Error()
				return err
			}
			if duplicate {
				return nil
			}
		}
		ready, err := receiveUpdate(mux, *reply.Update, opt.Version, reply.RequestID)
		if errors.Is(err, errUpdateBusy) {
			return nil
		}
		if err != nil {
			*opt.updateError = err.Error()
			return err
		}
		return ready
	}
	if reply.Op == "err" {
		if reply.Error == "unauthorized" {
			return errUnauthorized
		}
		return errors.New(reply.Error)
	}
	clientupdate.Confirm()
	return nil
}

var errUpdateBusy = errors.New("server update transfers are busy")

func probeMbps(p *bandwidthProbe) float64 {
	if p == nil {
		return 0
	}
	return p.Mbps()
}

func receiveUpdate(mux *serverMux, m clientupdate.Manifest, current string, requestID string) (*clientupdate.Ready, error) {
	if err := m.Verify(clientupdate.PublicKey, runtime.GOOS, runtime.GOARCH, current); err != nil {
		return nil, err
	}
	if err := mux.write(wireMessage{Op: "get-update", Version: m.Version, RequestID: requestID}); err != nil {
		return nil, err
	}
	reply, err := mux.readExclusive()
	if err != nil {
		return nil, err
	}
	if reply.Op == "update-busy" {
		mux.readMu.Unlock()
		return nil, errUpdateBusy
	}
	if reply.Op != "update" || reply.Update == nil || *reply.Update != m || reply.RequestID != requestID {
		mux.readMu.Unlock()
		return nil, errors.New("server did not provide the requested signed update")
	}
	exe, err := os.Executable()
	if err != nil {
		mux.readMu.Unlock()
		return nil, err
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".botnet-update-*"+suffix)
	if err != nil {
		mux.readMu.Unlock()
		return nil, err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = os.Remove(f.Name())
		}
	}()
	_ = mux.conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	if _, err := mux.readBinaryTo(f, m.Size); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Chmod(0o700); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := clientupdate.CheckFile(f.Name(), m); err != nil {
		return nil, err
	}
	ok = true
	return &clientupdate.Ready{Path: f.Name(), Manifest: m}, nil
}

func writeMessage(conn net.Conn, msg wireMessage) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}

func readReply(conn net.Conn, reader *bufio.Reader) (wireMessage, error) {
	return readReplyTimeout(conn, reader, 10*time.Second)
}

func readReplyTimeout(conn net.Conn, reader *bufio.Reader, timeout time.Duration) (wireMessage, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return wireMessage{}, err
	}
	var msg wireMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return wireMessage{}, err
	}
	return msg, nil
}

var started = time.Now()

func loadCreds(path string, servers []string) (*savedCreds, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var creds savedCreds
	if json.Unmarshal(data, &creds) != nil {
		return nil, errors.New("agent credential file is unreadable")
	}
	if creds.Secret == "" {
		return nil, nil
	}
	allowed := map[string]struct{}{}
	for _, addr := range servers {
		allowed[addr] = struct{}{}
	}
	if creds.Server != "" {
		if _, ok := allowed[creds.Server]; ok {
			return &creds, nil
		}
	}
	for _, addr := range creds.Servers {
		if _, ok := allowed[addr]; ok {
			return &creds, nil
		}
	}
	if creds.Server == "" && len(creds.Servers) == 0 {
		return &creds, nil
	}
	return nil, nil
}

func storeCreds(path string, creds savedCreds) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	return os.WriteFile(path, data, 0o600)
}

func normalizeAddr(server string) (string, error) {
	server = strings.TrimSpace(server)
	server = strings.TrimPrefix(server, "http://")
	server = strings.TrimPrefix(server, "https://")
	server = strings.TrimSuffix(server, "/")
	host, port, err := net.SplitHostPort(server)
	if err != nil || host == "" || port == "" {
		return "", errors.New("server must be host:port, for example 10.0.0.5:34116")
	}
	return net.JoinHostPort(host, port), nil
}

func normalizeServers(list []string) ([]string, error) {
	if len(list) == 0 {
		return nil, errors.New("at least one server host:port is required")
	}
	out := make([]string, 0, len(list))
	seen := map[string]struct{}{}
	for _, raw := range list {
		addr, err := normalizeAddr(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out, nil
}
