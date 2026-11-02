package clientupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"atlas.local/simulator/internal/version"
)

// Ready is returned only after a complete, signed platform-matching download.
type Ready struct {
	Path     string
	Manifest Manifest
}

func (r *Ready) Error() string { return "verified client update ready: " + r.Manifest.Version }

// Start launches the verified replacement as a short-lived installer helper.
// The caller must then exit so Windows can release its executable file lock.
func (r *Ready) Start() error {
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target + ".previous"); !os.IsNotExist(err) {
		return errors.New("previous update backup exists; resolve it before retrying")
	}
	if err := r.Manifest.Verify(PublicKey, runtime.GOOS, runtime.GOARCH, version.Client); err != nil {
		return err
	}
	if err := CheckFile(r.Path, r.Manifest); err != nil {
		return err
	}
	data, err := json.Marshal(r.Manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.Path+".json", data, 0o600); err != nil {
		return err
	}
	cmd := exec.Command(r.Path, "__botnet_apply_update", target, version.Client)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ApplyHelper is an internal mode, not a command channel. It can install only
// its own signed executable, beside itself, over an existing regular file.
func ApplyHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "__botnet_apply_update" {
		return false, nil
	}
	if len(args) != 3 {
		return true, errors.New("invalid update helper arguments")
	}
	self, err := os.Executable()
	if err != nil {
		return true, err
	}
	target := filepath.Clean(args[1])
	if !filepath.IsAbs(target) || filepath.Dir(target) != filepath.Dir(self) || target == self || !strings.HasPrefix(filepath.Base(self), ".botnet-update-") {
		return true, errors.New("update target must be an existing client in the same directory")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return true, errors.New("update target is not a regular file")
	}
	m, err := ReadManifest(self + ".json")
	if err != nil {
		return true, err
	}
	if m.Version != version.Client {
		return true, errors.New("helper build version differs from signed release")
	}
	if err := m.Verify(PublicKey, runtime.GOOS, runtime.GOARCH, args[2]); err != nil {
		return true, err
	}
	if err := CheckFile(self, m); err != nil {
		return true, err
	}
	backup := target + ".previous"
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		return true, errors.New("previous update backup exists; resolve it before retrying")
	}
	// Wait for the original process to release its binary (Windows). Linux also
	// gets a brief grace period to avoid overlapping foreground processes.
	time.Sleep(250 * time.Millisecond)
	deadline := time.Now().Add(15 * time.Second)
	for {
		err = os.Rename(target, backup)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = launch(target, []string{"BOTNET_UPDATE_ERROR=Client executable could not be replaced", "BOTNET_UPDATE_STAGE=" + self})
			return true, fmt.Errorf("client replacement failed: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	restore := func(cause error) (bool, error) {
		_ = os.Remove(target)
		if err := os.Rename(backup, target); err != nil {
			return true, fmt.Errorf("%v; rollback failed: %w", cause, err)
		}
		if err := launch(target, []string{"BOTNET_UPDATE_ERROR=Client update rolled back after installation failure", "BOTNET_UPDATE_STAGE=" + self}); err != nil {
			return true, fmt.Errorf("%v; restart after rollback failed: %w", cause, err)
		}
		return true, cause
	}
	source, err := os.Open(self)
	if err != nil {
		return restore(err)
	}
	dest, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		source.Close()
		return restore(err)
	}
	_, copyErr := io.Copy(dest, source)
	source.Close()
	if copyErr == nil {
		copyErr = dest.Sync()
	}
	closeErr := dest.Close()
	if copyErr != nil {
		return restore(copyErr)
	}
	if closeErr != nil {
		return restore(closeErr)
	}
	if err := CheckFile(target, m); err != nil {
		return restore(err)
	}
	if err := launch(target, []string{"BOTNET_UPDATE_STAGE=" + self}); err != nil {
		return restore(err)
	}
	return true, nil
}

func launch(path string, extra []string) error {
	cmd := exec.Command(path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "BOTNET_UPDATE_STAGE=") && !strings.HasPrefix(value, "BOTNET_UPDATE_ERROR=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, extra...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Confirm removes only the updater's own files after a successful heartbeat.
// Failures (e.g. the Windows helper has not exited yet) are retried on later beats.
func Confirm() {
	stage := os.Getenv("BOTNET_UPDATE_STAGE")
	if stage == "" {
		return
	}
	self, err := os.Executable()
	if err != nil || filepath.Dir(stage) != filepath.Dir(self) || !strings.HasPrefix(filepath.Base(stage), ".botnet-update-") || stage == self {
		return
	}
	for _, path := range []string{self + ".previous", stage + ".json", stage} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return
		}
	}
	_ = os.Unsetenv("BOTNET_UPDATE_STAGE")
}
