package monitor

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// geoLookup resolves a public address to coordinates. Private and loopback
// addresses are rejected before any request is made, because no service can
// place them and sending them would leak internal topology.
type geoLookup struct {
	mu     sync.Mutex
	client *http.Client
	cache  map[string]*Place
	failed map[string]bool
}

// Place is a resolved map position for one address.
type Place struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

func newGeoLookup() *geoLookup {
	return &geoLookup{
		client: &http.Client{Timeout: 6 * time.Second},
		cache:  map[string]*Place{},
		failed: map[string]bool{},
	}
}

// ipwhoResponse is the subset of https://ipwho.is/ that the atlas uses.
type ipwhoResponse struct {
	Success bool    `json:"success"`
	Message string  `json:"message"`
	IP      string  `json:"ip"`
	City    string  `json:"city"`
	Region  string  `json:"region"`
	Country string  `json:"country"`
	Lat     float64 `json:"latitude"`
	Lon     float64 `json:"longitude"`
}

// Lookup returns a place for a public address. It caches both hits and misses
// so a reconnect does not repeat the request.
func (g *geoLookup) Lookup(addr string) (*Place, error) {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return nil, errors.New("not an IP address")
	}
	key := ip.String()
	g.mu.Lock()
	cached, hit := g.cache[key]
	skip := g.failed[key]
	g.mu.Unlock()
	if hit {
		return cached, nil
	}
	if skip {
		return nil, errors.New("lookup already failed for this address")
	}
	if !isPublicIP(ip) {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		return nil, errors.New("address is private or reserved")
	}
	response, err := g.client.Get("https://ipwho.is/" + key + "?fields=success,message,ip,city,region,country,latitude,longitude")
	if err != nil {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		return nil, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	if response.StatusCode != http.StatusOK {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		return nil, errors.New("geolocation service returned " + response.Status)
	}
	var payload ipwhoResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		return nil, errors.New("geolocation response was unreadable")
	}
	if !payload.Success {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		message := payload.Message
		if message == "" {
			message = "geolocation lookup failed"
		}
		return nil, errors.New(message)
	}
	name := payload.City
	if name == "" {
		name = payload.Region
	}
	if name == "" {
		name = payload.Country
	}
	if name == "" {
		name = key
	}
	place := &Place{Name: name, Lat: payload.Lat, Lon: payload.Lon}
	if place.Lat < -90 || place.Lat > 90 || place.Lon < -180 || place.Lon > 180 {
		g.mu.Lock()
		g.failed[key] = true
		g.mu.Unlock()
		return nil, errors.New("geolocation returned coordinates outside the map")
	}
	g.mu.Lock()
	g.cache[key] = place
	g.mu.Unlock()
	return place, nil
}

// isPublicIP rejects ranges that carry no geographic meaning.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// 100.64.0.0/10 carrier-grade NAT, and the IPv4 benchmark range.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
		if v4[0] == 0 {
			return false
		}
	}
	if len(ip) == net.IPv6len && ip.To4() == nil {
		// Unique local addresses fc00::/7.
		if ip[0]&0xfe == 0xfc {
			return false
		}
	}
	return true
}
