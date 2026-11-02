package monitor

import (
	"net"
	"strings"
)

// ServerAddresses merges the primary console address with optional extras.
func ServerAddresses(primary, extra string) ([]string, error) {
	var list []string
	if strings.TrimSpace(primary) != "" {
		list = append(list, primary)
	}
	list = append(list, splitList(extra)...)
	out := make([]string, 0, len(list))
	seen := map[string]struct{}{}
	for _, raw := range list {
		addr, err := normalizeServerAddr(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, errNoServers
	}
	return out, nil
}

var errNoServers = &addrError{msg: "at least one console host:port is required"}

type addrError struct{ msg string }

func (e *addrError) Error() string { return e.msg }

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' '
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeServerAddr(server string) (string, error) {
	server = strings.TrimSpace(server)
	server = strings.TrimPrefix(server, "http://")
	server = strings.TrimPrefix(server, "https://")
	server = strings.TrimSuffix(server, "/")
	host, port, err := net.SplitHostPort(server)
	if err != nil || host == "" || port == "" {
		return "", &addrError{msg: "each console must be host:port, for example 10.0.0.5:34116"}
	}
	return net.JoinHostPort(host, port), nil
}
