package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muzzacode/moz/internal/memory"
)

// fill puts enough history in the session to make the viewport scrollable.
func fill(m *Model, n int) {
	for i := 0; i < n; i++ {
		m.session.Messages = append(m.session.Messages, memory.Message{
			Role: "user", Content: strings.Repeat("line ", 10),
		})
	}
	m.updateViewport()
}

// The textarea consumes every key it is handed, so without explicit routing the
// conversation cannot be scrolled at all.
func TestScrollKeysReachTheViewport(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)

	if !m.viewport.AtBottom() {
		t.Fatal("precondition: should start at the bottom")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	got := asModel(t, updated)

	if got.viewport.AtBottom() {
		t.Fatal("pgup should have scrolled away from the bottom")
	}
}

func TestHomeAndEndJumpToEnds(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	got := asModel(t, updated)
	if !got.viewport.AtTop() {
		t.Fatal("home should jump to the top")
	}

	updated2, _ := got.Update(tea.KeyMsg{Type: tea.KeyEnd})
	got2 := asModel(t, updated2)
	if !got2.viewport.AtBottom() {
		t.Fatal("end should jump to the bottom")
	}
}

// Scrolling back to read something must not be undone by the next streamed
// token, which is what an unconditional GotoBottom would do.
func TestNewOutputDoesNotYankAScrolledUpView(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)

	m.viewport.GotoTop()
	if !m.viewport.AtTop() {
		t.Fatal("precondition: should be at the top")
	}

	m.pending = "streaming output arriving"
	m.updateViewport()

	if m.viewport.AtBottom() {
		t.Fatal("new output should not force the view back to the bottom")
	}
}

// When already following along, new output should keep following.
func TestNewOutputFollowsWhenAtBottom(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)
	m.viewport.GotoBottom()

	fill(m, 20)

	if !m.viewport.AtBottom() {
		t.Fatal("expected the view to keep following new output")
	}
}

// A prompt is useless if it is off screen.
func TestApprovalPromptForcesScrollToBottom(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)
	m.viewport.GotoTop()

	m.confirming = true
	m.confirmText = "Approve? [y/n]"
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatal("an approval prompt must be brought into view")
	}
}

// Ordinary typing must still reach the input.
func TestPlainKeysStillReachTheInput(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if got := asModel(t, updated); got.textarea.Value() != "h" {
		t.Fatalf("expected typing to reach the input, got %q", got.textarea.Value())
	}
}

func TestMouseWheelScrollsHistory(t *testing.T) {
	m := newTestModel(t)
	m.viewport.Height = 5
	fill(m, 100)

	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if asModel(t, updated).viewport.AtBottom() {
		t.Fatal("wheel up should scroll the history")
	}
}
