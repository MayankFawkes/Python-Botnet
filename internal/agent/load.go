package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"atlas.local/simulator/internal/loadgen"
)

// loadRunner executes at most one console-issued load task at a time. It paces
// sends to the task's rate and always stops at the task deadline, even if the
// console disconnects.
type loadRunner struct {
	cancel context.CancelFunc
	bytes  atomic.Uint64
	rate   atomic.Int64 // bytes sent in the last measured second
	mu     sync.Mutex
	taskID string
	done   chan struct{}
}

// sync starts, stops or replaces the running task. A nil task stops the run.
func (r *loadRunner) sync(task *loadgen.Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task == nil || task.ID != r.taskID {
		r.stopLocked()
	}
	if task != nil && r.taskID == "" {
		r.startLocked(*task)
	}
}

// report returns the task ID, cumulative bytes and current per-second rate
// without changing the run.
func (r *loadRunner) report() (string, uint64, int64) {
	r.mu.Lock()
	id := r.taskID
	r.mu.Unlock()
	return id, r.bytes.Load(), r.rate.Load()
}

func (r *loadRunner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

func (r *loadRunner) stopLocked() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.done != nil {
		<-r.done
	}
	r.cancel = nil
	r.done = nil
	r.taskID = ""
	r.bytes.Store(0)
	r.rate.Store(0)
}

func (r *loadRunner) startLocked(task loadgen.Task) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(task.DeadlineUnix, 0))
	r.cancel = cancel
	r.taskID = task.ID
	r.bytes.Store(0)
	r.rate.Store(0)
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		runLoad(ctx, task, &r.bytes)
	}()
	// Track the send rate over a sliding five-second window so heartbeats
	// report stable throughput instead of a jittery one-second sample.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		const window = 5
		samples := make([]uint64, 0, window+1)
		for {
			select {
			case <-ctx.Done():
				r.rate.Store(0)
				return
			case <-ticker.C:
				samples = append(samples, r.bytes.Load())
				if len(samples) > window+1 {
					samples = samples[1:]
				}
				if len(samples) >= 2 {
					delta := samples[len(samples)-1] - samples[0]
					r.rate.Store(int64(delta) / int64(len(samples)-1))
				}
			}
		}
	}()
}

// runLoad sends paced traffic until the context deadline. Each method loops
// with a token bucket so the average send rate matches the task's percent of
// the per-agent ceiling.
func runLoad(ctx context.Context, task loadgen.Task, counter *atomic.Uint64) {
	rate := task.RateBps()
	if rate <= 0 {
		return
	}
	switch task.Method {
	case loadgen.MethodUDP:
		udpLoop(ctx, task, counter, rate)
	case loadgen.MethodTCP:
		tcpLoop(ctx, task, counter, rate)
	case loadgen.MethodHTTP:
		httpLoop(ctx, task, counter, rate)
	}
}

// pace sleeps for the share of one second that size bytes represents at rate.
func pace(ctx context.Context, sent int64, start time.Time, rate int64) {
	due := time.Duration(sent * int64(time.Second) / rate)
	if wait := due - time.Since(start); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func udpLoop(ctx context.Context, task loadgen.Task, counter *atomic.Uint64, rate int64) {
	conn, err := net.Dial("udp", task.Address())
	if err != nil {
		return
	}
	defer conn.Close()
	payload := make([]byte, 1200)
	start := time.Now()
	var sent int64
	for ctx.Err() == nil {
		n, err := conn.Write(payload)
		if err != nil {
			return
		}
		sent += int64(n)
		counter.Store(uint64(sent))
		pace(ctx, sent, start, rate)
	}
}

func tcpLoop(ctx context.Context, task loadgen.Task, counter *atomic.Uint64, rate int64) {
	payload := make([]byte, 16*1024)
	start := time.Now()
	var sent int64
	for ctx.Err() == nil {
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", task.Address())
		if err != nil {
			if !sleepOrDone(ctx, time.Second) {
				return
			}
			continue
		}
		for ctx.Err() == nil {
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			n, err := conn.Write(payload)
			if n > 0 {
				sent += int64(n)
				counter.Store(uint64(sent))
			}
			if err != nil {
				break
			}
			pace(ctx, sent, start, rate)
		}
		conn.Close()
	}
}

func httpLoop(ctx context.Context, task loadgen.Task, counter *atomic.Uint64, rate int64) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + task.Address() + "/"
	start := time.Now()
	var sent int64
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		response, err := client.Do(req)
		if err != nil {
			if !sleepOrDone(ctx, time.Second) {
				return
			}
			continue
		}
		// Sent bytes approximate the request line and headers; received bytes
		// drain so connections can be reused, but only sent bytes count.
		sent += int64(len(url) + 64)
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		counter.Store(uint64(sent))
		pace(ctx, sent, start, rate)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
