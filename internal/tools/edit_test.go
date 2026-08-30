package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/safepath"
)

func testToolkit(t *testing.T) (*Toolkit, string) {
	t.Helper()
	dir := t.TempDir()
	return New(safepath.New([]string{dir}), nil), dir
}

func TestWriteFileRefusesOverwrite(t *testing.T) {
	tk, dir := testToolkit(t)
	path := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(path, []byte("help:\n\techo help\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := tk.WriteFile(path, "ci: vet test\n")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
}

func TestEditFileRequiresUniqueMatch(t *testing.T) {
	tk, dir := testToolkit(t)
	path := filepath.Join(dir, "Makefile")
	original := ".PHONY: help\n\nhelp:\n\t@echo help\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	err := tk.EditFile(path, "help", "ci")
	if err == nil || !strings.Contains(err.Error(), "occurs") {
		t.Fatalf("expected uniqueness error, got %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("file was modified after failed edit:\n%s", got)
	}
}

func TestEditFileUniqueContextSucceeds(t *testing.T) {
	tk, dir := testToolkit(t)
	path := filepath.Join(dir, "Makefile")
	original := "vet:\n\tgo vet ./...\n\nhelp:\n\t@echo help\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := tk.EditFile(path, "help:\n\t@echo help", "ci: vet test\n\nhelp:\n\t@echo help"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ci: vet test") || !strings.Contains(string(got), "help:") {
		t.Fatalf("unexpected content:\n%s", got)
	}
}
