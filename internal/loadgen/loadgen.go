// Package loadgen defines the bounded load-test task shared by the console and
// its agents. Tasks only run against operator-allowlisted targets, carry a
// rate ceiling and a hard deadline, and agents stop on their own when the
// deadline passes, even if the console is gone.
package loadgen

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Methods supported by agents. "udp" and "tcp" are layer-4 streams; "http" is
// a layer-7 request loop.
const (
	MethodUDP  = "udp"
	MethodTCP  = "tcp"
	MethodHTTP = "http"
)

// MaxDurationSeconds bounds every run. MaxPercent is relative to CeilingBps,
// the per-agent send rate at 100 percent.
const (
	MaxDurationSeconds = 300
	MaxPercent         = 100
	CeilingBps         = 10 * 1000 * 1000 // 10 MB/s per agent at 100%
)

// Task is one load-test run dispatched to every online agent.
type Task struct {
	ID              string `json:"id"`
	Target          string `json:"target"`
	Port            int    `json:"port"`
	Method          string `json:"method"`
	Percent         int    `json:"percent"`
	DurationSeconds int    `json:"durationSeconds"`
	// DeadlineUnix is when agents must stop, set by the console so a run
	// cannot outlive operator intent.
	DeadlineUnix int64 `json:"deadlineUnix"`
}

var hostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// Normalize validates the operator-facing fields and canonicalizes the target.
// ID and DeadlineUnix are assigned by the console afterwards.
func Normalize(target string, port int, method string, percent, duration int) (Task, error) {
	host := strings.ToLower(strings.TrimSpace(target))
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else if len(host) < 1 || len(host) > 253 || !hostPattern.MatchString(host) || strings.Contains(host, "..") {
		return Task{}, errors.New("target must be a hostname or IP, without URL, path or port")
	}
	if port < 1 || port > 65535 {
		return Task{}, errors.New("port must be 1–65535")
	}
	switch method {
	case MethodUDP, MethodTCP, MethodHTTP:
	default:
		return Task{}, fmt.Errorf("method must be one of %s, %s, %s", MethodUDP, MethodTCP, MethodHTTP)
	}
	if percent < 1 || percent > MaxPercent {
		return Task{}, fmt.Errorf("load must be 1–%d percent", MaxPercent)
	}
	if duration < 1 || duration > MaxDurationSeconds {
		return Task{}, fmt.Errorf("duration must be 1–%d seconds", MaxDurationSeconds)
	}
	return Task{Target: host, Port: port, Method: method, Percent: percent, DurationSeconds: duration}, nil
}

// RateBps is this task's per-agent send rate in bytes per second.
func (t Task) RateBps() int64 {
	return int64(t.Percent) * CeilingBps / 100
}

// Address is the dial target for this task.
func (t Task) Address() string {
	return net.JoinHostPort(t.Target, fmt.Sprintf("%d", t.Port))
}
