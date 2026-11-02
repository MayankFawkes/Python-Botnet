package clientconfig

import "testing"

func TestDefaultsAndEmbeddedConfig(t *testing.T) {
	if Default().RetrySeconds != 10 {
		t.Fatal("retry default must be 10 seconds")
	}
	old := Baked
	t.Cleanup(func() { Baked = old })
	want := Config{12, 90, 20, 8}
	var err error
	Baked, err = want.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil || got != want {
		t.Fatalf("got %+v %v", got, err)
	}
	for _, bad := range []Config{{0, 60, 15, 5}, {10, 9, 15, 5}, {10, 60, 31, 5}, {10, 60, 15, 0}} {
		if bad.Validate() == nil {
			t.Fatalf("invalid config accepted: %+v", bad)
		}
	}
}
