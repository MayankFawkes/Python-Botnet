//go:build desktop || bindings

package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"atlas.local/simulator/internal/buildclient"
	"atlas.local/simulator/internal/clientconfig"
	"atlas.local/simulator/internal/localdata"
	"atlas.local/simulator/internal/monitor"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/index.html frontend/app.js frontend/styles.css frontend/world.json frontend/vendor
var assets embed.FS

// App is the desktop console. Agents connect to its listener; the webview
// only talks to these methods.
type App struct {
	hub *monitor.Hub
	ctx context.Context
	mu  sync.Mutex
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

// emit pushes a changed snapshot to the webview. Wails marshals it to JSON; an
// event is far cheaper than the interface re-rendering on a timer.
func (a *App) emit(snapshot monitor.Snapshot) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, "botnet:snapshot", snapshot)
}

func (a *App) emitShell(ev monitor.ShellEvent) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, "botnet:shell", ev)
}

func (a *App) Snapshot() monitor.Snapshot                 { return a.hub.Snapshot() }
func (a *App) Refresh() monitor.Snapshot                  { return a.hub.Snapshot() }
func (a *App) Revoke(id string) (monitor.Snapshot, error) { return a.hub.Revoke(id) }

func (a *App) UpdateClient(id string) (monitor.Snapshot, error) { return a.hub.UpdateClient(id) }

func (a *App) UpdateAllClients() (monitor.BulkUpdateResult, error) { return a.hub.UpdateAllClients() }
func (a *App) SaveSettings(settings monitor.Settings) (monitor.Snapshot, error) {
	return a.hub.SaveSettings(settings)
}

func (a *App) ClearOfflineClients() (monitor.CleanupResult, error) {
	return a.hub.ClearOfflineClients()
}

// StartLoad begins a bounded load test against an allowlisted target.
func (a *App) StartLoad(target string, port int, method string, percent, duration int) (monitor.Snapshot, error) {
	return a.hub.StartLoad(target, port, method, percent, duration)
}

// StopLoad ends the active load test.
func (a *App) StopLoad() (monitor.Snapshot, error) { return a.hub.StopLoad() }

func (a *App) ShellOpen(id string, cols, rows int) error {
	return a.hub.ShellOpen(id, cols, rows)
}
func (a *App) ShellWrite(id, data string) error { return a.hub.ShellWrite(id, data) }
func (a *App) ShellResize(id string, cols, rows int) error {
	return a.hub.ShellResize(id, cols, rows)
}
func (a *App) ShellClose(id string) error                { return a.hub.ShellClose(id) }
func (a *App) ShellDrain(id string) []monitor.ShellEvent { return a.hub.ShellDrain(id) }

func (a *App) BuildClientConfigured(extraServers string, config clientconfig.Config) (string, error) {
	snap := a.hub.Snapshot()
	servers, err := monitor.ServerAddresses(snap.AdvertiseURL, extraServers)
	if err != nil {
		return "", err
	}
	root, err := buildclient.RepoRoot()
	if err != nil {
		return "", err
	}
	dir, err := localdata.Dir()
	if err != nil {
		return "", err
	}
	return a.hub.BuildClientsConfigured(root, dir, servers, config)
}

// AddSite declares where a network's agents belong on the atlas.
func (a *App) AddSite(name, cidr string, lat, lon float64) (monitor.Snapshot, error) {
	return a.hub.AddSite(name, cidr, lat, lon)
}

// RemoveSite drops a declared location.
func (a *App) RemoveSite(cidr string) (monitor.Snapshot, error) {
	return a.hub.RemoveSite(cidr)
}

// BuildClient creates a numbered release for all supported targets beside the
// console executable. goos is retained for compatibility with existing bindings.
func (a *App) BuildClient(extraServers, goos string) (string, error) {
	snap := a.hub.Snapshot()
	servers, err := monitor.ServerAddresses(snap.AdvertiseURL, extraServers)
	if err != nil {
		return "", err
	}
	root, err := buildclient.RepoRoot()
	if err != nil {
		return "", err
	}
	dir, err := localdata.Dir()
	if err != nil {
		return "", err
	}
	return a.hub.BuildClients(root, dir, servers)
}

func (a *App) ExportLog() (bool, error) {
	data, err := json.MarshalIndent(struct {
		Mode   string          `json:"mode"`
		Events []monitor.Event `json:"events"`
	}{Mode: "monitor", Events: a.hub.Snapshot().Events}, "", "  ")
	if err != nil {
		return false, err
	}
	destination, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "Export console event log", DefaultFilename: "botnet-events.json",
	})
	if err != nil || destination == "" {
		return false, err
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func main() {
	addr, advertise := listenConfig()
	dir, err := localdata.Dir()
	if err != nil {
		log.Fatal(err)
	}
	app := &App{}
	hub := monitor.New(monitor.Config{
		ListenAddr: addr, AdvertiseURL: advertise, StatePath: statePath(),
		ArtifactDir:      dir,
		UseSavedSettings: true,
		OverrideListen:   providedFlag("listen"), OverrideAdvertise: providedFlag("advertise") || providedFlag("listen"),
		OnChange: app.emit,
		OnShell:  app.emitShell,
	})
	app.hub = hub
	defer hub.Close()
	go hub.Serve(hub.Settings().ListenAddr)

	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title: "BOTNET // CONSOLE", Width: 1440, Height: 980,
		MinWidth: 900, MinHeight: 650,
		BackgroundColour: &options.RGBA{R: 1, G: 4, B: 3, A: 255},
		AssetServer:      &assetserver.Options{Assets: frontend},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func listenConfig() (addr, advertise string) {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "0.0.0.0:34116", "address for agent enrollment and heartbeats")
	advertised := fs.String("advertise", "", "TCP address agents dial, for example 10.0.0.5:34116")
	_ = fs.Parse(os.Args[1:])
	addr = *listen
	if *advertised != "" {
		return addr, *advertised
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if lan := lanIPv4(); lan != "" {
			host = lan
		} else {
			host = "127.0.0.1"
		}
	}
	return addr, net.JoinHostPort(host, port)
}

func lanIPv4() string {
	conn, err := net.Dial("udp", "192.0.2.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return ""
	}
	return host
}

func statePath() string {
	dir, err := localdata.Dir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(dir, "console.json")
}

func providedFlag(name string) bool {
	for _, arg := range os.Args[1:] {
		arg = strings.TrimPrefix(arg, "-")
		arg = strings.TrimPrefix(arg, "-")
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}
