// Package geolocation resolves a monitoring client's own public-IP location.
package geolocation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type Place struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

func (p Place) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 160 {
		return errors.New("location name must be 1–160 characters")
	}
	for _, r := range p.Name {
		if unicode.IsControl(r) {
			return errors.New("location name contains control characters")
		}
	}
	if math.IsNaN(p.Lat) || math.IsInf(p.Lat, 0) || math.IsNaN(p.Lon) || math.IsInf(p.Lon, 0) || p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return errors.New("invalid location coordinates")
	}
	return nil
}

// LookupSelf deliberately sends no address: ipwho.is resolves the caller's
// public egress IP, including when its console link uses a private/LAN address.
func LookupSelf(ctx context.Context) (*Place, error) {
	return lookup(ctx, &http.Client{Timeout: 6 * time.Second}, "https://ipwho.is/?fields=success,message,ip,city,region,country,latitude,longitude")
}

func lookup(ctx context.Context, client *http.Client, url string) (*Place, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("location lookup returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil {
		return nil, err
	}
	if len(data) > 8192 {
		return nil, errors.New("location response too large")
	}
	var payload struct {
		Success bool     `json:"success"`
		City    string   `json:"city"`
		Region  string   `json:"region"`
		Country string   `json:"country"`
		IP      string   `json:"ip"`
		Lat     *float64 `json:"latitude"`
		Lon     *float64 `json:"longitude"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, errors.New("unreadable location response")
	}
	if !payload.Success {
		return nil, errors.New("location service could not resolve this client")
	}
	if payload.Lat == nil || payload.Lon == nil {
		return nil, errors.New("location response has no coordinates")
	}
	name := payload.City
	if name == "" {
		name = payload.Region
	}
	if name == "" {
		name = payload.Country
	}
	if name == "" {
		name = payload.IP
	}
	place := &Place{Name: name, Lat: *payload.Lat, Lon: *payload.Lon}
	return place, place.Validate()
}
