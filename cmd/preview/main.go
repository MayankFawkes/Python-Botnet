// Command preview serves the console on loopback for interface development.
// The desktop application does not include this process.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"atlas.local/simulator/internal/buildclient"
	"atlas.local/simulator/internal/clientconfig"
	"atlas.local/simulator/internal/monitor"
)

func main() {
	if _, err := os.Stat("frontend/index.html"); err != nil {
		log.Fatal("run from the repository root")
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	hub := monitor.New(monitor.Config{
		ListenAddr:   "127.0.0.1:34117",
		AdvertiseURL: "127.0.0.1:34117",
		StatePath:    "preview-console.json",
		ArtifactDir:  dir,
	})
	go hub.Serve("127.0.0.1:34117")
	defer hub.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != "127.0.0.1:34115" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://127.0.0.1:34115") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var args struct {
			Settings     monitor.Settings    `json:"settings"`
			Config       clientconfig.Config `json:"config"`
			ID           string              `json:"id"`
			ExtraServers string              `json:"extraServers"`
			GOOS         string              `json:"goos"`
			Name         string              `json:"name"`
			CIDR         string              `json:"cidr"`
			Lat          float64             `json:"lat"`
			Lon          float64             `json:"lon"`
			Target       string              `json:"target"`
			Port         int                 `json:"port"`
			Method       string              `json:"method"`
			Percent      int                 `json:"percent"`
			Duration     int                 `json:"duration"`
			Data         string              `json:"data"`
			Cols         int                 `json:"cols"`
			Rows         int                 `json:"rows"`
		}
		limit := int64(2048)
		if r.URL.Path == "/api/SaveSettings" {
			limit = 32768
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&args); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var result any
		var err error
		switch r.URL.Path {
		case "/api/Reset":
			hub.Reset()
			result = hub.Snapshot()
		case "/api/Snapshot", "/api/Refresh":
			result = hub.Snapshot()
		case "/api/BuildClient", "/api/BuildClientConfigured":
			servers, serr := monitor.ServerAddresses(hub.Snapshot().AdvertiseURL, args.ExtraServers)
			if serr != nil {
				err = serr
				break
			}
			root, serr := buildclient.RepoRoot()
			if serr != nil {
				err = serr
				break
			}
			config := args.Config
			if r.URL.Path == "/api/BuildClient" {
				config = hub.Settings().ClientDefaults
			}
			output, buildErr := hub.BuildClientsConfigured(root, root, servers, config)
			err = buildErr
			if err == nil {
				result = map[string]string{"path": output}
			}
		case "/api/Revoke":
			result, err = hub.Revoke(args.ID)
		case "/api/UpdateClient":
			result, err = hub.UpdateClient(args.ID)
		case "/api/UpdateAllClients":
			result, err = hub.UpdateAllClients()
		case "/api/SaveSettings":
			result, err = hub.SaveSettings(args.Settings)
		case "/api/ClearOfflineClients":
			result, err = hub.ClearOfflineClients()
		case "/api/StartLoad":
			result, err = hub.StartLoad(args.Target, args.Port, args.Method, args.Percent, args.Duration)
		case "/api/StopLoad":
			result, err = hub.StopLoad()
		case "/api/AddSite":
			result, err = hub.AddSite(args.Name, args.CIDR, args.Lat, args.Lon)
		case "/api/RemoveSite":
			result, err = hub.RemoveSite(args.CIDR)
		case "/api/ShellOpen":
			err = hub.ShellOpen(args.ID, args.Cols, args.Rows)
		case "/api/ShellWrite":
			err = hub.ShellWrite(args.ID, args.Data)
		case "/api/ShellResize":
			err = hub.ShellResize(args.ID, args.Cols, args.Rows)
		case "/api/ShellClose":
			err = hub.ShellClose(args.ID)
		case "/api/ShellDrain":
			result = hub.ShellDrain(args.ID)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/wails/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = fmt.Fprint(w, "/* Browser development preview. */")
	})
	mux.Handle("/", http.FileServer(http.Dir("frontend")))
	server := &http.Server{Addr: "127.0.0.1:34115", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Print("Development preview: http://127.0.0.1:34115 (loopback only)")
	log.Fatal(server.ListenAndServe())
}
