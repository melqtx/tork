package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func saveStateBytes(path string, data []byte, pending bool) error {
	previous, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read previous state: %w", err)
	}
	if os.IsNotExist(err) || pending {
		previous = data
	}
	var valid *State
	if json.Unmarshal(previous, &valid) == nil && valid != nil {
		if err := writeStateFile(path+".lastgood", previous); err != nil {
			return fmt.Errorf("back up state: %w", err)
		}
	}
	return writeStateFile(path, data)
}

func writeStateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
