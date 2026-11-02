package buildclient

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"atlas.local/simulator/internal/clientconfig"
	"atlas.local/simulator/internal/clientupdate"
)

// RepoRoot finds the module root from the working directory or executable path.
func RepoRoot() (string, error) {
	if wd, err := os.Getwd(); err == nil {
		if root, ok := findModuleRoot(wd); ok {
			return root, nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if root, ok := findModuleRoot(filepath.Dir(exe)); ok {
		return root, nil
	}
	return "", fmt.Errorf("could not locate the botnet module (go.mod)")
}

func findModuleRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Agent writes a monitoring agent binary with console addresses embedded.
func Agent(root, output string, servers []string, goos, goarch string) error {
	return agentVersion(root, output, servers, goos, goarch, "v1")
}

// AgentWithUpdateKey writes a development agent that also pins the console's
// update-signing public key, making it eligible for signed updates like a
// console-built release. keyPath is the console's update-signing.key.
func AgentWithUpdateKey(root, output, keyPath string, servers []string, goos, goarch string) error {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading update signing key: %w", err)
	}
	if len(keyData) != ed25519.PrivateKeySize {
		return fmt.Errorf("%s is not an update signing key", keyPath)
	}
	pub := base64.StdEncoding.EncodeToString(ed25519.PrivateKey(keyData).Public().(ed25519.PublicKey))
	return agentWithKey(root, output, servers, goos, goarch, "v1", pub)
}

func agentVersion(root, output string, servers []string, goos, goarch, version string) error {
	return agentWithKey(root, output, servers, goos, goarch, version, "")
}

func agentWithKey(root, output string, servers []string, goos, goarch, version, key string) error {
	return agentConfigured(root, output, servers, goos, goarch, version, key, clientconfig.Default())
}

func agentConfigured(root, output string, servers []string, goos, goarch, version, key string, config clientconfig.Config) error {
	baked, err := config.Encode()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return fmt.Errorf("at least one console address is required")
	}
	joined := strings.Join(servers, ",")
	ldflags := "-X atlas.local/simulator/internal/agent.BakedServers=" + joined
	ldflags += " -X atlas.local/simulator/internal/version.Client=" + version
	ldflags += " -X atlas.local/simulator/internal/clientconfig.Baked=" + baked
	if key != "" {
		ldflags += " -X atlas.local/simulator/internal/clientupdate.PublicKey=" + key
	}
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", output, "./cmd/agent")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		cmd.Env = append(cmd.Env, "GOOS="+goos)
	}
	if goarch != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+goarch)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Targets includes every Linux architecture supported by the installed Go
// toolchain, and the two requested Windows architectures.
func Targets() ([]string, error) {
	out, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, target := range strings.Fields(string(out)) {
		if strings.HasPrefix(target, "linux/") || target == "windows/386" || target == "windows/amd64" {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("Go toolchain returned no supported targets")
	}
	return targets, nil
}

// Bundle stages a complete release before publishing versioned binaries in dir.
// It refuses to overwrite any previously built artifact.
func Bundle(root, dir string, servers []string, version string) error {
	return BundleConfigured(root, dir, servers, version, clientconfig.Default())
}

func BundleConfigured(root, dir string, servers []string, version string, config clientconfig.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	targets, err := Targets()
	if err != nil {
		return err
	}
	key, err := clientupdate.SigningKey(dir)
	if err != nil {
		return err
	}
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	build := func(root, output string, servers []string, goos, arch, release string) error {
		if err := agentConfigured(root, output, servers, goos, arch, release, pub, config); err != nil {
			return err
		}
		return clientupdate.Sign(output, release, goos, arch, key)
	}
	return bundle(root, dir, servers, version, targets, build)
}

func bundle(root, dir string, servers []string, version string, targets []string, build func(string, string, []string, string, string, string) error) error {
	if len(servers) == 0 {
		return fmt.Errorf("at least one console address is required")
	}
	stage, err := os.MkdirTemp(dir, ".botnet-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // Only this build's private staging directory.
	var names []string
	for _, target := range targets {
		parts := strings.SplitN(target, "/", 2)
		name := "botnet-agent-" + version + "-" + parts[0] + "-" + parts[1]
		if parts[0] == "windows" {
			name += ".exe"
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil || !os.IsNotExist(err) {
			return fmt.Errorf("output already exists or cannot be checked: %s", name)
		}
		if err := build(root, filepath.Join(stage, name), servers, parts[0], parts[1], version); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		names = append(names, name)
		if _, err := os.Stat(filepath.Join(stage, name+".json")); err == nil {
			names = append(names, name+".json")
		}
	}
	for _, name := range names {
		// Hard-link publication is atomic and, unlike Rename, never replaces an
		// existing file. Staging is on the same filesystem as the destination.
		if err := os.Link(filepath.Join(stage, name), filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("publishing %s: %w", name, err)
		}
	}
	return nil
}

// Prune removes only older numbered agent artifacts, never console binaries,
// signing keys, state files, unversioned binaries or newer releases.
func Prune(dir, current string) error {
	n, err := clientupdate.Number(current)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^botnet-agent-v([1-9][0-9]*)-(linux|windows)-[a-z0-9]+(\.exe)?(\.json)?$`)
	for _, entry := range entries {
		match := pattern.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			continue
		}
		old, err := strconv.Atoi(match[1])
		if err != nil || old >= n {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
