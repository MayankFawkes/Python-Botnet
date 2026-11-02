package monitor

import (
	"errors"
	"strconv"
	"time"

	"atlas.local/simulator/internal/loadgen"
)

// LoadStatus is the console view of the current or most recent load-test run.
type LoadStatus struct {
	Task         *loadgen.Task `json:"task,omitempty"`
	Active       bool          `json:"active"`
	Agents       int           `json:"agents"`
	TotalBytes   uint64        `json:"totalBytes"`
	TotalRateBps int64         `json:"totalRateBps"`
	EndsAt       string        `json:"endsAt,omitempty"`
}

// loadRun is the hub's active task. Byte counters live on each agent record.
type loadRun struct {
	task     loadgen.Task
	deadline time.Time
}

// StartLoad validates a task and queues it for every online agent at their
// next heartbeat. The operator enters the target explicitly for each run.
func (h *Hub) StartLoad(target string, port int, method string, percent, duration int) (Snapshot, error) {
	task, err := loadgen.Normalize(target, port, method, percent, duration)
	if err != nil {
		return Snapshot{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id, err := randomHex(8)
	if err != nil {
		return Snapshot{}, err
	}
	task.ID = id
	deadline := h.now().Add(time.Duration(task.DurationSeconds) * time.Second)
	task.DeadlineUnix = deadline.Unix()
	h.loadTest = &loadRun{task: task, deadline: deadline}
	// Counters belong to one run; reset them so totals describe this run only.
	for _, agent := range h.agents {
		agent.LoadBytes = 0
	}
	h.event("info", "Load test "+id+" started: "+task.Method+" to "+task.Address()+" at "+strconv.Itoa(task.Percent)+"% for "+strconv.Itoa(task.DurationSeconds)+"s")
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}

// StopLoad ends the active run. Agents stop at the next heartbeat or at the
// task deadline, whichever comes first.
func (h *Hub) StopLoad() (Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.loadTest == nil {
		return Snapshot{}, errors.New("no load test is running")
	}
	h.event("info", "Load test "+h.loadTest.task.ID+" stopped by operator")
	h.loadTest = nil
	h.markDirtyLocked()
	return h.snapshotLocked(), nil
}

// loadOfferLocked returns the task for an agent's beat reply, expiring runs
// whose deadline has passed. Callers hold h.mu.
func (h *Hub) loadOfferLocked() *loadgen.Task {
	if h.loadTest == nil {
		return nil
	}
	if !h.now().Before(h.loadTest.deadline) {
		h.event("info", "Load test "+h.loadTest.task.ID+" completed")
		h.loadTest = nil
		return nil
	}
	task := h.loadTest.task
	return &task
}

// loadStatusLocked summarizes the run for the snapshot. Callers hold h.mu.
func (h *Hub) loadStatusLocked() LoadStatus {
	var status LoadStatus
	var taskID string
	if h.loadTest != nil {
		status.Active = true
		task := h.loadTest.task
		status.Task = &task
		status.EndsAt = h.loadTest.deadline.Format("15:04:05")
		taskID = task.ID
	}
	for _, agent := range h.agents {
		if agent.LoadTaskID == "" || (taskID != "" && agent.LoadTaskID != taskID) {
			continue
		}
		if agent.LoadBytes == 0 {
			continue
		}
		status.Agents++
		status.TotalBytes += agent.LoadBytes
		if agent.Connected {
			status.TotalRateBps += agent.LoadRateBps
		}
	}
	return status
}
