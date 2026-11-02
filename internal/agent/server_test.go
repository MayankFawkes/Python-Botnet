package agent

import (
	"net"
	"testing"

	"atlas.local/simulator/internal/monitor"
)

func startHub(t *testing.T, hub *monitor.Hub) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go hub.ServeListener(listener)
	t.Cleanup(func() {
		_ = listener.Close()
		hub.Close()
	})
	return listener.Addr().String()
}
