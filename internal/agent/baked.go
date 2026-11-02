package agent

import "strings"

// BakedServers is set at link time (comma-separated host:port values).
//
// The default is what a plain `go build ./cmd/agent` produces. It points at the
// console on the local machine so the binary is usable without a custom build;
// pass -ldflags to bake real console addresses instead.
var BakedServers = "127.0.0.1:34116"

// Hosts returns a copy of the console targets embedded in this binary.
func Hosts() ([]string, error) { return serversFromBaked() }

func serversFromBaked() ([]string, error) {
	raw := strings.TrimSpace(BakedServers)
	if raw == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' '
	})
	return normalizeServers(parts)
}
