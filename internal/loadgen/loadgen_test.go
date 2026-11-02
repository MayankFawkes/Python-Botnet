package loadgen

import "testing"

func TestNormalize(t *testing.T) {
	task, err := Normalize(" 10.100.0.10 ", 443, MethodTCP, 20, 30)
	if err != nil {
		t.Fatal(err)
	}
	if task.Target != "10.100.0.10" || task.Address() != "10.100.0.10:443" {
		t.Fatalf("task %+v", task)
	}
	if got := task.RateBps(); got != CeilingBps/5 {
		t.Fatalf("rate %d", got)
	}
	if _, err := Normalize("https://example.com/x", 80, MethodTCP, 20, 30); err == nil {
		t.Fatal("accepted a URL as target")
	}
	if _, err := Normalize("example.com", 0, MethodTCP, 20, 30); err == nil {
		t.Fatal("accepted port 0")
	}
	if _, err := Normalize("example.com", 80, MethodTCP, 0, 30); err == nil {
		t.Fatal("accepted 0 percent")
	}
	if _, err := Normalize("example.com", 80, MethodTCP, 20, MaxDurationSeconds+1); err == nil {
		t.Fatal("accepted duration above the cap")
	}
	if _, err := Normalize("example.com", 80, "icmp", 20, 30); err == nil {
		t.Fatal("accepted an unknown method")
	}
}
