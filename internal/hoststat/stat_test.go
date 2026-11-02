package hoststat

import (
	"strings"
	"testing"
	"time"
)

func TestParseMeminfoAndCPU(t *testing.T) {
	total, used, err := parseMeminfo(strings.NewReader("MemTotal: 1000 kB\nMemAvailable: 250 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if total != 1000*1024 || used != 750*1024 {
		t.Fatalf("memory total=%d used=%d", total, used)
	}
	idle, all, err := parseCPU("cpu 100 0 100 700 100 0 0 0")
	if err != nil {
		t.Fatal(err)
	}
	if idle != 800 || all != 1000 {
		t.Fatalf("idle=%d total=%d", idle, all)
	}
	got := cpuPercent(800, 1000, 850, 1100)
	if got < 49 || got > 51 {
		t.Fatalf("cpu percent %v", got)
	}
}

func TestLiveSample(t *testing.T) {
	sampler := Sampler{}
	if _, err := sampler.Read(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	sample, err := sampler.Read()
	if err != nil {
		t.Fatal(err)
	}
	if sample.MemTotal == 0 || sample.MemUsed > sample.MemTotal {
		t.Fatalf("memory sample %+v", sample)
	}
	if sample.CPUPercent < 0 || sample.CPUPercent > 100 {
		t.Fatalf("cpu %v", sample.CPUPercent)
	}
}
