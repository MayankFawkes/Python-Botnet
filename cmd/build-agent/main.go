// Command build-agent compiles botnet-agent with console addresses embedded.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"atlas.local/simulator/internal/buildclient"
)

func main() {
	serversFlag := flag.String("servers", "", "comma-separated console host:port values (required)")
	out := flag.String("out", "botnet-agent", "output binary path")
	goos := flag.String("goos", "", "target GOOS (optional, for example windows)")
	goarch := flag.String("goarch", "", "target GOARCH (optional, for example amd64)")
	updateKey := flag.String("update-key", "", "console update-signing.key to pin, making this build update-capable (optional)")
	flag.Parse()
	servers := splitServers(*serversFlag)
	if len(servers) == 0 {
		fmt.Fprintln(os.Stderr, "usage: build-agent -servers 10.0.0.5:34116,10.0.0.6:34116 [-out ./botnet-agent]")
		os.Exit(2)
	}
	root, err := buildclient.RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	output := *out
	if !filepath.IsAbs(output) {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		output = filepath.Join(wd, output)
	}
	if *goos == "windows" && !strings.HasSuffix(output, ".exe") {
		output += ".exe"
	}
	build := buildclient.Agent
	if *updateKey != "" {
		build = func(root, output string, servers []string, goos, goarch string) error {
			return buildclient.AgentWithUpdateKey(root, output, *updateKey, servers, goos, goarch)
		}
	}
	if err := build(root, output, servers, *goos, *goarch); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(output)
}

func splitServers(raw string) []string {
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
