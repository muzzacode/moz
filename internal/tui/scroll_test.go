package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muzzacode/moz/internal/agent"
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

// A hung task must become recoverable on its own, otherwise the input refuses
// new messages with no way back.
func TestWatchdogClearsAStalledTask(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true
	m.lastEvent = time.Now().Add(-2 * stallTimeout)

	updated, _ := m.handleWatchdog()
	got := asModel(t, updated)

	if got.busy() {
		t.Fatal("watchdog should have cleared the stalled task")
	}
	if !strings.Contains(systemText(got)+got.errMsg, "abandoned") {
		t.Fatalf("expected an explanation, got %q / %q", systemText(got), got.errMsg)
	}
}

// Normal slow work must not be killed.
func TestWatchdogLeavesAnActiveTaskAlone(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel
	m.streaming = true
	m.lastEvent = time.Now()

	updated, _ := m.handleWatchdog()
	if !asModel(t, updated).busy() {
		t.Fatal("an active task must not be cancelled")
	}
}

// Waiting on an approval is not a stall.
func TestWatchdogDoesNotKillWhileAwaitingApproval(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel
	m.streaming = true
	m.confirming = true
	m.lastEvent = time.Now().Add(-2 * stallTimeout)

	updated, _ := m.handleWatchdog()
	if !asModel(t, updated).busy() {
		t.Fatal("an approval prompt is waiting on the user, not stalled")
	}
}

// Streamed narration before a tool call must be committed, not merged into the
// next turn's output.
func TestMessageEndCommitsStreamedText(t *testing.T) {
	m := newTestModel(t)
	m.pending = "Let me check that file."

	updated, _ := m.handleAgentEvent(agent.Event{Type: "message_end"})
	got := asModel(t, updated)

	if got.pending != "" {
		t.Fatalf("pending should be flushed, got %q", got.pending)
	}
	last := got.session.Messages[len(got.session.Messages)-1]
	if last.Role != "assistant" || last.Content != "Let me check that file." {
		t.Fatalf("expected the text committed as an assistant message, got %+v", last)
	}
}

// Submitting while busy must point at the way out.
func TestBusyMessageMentionsEsc(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel
	m.streaming = true
	m.textarea.SetValue("hello")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := asModel(t, updated); !strings.Contains(got.errMsg, "esc") {
		t.Fatalf("expected the message to mention esc, got %q", got.errMsg)
	}
}
