// Package state persists the list of added torrents so they resume on restart.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"
)

type Entry struct {
	// Magnet is the resume key: a magnet URI for torrents, or the https URL
	// for ISO-shelf images downloaded directly (distros without torrents).
	Magnet            string      `json:"magnet"`
	Name              string      `json:"name"`
	CategorySource    string      `json:"category_source,omitempty"`
	Category          string      `json:"category,omitempty"`
	SHA256            string      `json:"sha256,omitempty"` // expected digest for direct downloads
	ChecksumAlgorithm string      `json:"checksum_algorithm,omitempty"`
	Checksum          string      `json:"checksum,omitempty"`
	ExpectedSize      int64       `json:"expected_size,omitempty"`
	LockToOrigin      bool        `json:"lock_to_origin,omitempty"`
	AddedAt           time.Time   `json:"added_at"`
	Paused            bool        `json:"paused"`
	Done              bool        `json:"done"`
	DownloadDir       string      `json:"download_dir,omitempty"`
	DataPath          string      `json:"data_path,omitempty"`
	PendingMove       *MoveTarget `json:"pending_move,omitempty"`
	NeedsRelink       bool        `json:"needs_relink,omitempty"`
	Seed              *bool       `json:"seed,omitempty"`
	BytesCompleted    int64       `json:"bytes_completed,omitempty"`
	Length            int64       `json:"length,omitempty"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
	Excluded          []int       `json:"excluded,omitempty"`
}

type State struct {
	Entries     []Entry         `json:"entries"`
	Preferences json.RawMessage `json:"preferences,omitempty"`
}

// Load restores a last-good snapshot when the primary state is missing or damaged.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s *State
	if !missing {
		s, err = decodeState(data)
	}
	if missing || err != nil {
		backup, backupErr := os.ReadFile(path + ".lastgood")
		if missing && os.IsNotExist(backupErr) {
			return &State{}, nil
		}
		if !missing {
			if preserveErr := writeStateFile(path+".bak", data); preserveErr != nil {
				return nil, fmt.Errorf("preserve damaged state: %w", preserveErr)
			}
		}
		if backupErr != nil {
			return nil, fmt.Errorf("recover state from %s.lastgood: %w", path, errors.Join(err, backupErr))
		}
		s, err = decodeState(backup)
		if err != nil {
			return nil, fmt.Errorf("invalid last-good state: %w", err)
		}
		if err := writeStateFile(path, backup); err != nil {
			return nil, fmt.Errorf("restore state: %w", err)
		}
		fmt.Fprintf(os.Stderr, "tork: restored download state from %s.lastgood\n", path)
	}
	for i := range s.Entries {
		recoverMove(&s.Entries[i])
	}
	return s, nil
}

func decodeState(data []byte) (*State, error) {
	var s *State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("state must be a JSON object")
	}
	return s, nil
}

// Save writes atomically via temp file + rename.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	pending := false
	for _, entry := range s.Entries {
		if entry.PendingMove != nil {
			pending = true
			break
		}
	}
	return saveStateBytes(path, data, pending)
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

func Bool(v bool) *bool { return &v }

func (e Entry) SeedEnabled(defaultSeed bool) bool {
	if e.Seed == nil {
		return defaultSeed
	}
	return *e.Seed
}
