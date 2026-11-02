// Package clientupdate implements signed, platform-specific monitoring-agent updates.
package clientupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MaxSize = 64 << 20

// PublicKey is pinned into client builds. Empty means remote updates are disabled.
var PublicKey string

type Manifest struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func Number(version string) (int, error) {
	if !strings.HasPrefix(version, "v") {
		return 0, errors.New("release must start with v")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(version, "v"))
	if err != nil || n < 1 || fmt.Sprintf("v%d", n) != version {
		return 0, errors.New("invalid release number")
	}
	return n, nil
}

func (m Manifest) Name() (string, error) {
	if _, err := Number(m.Version); err != nil {
		return "", err
	}
	if m.OS != "linux" && m.OS != "windows" {
		return "", errors.New("unsupported update OS")
	}
	if m.Arch == "" || strings.Trim(m.Arch, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
		return "", errors.New("invalid update architecture")
	}
	name := "botnet-agent-" + m.Version + "-" + m.OS + "-" + m.Arch
	if m.OS == "windows" {
		name += ".exe"
	}
	return name, nil
}

func (m Manifest) payload() []byte {
	m.Signature = ""
	b, _ := json.Marshal(m)
	return b
}

func (m Manifest) Verify(key, goos, arch, current string) error {
	if err := m.VerifySignature(key, goos, arch); err != nil {
		return err
	}
	next, _ := Number(m.Version)
	old, err := Number(current)
	if err != nil || next <= old {
		return errors.New("update must be a newer numbered release")
	}
	return nil
}

func (m Manifest) VerifySignature(key, goos, arch string) error {
	if _, err := m.Name(); err != nil {
		return err
	}
	if m.OS != goos || m.Arch != arch {
		return errors.New("update platform does not match client")
	}
	if m.Size < 1 || m.Size > MaxSize {
		return errors.New("update size outside permitted range")
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("invalid update checksum")
	}
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("no valid pinned update key")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || !ed25519.Verify(pub, m.payload(), sig) {
		return errors.New("update signature verification failed")
	}
	return nil
}

func SigningKey(dir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dir, "update-signing.key")
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != ed25519.PrivateKeySize {
			return nil, errors.New("invalid update signing key; refusing to replace it")
		}
		return ed25519.PrivateKey(data), nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.Write(key)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	return key, closeErr
}

func Sign(path, release, goos, arch string, key ed25519.PrivateKey) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, MaxSize+1))
	if err != nil {
		return err
	}
	if size < 1 || size > MaxSize {
		return errors.New("binary too large or empty")
	}
	m := Manifest{Version: release, OS: goos, Arch: arch, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, m.payload()))
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".json", data, 0o600)
}

func ReadManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	var m Manifest
	err = json.NewDecoder(io.LimitReader(f, 8192)).Decode(&m)
	return m, err
}

func CheckFile(path string, m Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, MaxSize+1))
	if err != nil {
		return err
	}
	if size != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return errors.New("downloaded update checksum or size mismatch")
	}
	return nil
}
