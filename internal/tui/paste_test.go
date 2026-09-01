package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muzzacode/moz/internal/memory"
)

func manyLines(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "some source code line"
	}
	return strings.Join(parts, "\n")
}

// A large paste must not fill the prompt, but must still be sent in full.
func TestLargePasteIsCollapsedButSentWhole(t *testing.T) {
	m := newTestModel(t)
	content := manyLines(60)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	got := asModel(t, updated)

	shown := got.textarea.Value()
	if strings.Contains(shown, "some source code line") {
		t.Fatalf("large paste should be hidden behind a placeholder, got %q", shown)
	}
	if !strings.Contains(shown, "60 lines") {
		t.Fatalf("placeholder should state the line count, got %q", shown)
	}

	// The full content must survive expansion.
	expanded := got.pastes.expand(shown)
	if strings.Count(expanded, "some source code line") != 60 {
		t.Fatalf("expected all 60 lines restored, got %d", strings.Count(expanded, "some source code line"))
	}
}

// Small pastes are ordinary input and must behave normally.
func TestSmallPasteIsNotCollapsed(t *testing.T) {
	m := newTestModel(t)
	content := manyLines(3)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	got := asModel(t, updated)

	if !strings.Contains(got.textarea.Value(), "some source code line") {
		t.Fatalf("a small paste should be inserted literally, got %q", got.textarea.Value())
	}
}

func TestMultiplePastesExpandIndependently(t *testing.T) {
	s := newPasteStore()
	a := s.collapse(manyLines(30))
	b := s.collapse("different content\n" + manyLines(25))

	if a == b {
		t.Fatal("placeholders must be distinct")
	}
	out := s.expand("before " + a + " middle " + b + " after")
	if !strings.Contains(out, "different content") {
		t.Fatal("second paste not restored")
	}
	if !strings.Contains(out, "before ") || !strings.Contains(out, " after") {
		t.Fatal("surrounding text must be preserved")
	}
	if strings.Contains(out, "pasted") {
		t.Fatalf("no placeholder should remain: %q", out)
	}
}

// Text the user typed that merely looks like a placeholder must be left alone.
func TestUnknownPlaceholderIsLeftIntact(t *testing.T) {
	s := newPasteStore()
	in := "see [#99 pasted 12 lines] above"
	if got := s.expand(in); got != in {
		t.Fatalf("unknown placeholders must be untouched, got %q", got)
	}
}

func TestResetClearsStoredPastes(t *testing.T) {
	s := newPasteStore()
	ph := s.collapse(manyLines(40))
	s.reset()
	if got := s.expand(ph); got != ph {
		t.Fatalf("after reset nothing should expand, got %q", got)
	}
}

func TestCountLines(t *testing.T) {
	if countLines("") != 0 {
		t.Fatal("empty string has no lines")
	}
	if countLines("one") != 1 {
		t.Fatal("single line")
	}
	if countLines("a\nb\nc") != 3 {
		t.Fatal("three lines")
	}
}

// The prompt must be multi-line, or a long message cannot be reviewed.
func TestPromptIsMultilineWithMarker(t *testing.T) {
	m := newTestModel(t)
	if promptHeight < 2 {
		t.Fatalf("prompt should be multi-line, height is %d", promptHeight)
	}
	if m.textarea.Prompt != promptMarker {
		t.Fatalf("expected the prompt marker %q, got %q", promptMarker, m.textarea.Prompt)
	}
}

// The viewport truncates rather than wraps, so long prose must be wrapped before
// it is handed over or most of a long answer is silently lost.
func TestLongLinesAreWrappedNotTruncated(t *testing.T) {
	long := strings.Repeat("word ", 60) // ~300 chars, one line
	out := wrapText(long, 80)

	if !strings.Contains(out, "\n") {
		t.Fatal("a long line should have been wrapped")
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) >= 80 {
			t.Fatalf("line exceeds the width: %d chars", len(line))
		}
	}
	// Nothing may be dropped.
	got := len(strings.Fields(out))
	if got != 60 {
		t.Fatalf("expected all 60 words preserved, got %d", got)
	}
}

// Code and diffs depend on leading whitespace, so they must not be reflowed.
func TestIndentedLinesAreNotReflowed(t *testing.T) {
	code := "    " + strings.Repeat("x", 200)
	if out := wrapText(code, 80); out != code {
		t.Fatal("indented lines must be left intact")
	}
}

func TestShortLinesUnchanged(t *testing.T) {
	in := "line one\nline two\nline three"
	if out := wrapText(in, 80); out != in {
		t.Fatalf("short lines should be untouched, got %q", out)
	}
}

// A single unbroken token still has to be split somewhere.
func TestOverlongWordIsBroken(t *testing.T) {
	out := wrapText(strings.Repeat("z", 250), 80)
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected the word to be broken across lines, got %d", len(lines))
	}
	if strings.Count(strings.Join(lines, ""), "z") != 250 {
		t.Fatal("no characters may be lost")
	}
}

func TestWrapTextHandlesZeroWidth(t *testing.T) {
	in := strings.Repeat("word ", 30)
	if out := wrapText(in, 0); out != in {
		t.Fatal("a zero width should be a no-op rather than a panic")
	}
}

func TestFormatMessageWrapsBody(t *testing.T) {
	msg := memory.Message{Role: "assistant", Content: strings.Repeat("word ", 60), Model: "GLM"}
	for _, line := range strings.Split(formatMessage(msg, 80), "\n") {
		if len(line) > 100 { // allow for style escape codes on the header
			t.Fatalf("unwrapped line of %d chars", len(line))
		}
	}
}
