// Package checkpoint snapshots files before the agent modifies them so any
// change can be reversed.
//
// The agent is not always right, and a bad edit to a file that is not in git
// would otherwise be unrecoverable. Every mutation is recorded with the file's
// prior contents, and the whole task can be rolled back in one step.
package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry records one file's state before a change.
type Entry struct {
	Path string
	// Existed distinguishes "modified" from "created". Undoing a creation means
	// deleting the file, not restoring empty contents.
	Existed  bool
	Contents []byte
	Mode     os.FileMode
	Time     time.Time
}

// Store holds snapshots for the current session, grouped into labelled batches
// so a single task can be undone as a unit.
type Store struct {
	mu      sync.Mutex
	batches []Batch
	// maxBatches bounds memory use over a long session.
	maxBatches int
}

type Batch struct {
	Label   string
	Time    time.Time
	Entries []Entry
}

const defaultMaxBatches = 50

func New() *Store {
	return &Store{maxBatches: defaultMaxBatches}
}

// Begin starts a new batch. Calling it repeatedly with no recorded entries in
// between does not accumulate empty batches.
func (s *Store) Begin(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n := len(s.batches); n > 0 && len(s.batches[n-1].Entries) == 0 {
		s.batches[n-1].Label = label
		s.batches[n-1].Time = time.Now()
		return
	}
	s.batches = append(s.batches, Batch{Label: label, Time: time.Now()})
	if len(s.batches) > s.maxBatches {
		s.batches = s.batches[len(s.batches)-s.maxBatches:]
	}
}

// Record snapshots path's current state.
//
// It must be called before the file is written. Recording the same path twice
// in one batch keeps only the earliest snapshot, which is the state to restore.
func (s *Store) Record(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	entry := Entry{Path: abs, Time: time.Now(), Mode: 0644}
	if info, statErr := os.Stat(abs); statErr == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", path)
		}
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			return readErr
		}
		entry.Existed = true
		entry.Contents = data
		entry.Mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.batches) == 0 {
		s.batches = append(s.batches, Batch{Label: "changes", Time: time.Now()})
	}
	cur := &s.batches[len(s.batches)-1]
	for _, e := range cur.Entries {
		if e.Path == abs {
			// Earliest snapshot wins; it represents the pre-task state.
			return nil
		}
	}
	cur.Entries = append(cur.Entries, entry)
	return nil
}

// UndoLast reverses the most recent non-empty batch and returns what it did.
func (s *Store) UndoLast() ([]string, error) {
	s.mu.Lock()
	idx := -1
	for i := len(s.batches) - 1; i >= 0; i-- {
		if len(s.batches[i].Entries) > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return nil, fmt.Errorf("nothing to undo")
	}
	batch := s.batches[idx]
	s.batches = append(s.batches[:idx], s.batches[idx+1:]...)
	s.mu.Unlock()

	var actions []string
	var firstErr error

	// Restore in reverse order so nested creations unwind cleanly.
	for i := len(batch.Entries) - 1; i >= 0; i-- {
		e := batch.Entries[i]
		action, err := restore(e)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		actions = append(actions, action)
	}

	sort.Strings(actions)
	return actions, firstErr
}

func restore(e Entry) (string, error) {
	if !e.Existed {
		if err := os.Remove(e.Path); err != nil {
			if os.IsNotExist(err) {
				return "already absent " + e.Path, nil
			}
			return "", err
		}
		return "deleted " + e.Path, nil
	}
	if err := os.MkdirAll(filepath.Dir(e.Path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(e.Path, e.Contents, e.Mode); err != nil {
		return "", err
	}
	// WriteFile only applies the mode when it creates the file, so an existing
	// file keeps whatever permissions it currently has. Restoring an
	// executable script therefore requires an explicit chmod.
	if err := os.Chmod(e.Path, e.Mode); err != nil {
		return "", err
	}
	return "restored " + e.Path, nil
}

// Pending reports how many files the most recent non-empty batch would restore.
func (s *Store) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.batches) - 1; i >= 0; i-- {
		if n := len(s.batches[i].Entries); n > 0 {
			return n
		}
	}
	return 0
}

// Batches returns a copy of the recorded batches, newest first.
func (s *Store) Batches() []Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Batch, 0, len(s.batches))
	for i := len(s.batches) - 1; i >= 0; i-- {
		if len(s.batches[i].Entries) > 0 {
			out = append(out, s.batches[i])
		}
	}
	return out
}
