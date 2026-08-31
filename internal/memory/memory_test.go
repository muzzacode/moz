package memory

import (
	"testing"
	"time"

	"github.com/muzzacode/moz/internal/config"
)

// Session IDs are filenames. Two sessions created in quick succession must not
// collide, or starting a new session would overwrite the previous one on disk.
func TestNewSessionIDsAreUniqueInTightLoop(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := NewSession().ID
		if seen[id] {
			t.Fatalf("duplicate session ID after %d iterations: %s", i, id)
		}
		seen[id] = true
	}
}

// IDs are also used for newest-first ordering, so the timestamp prefix must
// stay lexicographically sortable.
func TestNewSessionIDStartsWithSortableTimestamp(t *testing.T) {
	id := NewSession().ID
	if len(id) < 15 || id[8] != 'T' {
		t.Fatalf("unexpected ID shape: %s", id)
	}
}

func TestSessionInfosAndLatest(t *testing.T) {
	cfg := config.Default()
	cfg.MemoryDir = t.TempDir()
	store := New(cfg)

	older := &Session{
		ID:      "older",
		Started: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Messages: []Message{
			{Role: "system", Content: "ignored"},
			{Role: "user", Content: "first saved conversation"},
		},
	}
	newer := &Session{
		ID:      "newer",
		Started: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
		Messages: []Message{
			{Role: "user", Content: "most recent conversation"},
		},
	}

	if err := store.SaveSession(older); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(newer); err != nil {
		t.Fatal(err)
	}

	infos, err := store.SessionInfos()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].ID != "newer" || infos[1].ID != "older" {
		t.Fatalf("sessions not sorted newest first: %#v", infos)
	}
	if infos[1].Preview != "first saved conversation" {
		t.Fatalf("unexpected preview: %q", infos[1].Preview)
	}

	latest, err := store.LatestSession()
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != "newer" {
		t.Fatalf("expected newer, got %s", latest.ID)
	}
}
