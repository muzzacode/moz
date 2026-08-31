package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAgentsFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", "# Notes\nRequires Java 25.\n")

	ins, ok := Load(dir)
	if !ok {
		t.Fatal("expected instructions to load")
	}
	if ins.Source != "AGENTS.md" {
		t.Fatalf("unexpected source %q", ins.Source)
	}
	if !strings.Contains(ins.Content, "Java 25") {
		t.Fatalf("content missing: %q", ins.Content)
	}
}

// AGENTS.md is the cross-tool convention and must win over the others.
func TestLoadPrefersAgentsOverOthers(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CLAUDE.md", "claude rules")
	write(t, dir, "AGENTS.md", "agents rules")

	ins, ok := Load(dir)
	if !ok {
		t.Fatal("expected instructions")
	}
	if ins.Source != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md to win, got %q", ins.Source)
	}
}

func TestLoadFallsBackToOtherConventions(t *testing.T) {
	for _, name := range []string{".mozrules", "CLAUDE.md", ".cursorrules", "CONVENTIONS.md"} {
		dir := t.TempDir()
		write(t, dir, name, "some rules")
		ins, ok := Load(dir)
		if !ok || ins.Source != name {
			t.Fatalf("expected %s to be loaded, got %+v ok=%v", name, ins, ok)
		}
	}
}

func TestLoadMissingIsNotAnError(t *testing.T) {
	if _, ok := Load(t.TempDir()); ok {
		t.Fatal("expected no instructions in an empty directory")
	}
}

// An empty or whitespace-only file must be skipped rather than injecting noise.
func TestLoadSkipsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", "   \n\n")
	write(t, dir, "CLAUDE.md", "real content")

	ins, ok := Load(dir)
	if !ok {
		t.Fatal("expected fallback to a non-empty file")
	}
	if ins.Source != "CLAUDE.md" {
		t.Fatalf("expected CLAUDE.md, got %q", ins.Source)
	}
}

func TestLoadTruncatesHugeFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", strings.Repeat("x", maxInstructionBytes*2))

	ins, ok := Load(dir)
	if !ok {
		t.Fatal("expected instructions")
	}
	if !ins.Truncated {
		t.Fatal("expected Truncated to be set")
	}
	if len(ins.Content) > maxInstructionBytes {
		t.Fatalf("content not capped: %d bytes", len(ins.Content))
	}
	if !strings.Contains(ins.Render(), "truncated") {
		t.Fatal("render should note truncation")
	}
}

func TestRenderIncludesSourceAndContent(t *testing.T) {
	ins := &Instructions{Source: "AGENTS.md", Content: "export JAVA_HOME=..."}
	got := ins.Render()
	if !strings.Contains(got, "AGENTS.md") || !strings.Contains(got, "JAVA_HOME") {
		t.Fatalf("render incomplete: %q", got)
	}
	if !strings.Contains(got, "override") {
		t.Fatal("render should tell the model these take precedence")
	}
}
