package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUndoRestoresModifiedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	write(t, p, "original")

	s := New()
	s.Begin("edit")
	if err := s.Record(p); err != nil {
		t.Fatal(err)
	}
	write(t, p, "clobbered")

	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "original" {
		t.Fatalf("expected original contents, got %q", got)
	}
}

// Undoing a creation must delete the file, not leave an empty one behind.
func TestUndoDeletesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.txt")

	s := New()
	s.Begin("create")
	if err := s.Record(p); err != nil {
		t.Fatal(err)
	}
	write(t, p, "created")

	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("created file should have been removed")
	}
}

func TestUndoRestoresWholeBatch(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	write(t, a, "A")
	write(t, b, "B")

	s := New()
	s.Begin("task")
	_ = s.Record(a)
	_ = s.Record(b)
	write(t, a, "A-mod")
	write(t, b, "B-mod")

	actions, err := s.UndoLast()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %v", actions)
	}
	if read(t, a) != "A" || read(t, b) != "B" {
		t.Fatal("both files should be restored")
	}
}

// Only the most recent batch is undone, so earlier work is preserved.
func TestUndoOnlyAffectsLastBatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	write(t, p, "v1")

	s := New()
	s.Begin("first")
	_ = s.Record(p)
	write(t, p, "v2")

	s.Begin("second")
	_ = s.Record(p)
	write(t, p, "v3")

	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "v2" {
		t.Fatalf("expected v2 after one undo, got %q", got)
	}
	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "v1" {
		t.Fatalf("expected v1 after two undos, got %q", got)
	}
}

// The earliest snapshot in a batch is the pre-task state and must win.
func TestRecordTwiceKeepsEarliestSnapshot(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	write(t, p, "original")

	s := New()
	s.Begin("task")
	_ = s.Record(p)
	write(t, p, "intermediate")
	_ = s.Record(p)
	write(t, p, "final")

	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "original" {
		t.Fatalf("expected original, got %q", got)
	}
}

func TestUndoWithNothingRecorded(t *testing.T) {
	s := New()
	if _, err := s.UndoLast(); err == nil {
		t.Fatal("expected an error when there is nothing to undo")
	}
}

// Repeated Begin with no changes must not create undo entries that appear to
// do something.
func TestEmptyBatchesAreNotUndoable(t *testing.T) {
	s := New()
	s.Begin("a")
	s.Begin("b")
	s.Begin("c")
	if _, err := s.UndoLast(); err == nil {
		t.Fatal("empty batches should not be undoable")
	}
	if s.Pending() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.Pending())
	}
}

func TestPendingCountsLastBatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	write(t, p, "x")

	s := New()
	s.Begin("t")
	_ = s.Record(p)
	if s.Pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.Pending())
	}
}

func TestRecordPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script.sh")
	write(t, p, "#!/bin/sh\n")
	if err := os.Chmod(p, 0755); err != nil {
		t.Fatal(err)
	}

	s := New()
	s.Begin("t")
	_ = s.Record(p)
	write(t, p, "clobbered")
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UndoLast(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("expected mode 0755 restored, got %o", info.Mode().Perm())
	}
}

func TestRecordRejectsDirectory(t *testing.T) {
	s := New()
	s.Begin("t")
	if err := s.Record(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory")
	}
}

func TestBatchesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	write(t, p, "x")

	s := New()
	s.Begin("older")
	_ = s.Record(p)
	s.Begin("newer")
	_ = s.Record(p)

	got := s.Batches()
	if len(got) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(got))
	}
	if got[0].Label != "newer" {
		t.Fatalf("expected newest first, got %q", got[0].Label)
	}
}
