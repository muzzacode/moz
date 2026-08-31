package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muzzacode/moz/internal/agent"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
)

// newTestModel builds a Model without a terminal. Everything is rooted in a
// temp dir so tests never touch the real config or memory directories.
func newTestModel(t *testing.T) *Model {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Default()
	cfg.MemoryDir = filepath.Join(dir, "memory")
	cfg.Workspace = dir
	cfg.DefaultModel = "test-model"

	registry := &models.Registry{
		Profiles: []models.Profile{{
			ID:            "test-model",
			Name:          "Test Model",
			ProviderKind:  models.ProviderOpenRouter,
			Model:         "test/model",
			Capabilities:  []models.Capability{models.CapToolCalling},
			ContextLength: 128000,
		}},
	}

	store := memory.New(cfg)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}

	// Run from the temp dir so file tools and instruction loading stay scoped.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	m, err := New(cfg, registry, store)
	if err != nil {
		t.Fatal(err)
	}
	// Mark ready so View and updateViewport behave as they would on screen.
	m.ready = true
	m.viewport.Width = 80
	m.viewport.Height = 20
	return m
}

// asModel normalises a returned tea.Model. handleSlash uses a pointer receiver
// while Update uses a value receiver, so both forms occur.
func asModel(t *testing.T, m tea.Model) *Model {
	t.Helper()
	switch v := m.(type) {
	case *Model:
		return v
	case Model:
		return &v
	}
	t.Fatalf("unexpected model type %T", m)
	return nil
}

func systemText(m *Model) string {
	var b strings.Builder
	for _, msg := range m.session.Messages {
		if msg.Role == "system" {
			b.WriteString(msg.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestBusyReflectsInFlightWork(t *testing.T) {
	m := newTestModel(t)
	if m.busy() {
		t.Fatal("a fresh model must not be busy")
	}

	m.streaming = true
	if !m.busy() {
		t.Fatal("streaming should count as busy")
	}

	m.streaming = false
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	if !m.busy() {
		t.Fatal("a live cancel func should count as busy")
	}
	cancel()
}

func TestAbortClearsStateAndPreservesPartialOutput(t *testing.T) {
	m := newTestModel(t)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true
	m.confirming = true
	m.currentStep = "reasoning"
	m.pending = "partial answer"

	m.abort("Interrupted")

	if m.busy() {
		t.Fatal("model should not be busy after abort")
	}
	if m.streaming || m.confirming || m.currentStep != "" {
		t.Fatal("in-flight UI state should be cleared")
	}
	if ctx.Err() == nil {
		t.Fatal("abort must cancel the context")
	}
	// Losing the partial answer would discard work the user already saw.
	var found bool
	for _, msg := range m.session.Messages {
		if msg.Role == "assistant" && msg.Content == "partial answer" {
			found = true
		}
	}
	if !found {
		t.Fatal("partial assistant output should be preserved in history")
	}
	if m.pending != "" {
		t.Fatal("pending buffer should be flushed")
	}
	if !strings.Contains(systemText(m), "Interrupted") {
		t.Fatal("abort reason should be shown")
	}
}

// After abort the update loop stops reading agentOut. Without draining, the
// agent goroutine blocks forever on its next send.
func TestAbortDrainsAgentChannelSoGoroutineCanExit(t *testing.T) {
	m := newTestModel(t)

	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.agentOut = make(chan agent.Event)
	m.agentApproval = make(chan bool)
	m.streaming = true

	// Capture the channel the way production code does, so the test itself does
	// not race on the model field.
	out := m.agentOut

	exited := make(chan struct{})
	// Stand in for the agent: keep emitting, then close on the way out.
	go func() {
		defer close(exited)
		defer close(out)
		for i := 0; i < 5; i++ {
			out <- agent.Event{Type: "step", Step: "working"}
		}
	}()

	// Let the sender block on an unread send before aborting.
	time.Sleep(20 * time.Millisecond)
	m.abort("Interrupted")

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("agent goroutine leaked: abort did not drain its events")
	}
}

// A cancelled agent waiting on approval must be released, not left blocked.
func TestDrainAutoDeniesPendingApprovals(t *testing.T) {
	events := make(chan agent.Event)
	approvals := make(chan bool, 1)

	go drain(events, approvals)

	events <- agent.Event{Type: "tool_call", ToolCall: &llm.ToolCall{Name: "exec"}}
	close(events)

	select {
	case approved := <-approvals:
		if approved {
			t.Fatal("a drained tool call must be denied")
		}
	case <-time.After(time.Second):
		t.Fatal("drain did not answer the pending approval")
	}
}

func TestFinishTaskReleasesCancel(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true

	m.finishTask()

	if m.cancel != nil {
		t.Fatal("cancel should be released")
	}
	if ctx.Err() == nil {
		t.Fatal("finishTask should cancel to free the context")
	}
	if m.busy() {
		t.Fatal("model should be idle after finishTask")
	}
}

// Esc must interrupt a running task, and only quit when idle. Quitting
// mid-task would throw away the conversation.
func TestEscInterruptsWhenBusyAndQuitsWhenIdle(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := asModel(t, updated)
	if got.busy() {
		t.Fatal("esc should have interrupted the task")
	}
	if isQuit(cmd) {
		t.Fatal("esc must not quit while a task is running")
	}

	// Now idle: esc should quit.
	_, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !isQuit(cmd) {
		t.Fatal("esc should quit when idle")
	}
}

func TestCtrlCInterruptsWhenBusy(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := asModel(t, updated)
	if got.busy() {
		t.Fatal("ctrl+c should have interrupted the task")
	}
	if isQuit(cmd) {
		t.Fatal("ctrl+c must not quit while a task is running")
	}
}

// isQuit reports whether cmd is tea.Quit by executing it, which is safe because
// Quit simply returns a QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func TestCancelledEventEndsTaskCleanly(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true

	updated, _ := m.handleAgentEvent(agent.Event{Type: "cancelled"})
	got := asModel(t, updated)

	if got.busy() {
		t.Fatal("cancelled event should end the task")
	}
	if !strings.Contains(systemText(got), "cancelled") {
		t.Fatalf("expected a cancellation notice, got %q", systemText(got))
	}
}

func TestWarningAndVerifiedEventsAreShown(t *testing.T) {
	for _, kind := range []string{"warning", "compacted", "verified"} {
		m := newTestModel(t)
		m.agentOut = make(chan agent.Event, 1)

		updated, _ := m.handleAgentEvent(agent.Event{Type: kind, Step: "notable: " + kind})
		got := asModel(t, updated)

		if !strings.Contains(systemText(got), "notable: "+kind) {
			t.Fatalf("%s event was not surfaced", kind)
		}
	}
}

// A cancelled stream is a user action and must not be reported as an error.
func TestStreamCancellationIsNotAnError(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true

	updated, _ := m.handleStreamEvent(llm.StreamEvent{Err: context.Canceled})
	got := asModel(t, updated)

	if got.errMsg != "" {
		t.Fatalf("cancellation should not surface as an error, got %q", got.errMsg)
	}
	if got.busy() {
		t.Fatal("stream should be finished")
	}
}

func TestStreamErrorIsReported(t *testing.T) {
	m := newTestModel(t)
	m.streaming = true

	updated, _ := m.handleStreamEvent(llm.StreamEvent{Err: context.DeadlineExceeded})
	got := asModel(t, updated)

	if got.errMsg == "" {
		t.Fatal("a real stream error should be reported")
	}
}

func TestUndoRefusedWhileBusy(t *testing.T) {
	m := newTestModel(t)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel

	updated, _ := m.handleSlash("/undo")
	got := asModel(t, updated)

	if !strings.Contains(got.errMsg, "esc") {
		t.Fatalf("expected a refusal mentioning esc, got %q", got.errMsg)
	}
}

func TestUndoRestoresAgentEdit(t *testing.T) {
	m := newTestModel(t)

	path := filepath.Join(m.cfg.Workspace, "f.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	m.checkpoints.Begin("task")
	if err := m.toolkit.EditFile(path, "original", "modified"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "modified" {
		t.Fatal("edit did not apply")
	}

	updated, _ := m.handleSlash("/undo")
	got := asModel(t, updated)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("undo did not restore the file, got %q", data)
	}
	if !strings.Contains(systemText(got), "Undid") {
		t.Fatal("undo should report what it did")
	}
}

func TestUndoWithNothingRecorded(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.handleSlash("/undo")
	if asModel(t, updated).errMsg == "" {
		t.Fatal("expected an error when there is nothing to undo")
	}
}

func TestSlashModelSwitchesProfile(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.handleSlash("/model test-model")
	got := asModel(t, updated)

	if got.profile.ID != "test-model" {
		t.Fatalf("unexpected profile %q", got.profile.ID)
	}
	if got.mode != "manual" {
		t.Fatalf("selecting a model should switch to manual, got %q", got.mode)
	}
}

func TestSlashModelUnknownReportsError(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.handleSlash("/model does-not-exist")
	if asModel(t, updated).errMsg == "" {
		t.Fatal("expected an error for an unknown model")
	}
}

func TestSlashAgentToggles(t *testing.T) {
	m := newTestModel(t)
	m.agentEnabled = false

	updated, _ := m.handleSlash("/agent on")
	if !asModel(t, updated).agentEnabled {
		t.Fatal("/agent on should enable the agent")
	}

	next := asModel(t, updated)
	updated2, _ := next.handleSlash("/agent off")
	if asModel(t, updated2).agentEnabled {
		t.Fatal("/agent off should disable the agent")
	}
}

func TestSlashUnknownCommand(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.handleSlash("/nonsense")
	if !strings.Contains(asModel(t, updated).errMsg, "unknown command") {
		t.Fatal("expected an unknown-command error")
	}
}

func TestSlashNewStartsFreshSessionAndSavesOld(t *testing.T) {
	m := newTestModel(t)
	m.session.Messages = append(m.session.Messages, memory.Message{Role: "user", Content: "earlier work"})
	oldID := m.session.ID

	updated, _ := m.handleSlash("/new")
	got := asModel(t, updated)

	if got.session.ID == oldID {
		t.Fatal("expected a new session")
	}
	// The previous conversation must survive on disk.
	prev, err := m.store.LoadSession(oldID)
	if err != nil {
		t.Fatalf("previous session was not saved: %v", err)
	}
	if len(prev.Messages) == 0 {
		t.Fatal("saved session is empty")
	}
}

func TestSlashSessionsListsSavedSessions(t *testing.T) {
	m := newTestModel(t)
	m.session.Messages = append(m.session.Messages, memory.Message{Role: "user", Content: "first task"})
	m.saveSession()

	updated, _ := m.handleSlash("/sessions")
	got := asModel(t, updated)

	if !strings.Contains(systemText(got), "first task") {
		t.Fatalf("expected the session preview to be listed, got %q", systemText(got))
	}
}

func TestSlashHelpMentionsUndoAndInterrupt(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.handleSlash("/help")
	text := systemText(asModel(t, updated))
	for _, want := range []string{"/undo", "Esc"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help should mention %q, got %q", want, text)
		}
	}
}

func TestDescribeEditShowsDiffNotJSON(t *testing.T) {
	m := newTestModel(t)
	path := filepath.Join(m.cfg.Workspace, "Makefile")
	if err := os.WriteFile(path, []byte("build:\n\tgo build\n\nhelp:\n\t@echo hi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "help:\n\t@echo hi",
		"new_string": "ci: build",
	})
	desc := m.describeToolCall(&llm.ToolCall{Name: "edit_file", Arguments: args})

	if strings.Contains(desc, "old_string") {
		t.Fatalf("approval text should be a diff, not raw JSON:\n%s", desc)
	}
	if !strings.Contains(desc, "- help:") || !strings.Contains(desc, "+ ci: build") {
		t.Fatalf("expected a diff preview, got:\n%s", desc)
	}
}

// write_file refuses to overwrite, so the preview must warn instead of
// pretending the call will succeed.
func TestDescribeWriteWarnsWhenFileExists(t *testing.T) {
	m := newTestModel(t)
	path := filepath.Join(m.cfg.Workspace, "exists.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{"path": path, "content": "new"})
	desc := m.describeToolCall(&llm.ToolCall{Name: "write_file", Arguments: args})

	if !strings.Contains(desc, "already exists") {
		t.Fatalf("expected an existing-file warning, got:\n%s", desc)
	}
}

func TestDescribeWriteShowsNewFileContent(t *testing.T) {
	m := newTestModel(t)
	path := filepath.Join(m.cfg.Workspace, "brand-new.go")

	args, _ := json.Marshal(map[string]any{"path": path, "content": "package main\n"})
	desc := m.describeToolCall(&llm.ToolCall{Name: "write_file", Arguments: args})

	if !strings.Contains(desc, "create") || !strings.Contains(desc, "package main") {
		t.Fatalf("expected a creation preview, got:\n%s", desc)
	}
}

func TestDescribeExecShowsPlainCommand(t *testing.T) {
	m := newTestModel(t)
	args, _ := json.Marshal(map[string]any{"command": "go test ./..."})
	desc := m.describeToolCall(&llm.ToolCall{Name: "exec", Arguments: args})

	if desc != "run: go test ./..." {
		t.Fatalf("unexpected description: %q", desc)
	}
}

func TestDescribeUnknownToolFallsBackToJSON(t *testing.T) {
	m := newTestModel(t)
	args, _ := json.Marshal(map[string]any{"query": "x"})
	desc := m.describeToolCall(&llm.ToolCall{Name: "web_search", Arguments: args})

	if !strings.Contains(desc, "web_search") {
		t.Fatalf("expected the tool name in the fallback, got %q", desc)
	}
}

func TestFormatConfirmPutsPromptOnOwnLineForDiffs(t *testing.T) {
	single := formatConfirm("run: ls")
	if !strings.HasPrefix(single, "Allow ") {
		t.Fatalf("single-line prompt should be inline, got %q", single)
	}

	multi := formatConfirm("edit f.go\n  1 - a\n  1 + b")
	if !strings.Contains(multi, "\n\nApprove? [y/n]") {
		t.Fatalf("multi-line prompt should be separated, got %q", multi)
	}
}

func TestTruncateAddsMarker(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Fatalf("short input should be unchanged, got %q", got)
	}
	got := truncate(strings.Repeat("x", 500), 100)
	if !strings.Contains(got, "truncated") {
		t.Fatal("expected a truncation marker")
	}
	if len(got) > 200 {
		t.Fatalf("truncate produced %d chars", len(got))
	}
}

func TestViewRendersWithoutPanicAndShowsInterruptHint(t *testing.T) {
	m := newTestModel(t)

	if out := m.View(); out == "" {
		t.Fatal("View returned empty output")
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel
	if !strings.Contains(m.View(), "esc: interrupt") {
		t.Fatal("busy view should advertise the interrupt key")
	}

	m.cancel = nil
	if strings.Contains(m.View(), "esc: interrupt") {
		t.Fatal("idle view should not advertise interrupt")
	}
}

// The approval prompt is modal: y/n must be consumed by it rather than typed
// into the textarea.
func TestConfirmingConsumesYesNoKeys(t *testing.T) {
	m := newTestModel(t)
	var mu sync.Mutex
	var answered []bool

	m.confirming = true
	m.confirmText = "Approve? [y/n]"
	m.onConfirmYes = func() tea.Cmd {
		mu.Lock()
		answered = append(answered, true)
		mu.Unlock()
		return nil
	}
	m.onConfirmNo = func() tea.Cmd {
		mu.Lock()
		answered = append(answered, false)
		mu.Unlock()
		return nil
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	got := asModel(t, updated)
	if got.confirming {
		t.Fatal("y should close the prompt")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(answered) != 1 || !answered[0] {
		t.Fatalf("expected a single approval, got %v", answered)
	}
}

func TestConfirmingEscDenies(t *testing.T) {
	m := newTestModel(t)
	denied := false
	m.confirming = true
	m.onConfirmYes = func() tea.Cmd { return nil }
	m.onConfirmNo = func() tea.Cmd { denied = true; return nil }

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if asModel(t, updated).confirming {
		t.Fatal("esc should close the prompt")
	}
	if !denied {
		t.Fatal("esc should deny the pending call")
	}
}
