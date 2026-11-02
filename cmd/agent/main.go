// Command agent reports this machine's hostname, OS, CPU, and memory to an
// Botnet console. Console addresses are embedded when the binary is built.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"atlas.local/simulator/internal/agent"
	"atlas.local/simulator/internal/clientupdate"
	"atlas.local/simulator/internal/localdata"
	"atlas.local/simulator/internal/version"
)

func main() {
	if handled, err := clientupdate.ApplyHelper(os.Args[1:]); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if handled, err := inspect(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	fmt.Println("Botnet monitoring agent")
	fmt.Println("Console addresses are embedded in this binary.")
	fmt.Println("Approximate public-IP location is resolved using ipwho.is and reported to the console.")
	fmt.Println("Monitoring agent. Ctrl+C stops this process.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	opt := agent.Options{StatePath: defaultStatePath(), UpdateError: os.Getenv("BOTNET_UPDATE_ERROR")}
	_ = os.Unsetenv("BOTNET_UPDATE_ERROR")
	for {
		err := agent.Run(ctx, opt)
		var ready *clientupdate.Ready
		if errors.As(err, &ready) {
			log.Printf("Installing signed client update %s", ready.Manifest.Version)
			if err := ready.Start(); err != nil {
				log.Printf("Update rejected; keeping existing client: %v", err)
				opt.UpdateError = err.Error()
				_ = os.Remove(ready.Path)
				_ = os.Remove(ready.Path + ".json")
				continue
			}
			return
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
}

// inspect handles informational commands before any state files or connections
// are created. Neither command can change the embedded configuration.
func inspect(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if len(args) != 1 {
		return true, fmt.Errorf("usage: botnet-agent [version|host]")
	}
	switch args[0] {
	case "version", "-version", "--version":
		_, err := fmt.Fprintln(out, version.Client)
		return true, err
	case "host", "-host", "--host":
		hosts, err := agent.Hosts()
		if err != nil {
			return true, err
		}
		for _, host := range hosts {
			if _, err := fmt.Fprintln(out, host); err != nil {
				return true, err
			}
		}
		return true, nil
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(out, "usage: botnet-agent [version|host]\nNo arguments: run monitoring client.\nversion: print client version (read-only).\nhost: list embedded console hosts (read-only).")
		return true, err
	default:
		return true, fmt.Errorf("unknown argument %q; usage: botnet-agent [version|host]", args[0])
	}
}

func defaultStatePath() string {
	dir, err := localdata.Dir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(dir, "agent.json")
}
