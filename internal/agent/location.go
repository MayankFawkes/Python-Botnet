package agent

import (
	"context"
	"log"
	"sync"
	"time"

	"atlas.local/simulator/internal/geolocation"
)

type locationReporter struct {
	mu          sync.Mutex
	place       *geolocation.Place
	lastAttempt time.Time
	connected   bool
	changed     chan struct{}
	refresh     chan struct{}
}

func newLocationReporter() *locationReporter {
	return &locationReporter{lastAttempt: time.Now(), changed: make(chan struct{}, 1), refresh: make(chan struct{}, 1)}
}

func (r *locationReporter) current() *geolocation.Place {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.place == nil {
		return nil
	}
	copy := *r.place
	return &copy
}

func (r *locationReporter) reconnect() {
	if r == nil {
		return
	}
	r.mu.Lock()
	first := !r.connected
	r.connected = true
	r.mu.Unlock()
	// The startup lookup covers the first connection. Later connections always
	// schedule a refresh, even if we must wait briefly for the rate limit.
	if first {
		return
	}
	select {
	case r.refresh <- struct{}{}:
	default:
	}
}

func (r *locationReporter) run(ctx context.Context, lookup func(context.Context) (*geolocation.Place, error)) {
	for {
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		r.lastAttempt = time.Now()
		r.mu.Unlock()
		place, err := lookup(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil && place != nil {
			err = place.Validate()
		}
		delay := time.Minute
		if err == nil && place != nil {
			r.mu.Lock()
			copy := *place
			r.place = &copy
			r.mu.Unlock()
			select {
			case r.changed <- struct{}{}:
			default:
			}
			delay = 6 * time.Hour
		} else {
			log.Printf("Client location unavailable; retrying in one minute")
		}
		timer := time.NewTimer(delay)
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-r.refresh:
				r.mu.Lock()
				remaining := time.Until(r.lastAttempt.Add(time.Minute))
				r.mu.Unlock()
				if remaining < 0 {
					remaining = 0
				}
				timer.Reset(remaining)
			case <-timer.C:
				waiting = false
			}
		}
	}
}
