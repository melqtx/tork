// Package state persists the list of added torrents so they resume on restart.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type Entry struct {
	Magnet   string    `json:"magnet"`
	Name     string    `json:"name"`
	AddedAt  time.Time `json:"added_at"`
	Paused   bool      `json:"paused"`
	Done     bool      `json:"done"`
	Excluded []int     `json:"excluded,omitempty"`
}

type State struct {
	Entries []Entry `json:"entries"`
}

// Load reads the state file. A missing, unreadable, or corrupt file yields an
// empty state rather than an error - losing the resume list is recoverable,
// failing to start is not. A corrupt file is preserved as .bak.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "tork: could not read %s (%v); starting with an empty download list\n", path, err)
		}
		return &State{}, nil
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		_ = os.Rename(path, path+".bak")
		fmt.Fprintf(os.Stderr, "tork: %s was corrupt (%v); backed up to %s.bak\n", path, err, path)
		return &State{}, nil
	}
	return &s, nil
}

// Save writes atomically via temp file + rename.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Upsert adds the entry or replaces an existing one with the same magnet.
func (s *State) Upsert(e Entry) {
	for i := range s.Entries {
		if s.Entries[i].Magnet == e.Magnet {
			s.Entries[i] = e
			return
		}
	}
	s.Entries = append(s.Entries, e)
}

func (s *State) Remove(magnet string) {
	s.Entries = slices.DeleteFunc(s.Entries, func(e Entry) bool { return e.Magnet == magnet })
}

func (s *State) Find(magnet string) *Entry {
	for i := range s.Entries {
		if s.Entries[i].Magnet == magnet {
			return &s.Entries[i]
		}
	}
	return nil
}
