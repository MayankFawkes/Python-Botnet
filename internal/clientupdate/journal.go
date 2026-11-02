package clientupdate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type RequestReceipt struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Completed bool   `json:"completed"`
}

// Journal is local to the single foreground update session. It is not a
// general-purpose command queue. Version checks also reject older evicted IDs.
type Journal struct {
	path     string
	Requests []RequestReceipt `json:"requests"`
}

func OpenJournal(path, current string) (*Journal, error) {
	j := &Journal{path: path}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if len(data) > 131072 {
			return nil, errors.New("update request journal too large")
		}
		if err := json.Unmarshal(data, j); err != nil {
			return nil, errors.New("update request journal is unreadable; refusing to discard duplicate protection")
		}
		if len(j.Requests) > 256 {
			return nil, errors.New("too many update receipts")
		}
		seen := map[string]bool{}
		for _, r := range j.Requests {
			hash, hashErr := hex.DecodeString(r.SHA256)
			_, versionErr := Number(r.Version)
			if !ValidRequestID(r.ID) || seen[r.ID] || hashErr != nil || len(hash) != 32 || versionErr != nil {
				return nil, errors.New("invalid update receipt")
			}
			seen[r.ID] = true
		}
	}
	changed := false
	for i := range j.Requests {
		// A new executable reporting this exact release proves that this
		// prepared request has been applied; no helper-specific command exists.
		if j.Requests[i].Version == current && !j.Requests[i].Completed {
			j.Requests[i].Completed = true
			changed = true
		}
	}
	if changed {
		if err := j.save(); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func ValidRequestID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == id
}

// Prepare binds the ID to one signed version/checksum before any download.
// Completed IDs are acknowledged, not installed again. Failed downloads may
// retry the same prepared request; this does not claim distributed exactly-once.
func (j *Journal) Prepare(id string, m Manifest, current string) (bool, error) {
	if err := m.VerifySignature(PublicKey, m.OS, m.Arch); err != nil {
		return false, err
	}
	if !ValidRequestID(id) {
		return false, errors.New("invalid signed-update request ID")
	}
	for _, r := range j.Requests {
		if r.ID != id {
			continue
		}
		if r.Version != m.Version || r.SHA256 != m.SHA256 {
			return false, errors.New("update request ID reused for a different release")
		}
		if r.Completed {
			return true, nil
		}
		if err := m.Verify(PublicKey, m.OS, m.Arch, current); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := m.Verify(PublicKey, m.OS, m.Arch, current); err != nil {
		return false, err
	}
	old := append([]RequestReceipt(nil), j.Requests...)
	j.Requests = append(j.Requests, RequestReceipt{ID: id, Version: m.Version, SHA256: m.SHA256})
	if len(j.Requests) > 256 {
		j.Requests = j.Requests[len(j.Requests)-256:]
	}
	if err := j.save(); err != nil {
		j.Requests = old
		return false, err
	}
	return false, nil
}

func (j *Journal) CompletedID(current string) string {
	if j == nil {
		return ""
	}
	for i := len(j.Requests) - 1; i >= 0; i-- {
		if j.Requests[i].Completed && j.Requests[i].Version == current {
			return j.Requests[i].ID
		}
	}
	return ""
}

func (j *Journal) save() error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(j.path), ".update-requests-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), j.path)
}
