package monitor

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
)

// Site maps a network range to a place on the atlas. Private VLAN addresses do
// not carry a geographic position, so operators declare the mapping once and
// every agent whose address falls inside the range is grouped under it.
type Site struct {
	Name string  `json:"name"`
	CIDR string  `json:"cidr"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// maxSites caps the list so a bad import cannot flood the console.
const maxSites = 256

// Sites returns the configured mappings, sorted by name.
func (h *Hub) Sites() []Site {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Site{}, h.sites...)
}

// AddSite validates and stores a map location. The CIDR decides which agents
// appear at that point; the coordinates are chosen by the operator.
func (h *Hub) AddSite(name, cidr string, lat, lon float64) (Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name = strings.TrimSpace(name)
	if err := validateSiteName(name); err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(cidr) == "" {
		// Fall back to the /24 around the first unplaced agent so a name and a
		// position are enough to get dots on the map.
		cidr = h.suggestedCIDRLocked()
		if cidr == "" {
			return Snapshot{}, errors.New("network is required, for example 10.10.10.0/24")
		}
	}
	network, err := parseNetwork(cidr)
	if err != nil {
		return Snapshot{}, err
	}
	if lat < -90 || lat > 90 {
		return Snapshot{}, errors.New("latitude must be between -90 and 90")
	}
	if lon < -180 || lon > 180 {
		return Snapshot{}, errors.New("longitude must be between -180 and 180")
	}
	// 0,0 is in the Atlantic and is almost always an empty form field rather
	// than a real site. Reject it unless the operator is explicit.
	if lat == 0 && lon == 0 {
		return Snapshot{}, errors.New("latitude and longitude are empty; pick a position on the map")
	}
	if len(h.sites) >= maxSites {
		return Snapshot{}, errors.New("site limit reached")
	}
	for _, existing := range h.sites {
		if existing.CIDR == network {
			return Snapshot{}, errors.New("that network already has a location")
		}
	}
	h.sites = append(h.sites, Site{Name: name, CIDR: network, Lat: lat, Lon: lon})
	sort.Slice(h.sites, func(i, j int) bool { return h.sites[i].Name < h.sites[j].Name })
	if err := h.saveLocked(); err != nil {
		h.sites = h.sites[:len(h.sites)-1]
		return Snapshot{}, err
	}
	h.event("info", "Location added for "+network)
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}

// RemoveSite drops a mapping. Agents are not disconnected; they simply stop
// being placed on the atlas.
func (h *Hub) RemoveSite(cidr string) (Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, site := range h.sites {
		if site.CIDR != cidr {
			continue
		}
		removed := h.sites[i]
		h.sites = append(h.sites[:i], h.sites[i+1:]...)
		if err := h.saveLocked(); err != nil {
			h.sites = append(h.sites, removed)
			sort.Slice(h.sites, func(a, b int) bool { return h.sites[a].Name < h.sites[b].Name })
			return Snapshot{}, err
		}
		h.event("info", "Location removed for "+removed.CIDR)
		h.markDirtyLocked()
		return h.snapshotLocked(), nil
	}
	return Snapshot{}, errors.New("unknown location")
}

// suggestedCIDRLocked guesses a /24 from an agent that has no location yet.
func (h *Hub) suggestedCIDRLocked() string {
	for _, agent := range h.agents {
		ip := net.ParseIP(strings.TrimSpace(agent.Remote))
		if ip == nil || h.siteForLocked(agent.Remote) != nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			return fmt.Sprintf("%d.%d.%d.0/24", v4[0], v4[1], v4[2])
		}
	}
	return ""
}

// siteForLocked finds the most specific declared network containing addr.
func (h *Hub) siteForLocked(addr string) *Site {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return nil
	}
	var best *Site
	bestBits := -1
	for i := range h.sites {
		_, network, err := net.ParseCIDR(h.sites[i].CIDR)
		if err != nil || !network.Contains(ip) {
			continue
		}
		ones, _ := network.Mask.Size()
		if ones > bestBits {
			bestBits = ones
			best = &h.sites[i]
		}
	}
	return best
}

func parseNetwork(cidr string) (string, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return "", errors.New("network is required, for example 10.10.10.0/24")
	}
	// Accept a bare address by treating it as a single host.
	if !strings.Contains(cidr, "/") {
		ip := net.ParseIP(cidr)
		if ip == nil {
			return "", errors.New("network must be CIDR, for example 10.10.10.0/24")
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		cidr = fmt.Sprintf("%s/%d", ip.String(), bits)
	}
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", errors.New("network must be CIDR, for example 10.10.10.0/24")
	}
	return network.String(), nil
}

func validateSiteName(name string) error {
	if len(name) < 1 || len(name) > 48 {
		return errors.New("location name must be 1–48 characters")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return errors.New("location name contains unsupported characters")
		}
	}
	return nil
}
