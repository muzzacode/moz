package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// stallTimeout is how long a task may produce no events before it is treated as
// hung.
//
// Every layer below already has its own deadline, so silence for this long means
// something escaped them: a provider holding a connection open, or a goroutine
// blocked on a channel nobody is reading. Without this the UI stays wedged and
// the only clue is an input that refuses new messages.
const stallTimeout = 10 * time.Minute

// watchdogInterval is how often the stall check runs.
const watchdogInterval = 30 * time.Second

type watchdogTick time.Time

// watchdog schedules the next stall check.
func watchdog() tea.Cmd {
	return tea.Tick(watchdogInterval, func(t time.Time) tea.Msg {
		return watchdogTick(t)
	})
}

// handleWatchdog clears a task that has stopped producing events.
//
// Waiting is normal, so the check is deliberately generous; it exists to make an
// unrecoverable state recoverable, not to enforce responsiveness.
func (m *Model) handleWatchdog() (tea.Model, tea.Cmd) {
	if !m.busy() || m.confirming {
		// An approval prompt is not a stall: it is waiting on the user.
		return m, watchdog()
	}
	if time.Since(m.lastEvent) < stallTimeout {
		return m, watchdog()
	}

	m.abort(fmt.Sprintf(
		"No response for %s — the task was abandoned. Your conversation is intact; try again or switch model with /models.",
		stallTimeout,
	))
	return m, watchdog()
}

// noteActivity records that the task is still alive.
func (m *Model) noteActivity() {
	m.lastEvent = time.Now()
}
