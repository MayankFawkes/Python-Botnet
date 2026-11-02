// Package monitor is the controller for explicitly installed monitoring agents.
// Agents report hostname, operating system, CPU, and memory. Operators can open
// an interactive shell on connected clients. Signed monitoring-client updates
// can be explicitly requested by the operator.
package monitor

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"atlas.local/simulator/internal/clientconfig"
	"atlas.local/simulator/internal/clientupdate"
	"atlas.local/simulator/internal/geolocation"
	"atlas.local/simulator/internal/version"
)

const (
	// LatestVersion is the latest client release, never the server release.
	LatestVersion = "v1"
)

// Config selects where the controller listens and where enrollment is stored.
type Config struct {
	ListenAddr        string
	AdvertiseURL      string
	StatePath         string
	ArtifactDir       string
	UseSavedSettings  bool
	OverrideListen    bool
	OverrideAdvertise bool
	Now               func() time.Time
	// OnChange is called with a fresh snapshot whenever state changes. It is
	// invoked from one background goroutine, never while the hub is locked.
	OnChange func(Snapshot)
	// OnShell delivers remote shell output to the console UI.
	OnShell func(ShellEvent)
}

// Agent is one enrolled machine as shown to the console.
type Agent struct {
	ID              string  `json:"id"`
	Hostname        string  `json:"hostname"`
	OS              string  `json:"os"`
	Arch            string  `json:"arch"`
	Version         string  `json:"version"`
	UpdateCapable   bool    `json:"updateCapable"`
	UpdateStatus    string  `json:"updateStatus,omitempty"`
	UpdateRequestID string  `json:"updateRequestId,omitempty"`
	Status          string  `json:"status"`
	CPU             float64 `json:"cpu"`
	MemUsed         uint64  `json:"memUsed"`
	MemTotal        uint64  `json:"memTotal"`
	Uptime          int     `json:"uptime"`
	LoadBytes       uint64  `json:"loadBytes"`
	LoadRateBps     int64   `json:"loadRateBps"`
	BandwidthMbps   float64 `json:"bandwidthMbps"`
	Remote          string  `json:"remote"`
	LastSeen        string  `json:"lastSeen"`
	// Site is the operator-declared location name, or the resolved city.
	Site string `json:"site,omitempty"`
	// Lat and Lon are set only when a location is known.
	Lat   float64 `json:"lat,omitempty"`
	Lon   float64 `json:"lon,omitempty"`
	GeoBy string  `json:"geoBy,omitempty"`
}

// MapPoint is one dot on the atlas with the agents it represents.
type MapPoint struct {
	Name  string   `json:"name"`
	Lat   float64  `json:"lat"`
	Lon   float64  `json:"lon"`
	Total int      `json:"total"`
	Hosts []string `json:"hosts"`
}

// Event is one session log line.
type Event struct {
	Time    string `json:"time"`
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// Snapshot is the console's current view.
type Snapshot struct {
	Settings        Settings `json:"settings"`
	RestartRequired bool     `json:"restartRequired"`
	Agents          []Agent  `json:"agents"`
	Events          []Event  `json:"events"`
	History         []int    `json:"history"`
	Elapsed         int      `json:"elapsed"`
	LatestVersion   string   `json:"latestVersion"`
	ServerVersion   string   `json:"serverVersion"`
	ClientVersion   string   `json:"clientVersion"`
	ListenAddr      string   `json:"listenAddr"`
	AdvertiseURL    string   `json:"advertiseURL"`
	ListenerError   string   `json:"listenerError"`
	// Points are the atlas dots, grouped by location.
	Points []MapPoint `json:"points"`
	// Unplaced counts online agents with no known location.
	Unplaced int `json:"unplaced"`
	// Sites are the operator-declared network maps.
	Sites []Site `json:"sites"`
	// Load is the current or most recent load-test run.
	Load LoadStatus `json:"load"`
}

// Heartbeat is a metrics report from an enrolled agent.
type Heartbeat struct {
	UpdateIDs       bool
	UpdateAckID     string
	Location        *geolocation.Place
	LocationCapable bool
	UpdateCapable   bool
	UpdateError     string
	Hostname        string
	Version         string
	CPU             float64
	MemUsed         uint64
	MemTotal        uint64
	Uptime          int
	LoadBytes       uint64
	LoadRateBps     int64
	LoadTaskID      string
	BandwidthMbps   float64
	RemoteAddr      string
}

type record struct {
	LoadBytes                                       uint64
	LoadRateBps                                     int64
	BandwidthMbps                                   float64
	LoadTaskID                                      string
	UpdateRequestID                                 string
	UpdateIDs                                       bool
	ReportedPlace                                   *geolocation.Place `json:"reportedPlace,omitempty"`
	ClientGeo                                       bool               `json:"clientGeo,omitempty"`
	UpdateCapable                                   bool
	UpdateStatus                                    string
	UpdateTarget                                    string
	PendingUpdate                                   *clientupdate.Manifest `json:"pendingUpdate,omitempty"`
	ID, Secret, Hostname, OS, Arch, Version, Remote string
	CPU                                             float64
	MemUsed, MemTotal                               uint64
	Uptime                                          int
	LastSeen                                        time.Time
	Connected                                       bool
	ConnGen                                         int
	OfflineNoted                                    bool
	// Place is resolved once per address and reused for later reconnects.
	Place *Place `json:"place,omitempty"`
	// GeoState records why a lookup produced no place, so it is not retried
	// on every snapshot.
	GeoState string `json:"geoState,omitempty"`
}

type diskState struct {
	Settings      *Settings `json:"settings,omitempty"`
	Agents        []record  `json:"agents"`
	Sites         []Site    `json:"sites"`
	ClientBuild   int       `json:"clientBuild"`
	ClientRelease int       `json:"clientRelease"`
	Events        []Event   `json:"events,omitempty"`
	History       []int     `json:"history,omitempty"`
}

// Hub tracks enrollment and heartbeats.
type Hub struct {
	settings      Settings
	updateSlots   chan struct{}
	updateOffers  map[string]time.Time
	loopDone      chan struct{}
	buildMu       sync.Mutex
	clientBuild   int
	clientRelease int
	mu            sync.Mutex
	cfg           Config
	agents        map[string]*record
	bySecret      map[string]*record
	events        []Event
	history       []int
	started       time.Time
	listenAddr    string
	listenErr     string
	ln            net.Listener
	stop          chan struct{}
	stopped       sync.Once
	dirty         chan struct{}
	sites         []Site
	loadTest      *loadRun
	geo           *geoLookup
	geoPending    map[string]bool
	geoSlots      chan struct{}
	liveConns     map[string]*liveConn
	shellMu       sync.Mutex
	shellOut      map[string][]ShellEvent
}

// New loads any saved enrollment and starts expiry tracking.
func New(cfg Config) *Hub {
	h := &Hub{
		cfg:          cfg,
		agents:       map[string]*record{},
		bySecret:     map[string]*record{},
		started:      time.Now(),
		stop:         make(chan struct{}),
		dirty:        make(chan struct{}, 1),
		geo:          newGeoLookup(),
		sites:        nil,
		geoPending:   map[string]bool{},
		geoSlots:     make(chan struct{}, 8),
		settings:     Settings{ListenAddr: cfg.ListenAddr, AdvertiseURL: cfg.AdvertiseURL, MaxClients: 20000, ClientDefaults: clientconfig.Default()},
		updateSlots:  make(chan struct{}, 8),
		updateOffers: map[string]time.Time{},
		loopDone:     make(chan struct{}),
	}
	if cfg.Now != nil {
		h.started = cfg.Now()
	}
	h.listenAddr = cfg.ListenAddr
	h.load()
	if cfg.UseSavedSettings {
		if cfg.OverrideListen {
			h.settings.ListenAddr = cfg.ListenAddr
		}
		if cfg.OverrideAdvertise {
			h.settings.AdvertiseURL = cfg.AdvertiseURL
		}
		h.cfg.ListenAddr = h.settings.ListenAddr
		h.cfg.AdvertiseURL = h.settings.AdvertiseURL
		h.listenAddr = h.settings.ListenAddr
	}
	go h.notifyLoop()
	return h
}

// notifyLoop delivers snapshots after state changes without locking the hub
// during the callback. Bursts of changes collapse into one delivery.
func (h *Hub) notifyLoop() {
	defer close(h.loopDone)
	for {
		select {
		case <-h.stop:
			return
		case <-h.dirty:
		}
		// Coalesce bursts: never serialize the full registry or push a complete
		// desktop snapshot for every individual heartbeat.
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-h.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		select {
		case <-h.dirty:
		default:
		}
		h.mu.Lock()
		if err := h.saveLocked(); err != nil {
			log.Printf("saving console state: %v", err)
		}
		h.mu.Unlock()
		if h.cfg.OnChange != nil {
			h.cfg.OnChange(h.Snapshot())
		}
	}
}

// markDirtyLocked schedules a snapshot notification. Callers hold h.mu.
func (h *Hub) markDirtyLocked() {
	select {
	case h.dirty <- struct{}{}:
	default:
	}
}

// Close stops the listener. There is no background poll.
func (h *Hub) Close() {
	h.stopped.Do(func() {
		close(h.stop)
		<-h.loopDone
		h.mu.Lock()
		ln := h.ln
		if err := h.saveLocked(); err != nil {
			log.Printf("saving console state on close: %v", err)
		}
		h.mu.Unlock()
		if ln != nil {
			_ = ln.Close()
		}
	})
}

func (h *Hub) now() time.Time {
	if h.cfg.Now != nil {
		return h.cfg.Now()
	}
	return time.Now()
}

func (h *Hub) event(kind, message string) {
	h.events = append([]Event{{h.now().Format("15:04:05"), message, kind}}, h.events...)
	if len(h.events) > 80 {
		h.events = h.events[:80]
	}
}

// SetListener records whether the agent port opened.
func (h *Hub) SetListener(addr string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.listenErr = err.Error()
		h.event("warning", "Agent listener failed: "+err.Error())
		h.markDirtyLocked()
		return
	}
	h.listenAddr = addr
	h.listenErr = ""
	h.event("success", "Agent listener ready at "+h.cfg.AdvertiseURL)
	h.markDirtyLocked()
}

// Reset drops enrolled agents and tokens. The development preview uses it
// between interface tests. It is not bound on the desktop console.
func (h *Hub) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents = map[string]*record{}
	h.bySecret = map[string]*record{}
	h.events = nil
	h.history = nil
	h.sites = nil
	h.loadTest = nil
	h.started = h.now()
	_ = h.saveLocked()
	if h.listenErr == "" && h.cfg.AdvertiseURL != "" {
		h.event("success", "Agent listener ready at "+h.cfg.AdvertiseURL)
	}
	h.markDirtyLocked()
}

func (h *Hub) rememberHistoryLocked() {
	online := 0
	for _, agent := range h.agents {
		if agent.Connected {
			online++
		}
	}
	if n := len(h.history); n > 0 && h.history[n-1] == online {
		return
	}
	h.history = append(h.history, online)
	if len(h.history) > 60 {
		h.history = h.history[len(h.history)-60:]
	}
}

func (h *Hub) onlineLocked(agent *record) bool { return agent.Connected }

// Attach marks an agent online for this TCP session. The returned generation
// lets an older connection ignore its own disconnect after a reconnect.
func (h *Hub) Attach(secret, remote string) int {
	return h.attach(secret, remote, false)
}

func (h *Hub) attach(secret, remote string, locationCapable bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.agentBySecret(secret)
	if agent == nil {
		return 0
	}
	agent.ConnGen++
	agent.ClientGeo = locationCapable
	if !locationCapable {
		agent.ReportedPlace = nil
	}
	agent.Connected = true
	agent.OfflineNoted = false
	agent.LastSeen = h.now()
	if remote != "" && remote != agent.Remote {
		agent.Remote = remote
		agent.Place = nil
		agent.ReportedPlace = nil
		agent.GeoState = ""
	}
	h.placeLocked(agent)
	h.rememberHistoryLocked()
	h.markDirtyLocked()
	return agent.ConnGen
}

// Detach marks that TCP session offline unless a newer one replaced it.
func (h *Hub) Detach(secret string, generation int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.agentBySecret(secret)
	if agent == nil || generation == 0 || agent.ConnGen != generation {
		return
	}
	agent.Connected = false
	if !agent.OfflineNoted {
		agent.OfflineNoted = true
		h.event("warning", agent.Hostname+" disconnected")
	}
	h.rememberHistoryLocked()
	h.markDirtyLocked()
}

// Snapshot returns a copy of the current console state.
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rememberHistoryLocked()
	return h.snapshotLocked()
}

func (h *Hub) snapshotLocked() Snapshot {
	agents := make([]Agent, 0, len(h.agents))
	for _, agent := range h.agents {
		status := "offline"
		if h.onlineLocked(agent) {
			status = "online"
		}
		seen := "—"
		if !agent.LastSeen.IsZero() {
			seen = agent.LastSeen.Format("15:04:05")
		}
		site, lat, lon, geoBy := h.placeLocked(agent)
		agents = append(agents, Agent{
			ID: agent.ID, Hostname: agent.Hostname, OS: agent.OS, Arch: agent.Arch,
			Version: agent.Version, Status: status, CPU: agent.CPU,
			UpdateCapable: agent.UpdateCapable, UpdateStatus: agent.UpdateStatus,
			UpdateRequestID: agent.UpdateRequestID,
			MemUsed:         agent.MemUsed, MemTotal: agent.MemTotal, Uptime: agent.Uptime,
			LoadBytes: agent.LoadBytes, LoadRateBps: agent.LoadRateBps,
			BandwidthMbps: agent.BandwidthMbps,
			Remote:        agent.Remote, LastSeen: seen,
			Site: site, Lat: lat, Lon: lon, GeoBy: geoBy,
		})
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Hostname == agents[j].Hostname {
			return agents[i].ID < agents[j].ID
		}
		return agents[i].Hostname < agents[j].Hostname
	})
	history := append([]int{}, h.history...)
	if history == nil {
		history = []int{}
	}
	events := append([]Event{}, h.events...)
	if events == nil {
		events = []Event{}
	}
	elapsed := int(h.now().Sub(h.started).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	points, unplaced := buildPoints(agents)
	return Snapshot{
		Settings: h.settings, RestartRequired: h.settings.ListenAddr != h.cfg.ListenAddr,
		Agents: agents, Events: events, History: history,
		Elapsed: elapsed, LatestVersion: h.clientVersionLocked(),
		ServerVersion: version.Server, ClientVersion: h.clientVersionLocked(),
		ListenAddr: h.listenAddr, AdvertiseURL: h.cfg.AdvertiseURL, ListenerError: h.listenErr,
		Points: points, Unplaced: unplaced, Sites: append([]Site{}, h.sites...),
		Load: h.loadStatusLocked(),
	}
}

// buildPoints groups agents that share a location into one dot. Only agents
// with known coordinates are placed; the rest are counted as unplaced.
func buildPoints(agents []Agent) ([]MapPoint, int) {
	byKey := map[string]*MapPoint{}
	unplaced := 0
	for _, agent := range agents {
		if agent.Status != "online" {
			continue
		}
		if agent.Lat == 0 && agent.Lon == 0 && agent.Site == "" {
			if agent.Status == "online" {
				unplaced++
			}
			continue
		}
		// Coordinates, not city names, identify locations: different cities can
		// share a name, and different networks can share one physical location.
		key := fmt.Sprintf("%.5f,%.5f", agent.Lat, agent.Lon)
		point := byKey[key]
		if point == nil {
			point = &MapPoint{Name: agent.Site, Lat: agent.Lat, Lon: agent.Lon}
			byKey[key] = point
		}
		point.Total++
		if len(point.Hosts) < 20 {
			point.Hosts = append(point.Hosts, agent.Hostname)
		}
	}
	points := make([]MapPoint, 0, len(byKey))
	for _, point := range byKey {
		sort.Strings(point.Hosts)
		points = append(points, *point)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Total == points[j].Total {
			return points[i].Name < points[j].Name
		}
		return points[i].Total > points[j].Total
	})
	if points == nil {
		points = []MapPoint{}
	}
	return points, unplaced
}

// placeLocked resolves an agent's atlas position. Declared networks win over
// geolocation, and public lookups happen in the background so no snapshot ever
// blocks on the network.
func (h *Hub) placeLocked(agent *record) (site string, lat, lon float64, geoBy string) {
	if declared := h.siteForLocked(agent.Remote); declared != nil {
		return declared.Name, declared.Lat, declared.Lon, "declared"
	}
	if agent.ReportedPlace != nil {
		return agent.ReportedPlace.Name, agent.ReportedPlace.Lat, agent.ReportedPlace.Lon, "client"
	}
	if !agent.ClientGeo && agent.Place != nil {
		return agent.Place.Name, agent.Place.Lat, agent.Place.Lon, "geo"
	}
	if !agent.ClientGeo && agent.GeoState == "" && h.onlineLocked(agent) && !h.geoPending[agent.Remote] {
		h.queueGeoLocked(agent)
	}
	return "", 0, 0, ""
}

// queueGeoLocked starts a background lookup for one address, or records why
// no lookup is possible.
func (h *Hub) queueGeoLocked(agent *record) {
	addr := agent.Remote
	if addr == "" {
		agent.GeoState = "no address"
		return
	}
	if ip := net.ParseIP(addr); ip != nil && !isPublicIP(ip) {
		// Private VLAN space carries no coordinates, and must not be sent to
		// an outside service.
		agent.GeoState = "private"
		return
	}
	if h.geoPending == nil {
		h.geoPending = map[string]bool{}
	}
	h.geoPending[addr] = true
	go func() {
		select {
		case h.geoSlots <- struct{}{}:
		case <-h.stop:
			return
		}
		place, err := h.geo.Lookup(addr)
		<-h.geoSlots
		h.mu.Lock()
		select {
		case <-h.stop:
			h.mu.Unlock()
			return
		default:
		}
		delete(h.geoPending, addr)
		for _, candidate := range h.agents {
			if candidate.Remote != addr {
				continue
			}
			if err != nil {
				candidate.GeoState = "unresolved"
				continue
			}
			candidate.Place = place
			candidate.GeoState = ""
		}
		if err == nil {
			if saveErr := h.saveLocked(); saveErr != nil {
				h.event("warning", "Could not save atlas locations: "+saveErr.Error())
			}
		}
		h.markDirtyLocked()
		h.mu.Unlock()
	}()
}

// Revoke removes an agent. Later heartbeats from that agent are rejected.
func (h *Hub) Revoke(id string) (Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.agents[id]
	if agent == nil {
		return Snapshot{}, errors.New("unknown agent")
	}
	delete(h.agents, id)
	delete(h.bySecret, agent.Secret)
	if err := h.saveLocked(); err != nil {
		h.agents[id] = agent
		h.bySecret[agent.Secret] = agent
		return Snapshot{}, err
	}
	h.event("info", agent.Hostname+" revoked")
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}

// Enroll registers a new agent. Lab consoles accept the first connection from
// a built agent binary that already knows this listener address.
func (h *Hub) Enroll(hostname, osName, arch, version, remote string) (id, secret string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := validateName(hostname, "hostname"); err != nil {
		return "", "", err
	}
	if err := validateName(osName, "os"); err != nil {
		return "", "", err
	}
	if err := validateName(arch, "arch"); err != nil {
		return "", "", err
	}
	if err := validateVersion(version); err != nil {
		return "", "", err
	}
	// A client that lost its stored credential re-enrolls instead of
	// reconnecting. Reuse the record for the same host at the same address so
	// the registry does not fill with duplicates. The address is part of the
	// key because hostnames alone are not unique across machines.
	for _, existing := range h.agents {
		if existing.Hostname != hostname || existing.Remote != remote {
			continue
		}
		delete(h.bySecret, existing.Secret)
		secret, err = randomHex(32)
		if err != nil {
			h.bySecret[existing.Secret] = existing
			return "", "", err
		}
		existing.Secret = secret
		existing.OS = osName
		existing.Arch = arch
		existing.Version = version
		existing.Remote = remote
		existing.LastSeen = h.now()
		existing.Place = nil
		existing.GeoState = ""
		h.bySecret[secret] = existing
		if err := h.saveLocked(); err != nil {
			return "", "", err
		}
		h.event("info", hostname+" re-enrolled from "+remote)
		h.markDirtyLocked()
		return existing.ID, secret, nil
	}
	if len(h.agents) >= h.settings.MaxClients {
		return "", "", errors.New("agent limit reached")
	}
	secret, err = randomHex(32)
	if err != nil {
		return "", "", err
	}
	id = "ag-" + secret[:8]
	agent := &record{
		ID: id, Secret: secret, Hostname: hostname, OS: osName, Arch: arch,
		Version: version, Remote: remote, LastSeen: h.now(),
	}
	h.agents[id] = agent
	h.bySecret[secret] = agent
	if err := h.saveLocked(); err != nil {
		delete(h.agents, id)
		delete(h.bySecret, secret)
		return "", "", err
	}
	h.event("success", hostname+" enrolled from "+remote)
	h.markDirtyLocked()
	return id, secret, nil
}

// Beat applies a metrics report authorized by the agent secret.
func (h *Hub) Beat(secret string, beat Heartbeat) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.agentBySecret(secret)
	if agent == nil {
		return errors.New("unauthorized")
	}
	if beat.Location != nil {
		if err := beat.Location.Validate(); err != nil {
			return err
		}
	}
	if beat.CPU < 0 || beat.CPU > 100 {
		return errors.New("cpu percent must be between 0 and 100")
	}
	if beat.MemTotal > 0 && beat.MemUsed > beat.MemTotal {
		return errors.New("memory used exceeds memory total")
	}
	if beat.Uptime < 0 {
		return errors.New("uptime must be positive")
	}
	if beat.Hostname != "" {
		if err := validateName(beat.Hostname, "hostname"); err != nil {
			return err
		}
		agent.Hostname = beat.Hostname
	}
	if beat.Version != "" {
		if err := validateVersion(beat.Version); err != nil {
			return err
		}
		agent.Version = beat.Version
	}
	agent.CPU = beat.CPU
	agent.ClientGeo = beat.LocationCapable || beat.Location != nil
	if beat.Location != nil {
		copy := *beat.Location
		agent.ReportedPlace = &copy
	}
	agent.UpdateCapable = beat.UpdateCapable
	agent.UpdateIDs = beat.UpdateIDs
	if beat.UpdateAckID != "" && beat.UpdateAckID == agent.UpdateRequestID && agent.Version == agent.UpdateTarget {
		if agent.UpdateStatus != "Updated" {
			h.event("success", agent.Hostname+" acknowledged signed update "+beat.UpdateAckID)
		}
		agent.UpdateStatus = "Updated"
		agent.PendingUpdate = nil
		delete(h.updateOffers, agent.Secret)
	}
	if beat.UpdateError != "" {
		if len(beat.UpdateError) > 512 {
			beat.UpdateError = beat.UpdateError[:512]
		}
		agent.UpdateStatus = "Failed: " + beat.UpdateError
		agent.PendingUpdate = nil
		h.event("warning", agent.Hostname+" update failed: "+beat.UpdateError)
	} else if agent.UpdateStatus == "Installing" {
		if agent.Version == agent.UpdateTarget {
			agent.UpdateStatus = "Updated"
			h.event("success", agent.Hostname+" updated to "+agent.Version)
		} else {
			agent.UpdateStatus = "Failed: client reconnected without the requested version"
		}
	}
	agent.MemUsed = beat.MemUsed
	agent.MemTotal = beat.MemTotal
	agent.Uptime = beat.Uptime
	if beat.LoadTaskID != "" {
		agent.LoadTaskID = beat.LoadTaskID
		agent.LoadBytes = beat.LoadBytes
		agent.LoadRateBps = beat.LoadRateBps
	} else {
		agent.LoadRateBps = 0
	}
	if beat.BandwidthMbps < 0 || beat.BandwidthMbps > 1e6 {
		return errors.New("bandwidth is out of range")
	}
	agent.BandwidthMbps = beat.BandwidthMbps
	if beat.RemoteAddr != "" {
		agent.Remote = beat.RemoteAddr
	}
	agent.LastSeen = h.now()
	h.markDirtyLocked()
	return nil
}

func (h *Hub) agentBySecret(secret string) *record {
	agent := h.bySecret[secret]
	if agent == nil {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(agent.Secret), []byte(secret)) != 1 {
		return nil
	}
	return agent
}

func validateName(value, field string) error {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 64 {
		return fmt.Errorf("%s must be 1–64 characters", field)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return fmt.Errorf("%s contains unsupported characters", field)
		}
	}
	return nil
}

func validateVersion(version string) error {
	if len(version) < 1 || len(version) > 32 {
		return errors.New("version must be 1–32 characters")
	}
	for _, r := range version {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == '+') {
			return errors.New("version contains unsupported characters")
		}
	}
	return nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (h *Hub) load() {
	if h.cfg.StatePath == "" {
		return
	}
	data, err := os.ReadFile(h.cfg.StatePath)
	if err != nil {
		return
	}
	var disk diskState
	if json.Unmarshal(data, &disk) != nil {
		return
	}
	for i := range disk.Agents {
		agent := disk.Agents[i]
		if agent.PendingUpdate != nil && agent.UpdateRequestID == "" {
			agent.UpdateRequestID, _ = randomHex(16)
		}
		if agent.ID == "" || agent.Secret == "" {
			continue
		}
		copied := agent
		copied.Connected = false
		h.agents[agent.ID] = &copied
		h.bySecret[agent.Secret] = &copied
	}
	h.sites = disk.Sites
	h.clientBuild = disk.ClientBuild
	h.clientRelease = disk.ClientRelease
	h.events = disk.Events
	h.history = disk.History
	if disk.Settings != nil && disk.Settings.Validate() == nil {
		h.settings = *disk.Settings
	}
}

func (h *Hub) saveLocked() error {
	if h.cfg.StatePath == "" {
		return nil
	}
	disk := diskState{Settings: &h.settings, Sites: h.sites, ClientBuild: h.clientBuild, ClientRelease: h.clientRelease, Events: h.events, History: h.history}
	for _, agent := range h.agents {
		disk.Agents = append(disk.Agents, *agent)
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(h.cfg.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp := h.cfg.StatePath + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, h.cfg.StatePath)
}
