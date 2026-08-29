package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/ollama"
	"github.com/muzzacode/moz/internal/version"
)

var (
	enterBinding      = key.NewBinding(key.WithKeys("enter"))
	shiftEnterBinding = key.NewBinding(key.WithKeys("shift+enter"))
)

type Model struct {
	cfg      *config.Config
	registry *models.Registry
	store    *memory.Store
	session  *memory.Session
	profile  *models.Profile

	viewport   viewport.Model
	textarea   textarea.Model
	ready      bool
	streaming  bool
	streamChan chan ollama.StreamEvent
	pending    string
	errMsg     string
}

var (
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ECDC4"))
	systemStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#95A5A6"))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E74C3C"))
	statusStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2C3E50")).Foreground(lipgloss.Color("#ECF0F1"))
)

func New(cfg *config.Config, registry *models.Registry, store *memory.Store) (*Model, error) {
	profile, err := registry.Find(cfg.DefaultModel)
	if err != nil {
		profile = &registry.Profiles[0]
	}

	ta := textarea.New()
	ta.Placeholder = "Type a message, or /exit to quit"
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.KeyMap.InsertNewline = shiftEnterBinding
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.SetContent("")

	sess := memory.NewSession()

	return &Model{
		cfg:        cfg,
		registry:   registry,
		store:      store,
		session:    sess,
		profile:    profile,
		textarea:   ta,
		viewport:   vp,
		streamChan: make(chan ollama.StreamEvent),
	}, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.EnterAltScreen,
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 4
		m.textarea.SetWidth(msg.Width)
		m.textarea.SetHeight(3)
		m.ready = true
		m.updateViewport()

	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC, msg.Type == tea.KeyEsc:
			m.saveSession()
			return m, tea.Quit
		case key.Matches(msg, enterBinding):
			return m.submitInput()
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)

	case ollama.StreamEvent:
		return m.handleStreamEvent(msg)

	case errMsg:
		m.errMsg = string(msg)
	}

	return m, tea.Batch(cmds...)
}

type errMsg string

func (m *Model) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	m.textarea.Reset()
	m.textarea.Blur()
	m.textarea.Focus()

	if input == "" {
		return m, nil
	}

	if strings.HasPrefix(input, "/") {
		return m.handleSlash(input)
	}

	if m.streaming {
		m.errMsg = "wait for the current response to finish"
		return m, nil
	}

	m.session.Messages = append(m.session.Messages, memory.Message{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now().UTC(),
	})
	m.updateViewport()

	return m, m.startStream()
}

func (m *Model) handleSlash(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return m, nil
	}

	switch parts[0] {
	case "/exit", "/quit":
		m.saveSession()
		return m, tea.Quit

	case "/model":
		if len(parts) < 2 {
			m.errMsg = "usage: /model <profile-id>"
			return m, nil
		}
		p, err := m.registry.Find(parts[1])
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.profile = p
		m.errMsg = ""
		m.addSystem(fmt.Sprintf("Switched to model: %s", p.Name))
		return m, nil

	case "/memory":
		m.addSystem(m.store.Summary())
		return m, nil

	case "/clear":
		m.session = memory.NewSession()
		m.pending = ""
		m.streaming = false
		m.updateViewport()
		m.addSystem("New session started")
		return m, nil

	default:
		m.errMsg = fmt.Sprintf("unknown command: %s", parts[0])
		return m, nil
	}
}

func (m *Model) addSystem(text string) {
	m.session.Messages = append(m.session.Messages, memory.Message{
		Role:      "system",
		Content:   text,
		Timestamp: time.Now().UTC(),
	})
	m.updateViewport()
}

func (m *Model) startStream() tea.Cmd {
	m.streaming = true
	m.pending = ""
	m.errMsg = ""

	ch := make(chan ollama.StreamEvent)
	m.streamChan = ch

	client := ollama.New(m.profile)
	go client.ChatStream(context.Background(), m.session.Messages, ch)

	return waitForStream(ch)
}

func waitForStream(ch chan ollama.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ollama.StreamEvent{Done: true}
		}
		return ev
	}
}

func (m Model) handleStreamEvent(ev ollama.StreamEvent) (tea.Model, tea.Cmd) {
	if ev.Err != nil {
		m.streaming = false
		m.errMsg = ev.Err.Error()
		m.updateViewport()
		return m, nil
	}

	if ev.Done {
		m.streaming = false
		if m.pending != "" {
			m.session.Messages = append(m.session.Messages, memory.Message{
				Role:      "assistant",
				Content:   m.pending,
				Model:     m.profile.Name,
				Timestamp: time.Now().UTC(),
			})
			m.pending = ""
		}
		m.saveSession()
		m.updateViewport()
		return m, nil
	}

	m.pending += ev.Content
	m.updateViewport()
	return m, waitForStream(m.streamChan)
}

func (m *Model) updateViewport() {
	var b strings.Builder
	for _, msg := range m.session.Messages {
		b.WriteString(formatMessage(msg))
		b.WriteString("\n\n")
	}
	if m.streaming && m.pending != "" {
		b.WriteString(formatMessage(memory.Message{Role: "assistant", Content: m.pending, Model: m.profile.Name, Timestamp: time.Now().UTC()}))
		b.WriteString("▌")
	}
	if m.errMsg != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(m.errMsg))
		b.WriteString("\n")
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func formatMessage(m memory.Message) string {
	switch m.Role {
	case "user":
		return userStyle.Render("You") + "\n" + m.Content
	case "assistant":
		header := assistantStyle.Render("Moz")
		if m.Model != "" {
			header = assistantStyle.Render("Moz") + systemStyle.Render(" ("+m.Model+")")
		}
		return header + "\n" + m.Content
	case "system":
		return systemStyle.Render(m.Content)
	default:
		return m.Content
	}
}

func (m *Model) saveSession() {
	if m.store == nil {
		return
	}
	_ = m.store.SaveSession(m.session)
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing Moz..."
	}

	status := statusStyle.Width(m.viewport.Width).Render(
		fmt.Sprintf(" moz %s | model: %s | streaming: %v ", version.Version, m.profile.Name, m.streaming),
	)

	return fmt.Sprintf(
		"%s\n%s\n%s",
		m.viewport.View(),
		m.textarea.View(),
		status,
	)
}

func Run(cfg *config.Config, registry *models.Registry, store *memory.Store) error {
	m, err := New(cfg, registry, store)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
