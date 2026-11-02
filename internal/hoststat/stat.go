// Package hoststat reads local CPU and memory counters for the monitoring agent.
// It does not inspect files outside the operating system's process statistics.
package hoststat

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Sample is one reading from the local machine.
type Sample struct {
	CPUPercent float64
	MemTotal   uint64
	MemUsed    uint64
}

// Sampler keeps the previous CPU counter so a later Read can report a percentage.
type Sampler struct {
	prevIdle  uint64
	prevTotal uint64
	ready     bool
}

// Read returns the current sample. The first call reports CPU as zero because a
// percentage needs two counter readings.
func (s *Sampler) Read() (Sample, error) {
	if runtime.GOOS != "linux" {
		return Sample{}, fmt.Errorf("host metrics are implemented for Linux")
	}
	memTotal, memUsed, err := readMeminfo()
	if err != nil {
		return Sample{}, err
	}
	idle, total, err := readCPU()
	if err != nil {
		return Sample{}, err
	}
	sample := Sample{MemTotal: memTotal, MemUsed: memUsed}
	if s.ready {
		sample.CPUPercent = cpuPercent(s.prevIdle, s.prevTotal, idle, total)
	}
	s.prevIdle, s.prevTotal, s.ready = idle, total, true
	return sample, nil
}

func readMeminfo() (total, used uint64, err error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	return parseMeminfo(file)
}

func readCPU() (idle, total uint64, err error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, fmt.Errorf("empty /proc/stat")
	}
	return parseCPU(scanner.Text())
}

func parseMeminfo(r interface{ Read([]byte) (int, error) }) (total, used uint64, err error) {
	scanner := bufio.NewScanner(r)
	var available uint64
	var haveTotal, haveAvailable bool
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, convErr := strconv.ParseUint(fields[1], 10, 64)
		if convErr != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
			haveTotal = true
		case "MemAvailable:":
			available = value * 1024
			haveAvailable = true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if !haveTotal || !haveAvailable || available > total {
		return 0, 0, fmt.Errorf("meminfo is missing MemTotal or MemAvailable")
	}
	return total, total - available, nil
}

func parseCPU(line string) (idle, total uint64, err error) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected cpu line")
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, convErr := strconv.ParseUint(field, 10, 64)
		if convErr != nil {
			return 0, 0, convErr
		}
		values = append(values, value)
		total += value
	}
	// user nice system idle iowait...
	idle = values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("cpu counters are zero")
	}
	return idle, total, nil
}

func cpuPercent(prevIdle, prevTotal, idle, total uint64) float64 {
	totalDelta := total - prevTotal
	idleDelta := idle - prevIdle
	if total <= prevTotal || idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}
