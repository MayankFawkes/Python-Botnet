// Package localdata locates portable application state beside the executable.
package localdata

import (
	"os"
	"path/filepath"
)

func Dir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
