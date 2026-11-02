package agent

import (
	"context"
	"io"
	"math"
	"net/http"
	"sync/atomic"
	"time"
)

// bandwidthProbe periodically measures the agent's real internet download
// throughput by timing a small file download, and reports megabits per second.
// It is an active measurement, not the interface link speed.
type bandwidthProbe struct {
	bits atomic.Uint64 // math.Float64bits of the last measured Mbps
}

const (
	// probeURL streams discardable bytes; about 10 MB keeps the measurement
	// under a few seconds on a slow link without much data use.
	probeURL            = "https://speed.cloudflare.com/__down?bytes=10000000"
	probeMaxBytes       = 10 << 20
	probeTimeout        = 15 * time.Second
	probeInterval       = 10 * time.Minute
	probeInitialDelay   = 20 * time.Second
	probeFailureBackoff = time.Minute
)

func newBandwidthProbe() *bandwidthProbe { return &bandwidthProbe{} }

// Mbps returns the last measured download speed, or 0 before the first probe.
func (p *bandwidthProbe) Mbps() float64 {
	return math.Float64frombits(p.bits.Load())
}

// run probes shortly after startup, then every probeInterval. Failed probes
// retry after a minute. It never blocks heartbeats.
func (p *bandwidthProbe) run(ctx context.Context, isBusy func() bool) {
	timer := time.NewTimer(probeInitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// Skip while a load test is running so the measurement reflects the
		// link, not the test, and so the probe does not distort the run.
		if isBusy == nil || !isBusy() {
			if mbps, err := measureDownload(ctx); err == nil {
				p.bits.Store(math.Float64bits(mbps))
				timer.Reset(probeInterval)
				continue
			}
		}
		timer.Reset(probeFailureBackoff)
	}
}

// measureDownload times a bounded download and returns megabits per second.
func measureDownload(ctx context.Context) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	received, err := io.Copy(io.Discard, io.LimitReader(response.Body, probeMaxBytes))
	elapsed := time.Since(start)
	if err != nil && received == 0 {
		return 0, err
	}
	if received < 1<<19 || elapsed <= 0 {
		// Too little data for a meaningful number.
		return 0, io.ErrUnexpectedEOF
	}
	return float64(received) * 8 / elapsed.Seconds() / 1e6, nil
}
