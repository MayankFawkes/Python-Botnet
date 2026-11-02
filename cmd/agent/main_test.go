package main

import (
	"bytes"
	"strings"
	"testing"

	"atlas.local/simulator/internal/agent"
	"atlas.local/simulator/internal/version"
)

func TestInspect(t *testing.T) {
	previous := agent.BakedServers
	agent.BakedServers = "10.0.0.1:34116,10.0.0.2:34116"
	t.Cleanup(func() { agent.BakedServers = previous })
	for _, command := range []string{"version", "--version", "-version", "host", "--host", "-host"} {
		t.Run(command, func(t *testing.T) {
			var out bytes.Buffer
			handled, err := inspect([]string{command}, &out)
			if !handled || err != nil {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			want := version.Client + "\n"
			if strings.Contains(command, "host") {
				want = "10.0.0.1:34116\n10.0.0.2:34116\n"
			}
			if out.String() != want {
				t.Fatalf("got %q want %q", out.String(), want)
			}
		})
	}
	if handled, err := inspect(nil, &bytes.Buffer{}); handled || err != nil {
		t.Fatal("no arguments should run the client")
	}
	for _, args := range [][]string{{"host", "evil:123"}, {"version", "9.0.0"}, {"--host=evil:123"}} {
		if handled, err := inspect(args, &bytes.Buffer{}); !handled || err == nil {
			t.Fatalf("accepted override %v", args)
		}
	}
}
