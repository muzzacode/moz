package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muzzacode/moz/internal/adaptive"
	"github.com/muzzacode/moz/internal/agent"
	"github.com/muzzacode/moz/internal/approval"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/cost"
	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/safepath"
	"github.com/muzzacode/moz/internal/todo"
	"github.com/muzzacode/moz/internal/tools"
	"github.com/muzzacode/moz/internal/version"
	openai "github.com/sashabaranov/go-openai"
)

var (
	enterBinding      = key.NewBinding(key.WithKeys("enter"))
	shiftEnterBinding = key.NewBinding(key.WithKeys("shift+enter"))
)

type Model struct {
	cfg       *config.Config
	registry  *models.Registry
	store     *memory.Store
	session   *memory.Session
	creds     *credentials.Manager
	router    *adaptive.Router
	toolkit   *tools.Toolkit
	todos     *todo.List
	todoStore *todo.Store

	profile      *models.Profile
	mode         string
	lastDecision *adaptive.Decision

	viewport   viewport.Model
	textarea   textarea.Model
	ready      bool
	streaming  bool
	streamChan chan llm.StreamEvent
	pending    string
	errMsg     string
	totalUsage openai.Usage

	confirming   bool
	confirmText  string
	onConfirmYes func() tea.Cmd
	onConfirmNo  func() tea.Cmd

	// Agent loop.
	agentEnabled  bool
	agentOut      chan agent.Event
	agentApproval chan bool
	currentStep   string
	startTime     time.Time
	elapsed       time.Duration
}

var (
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ECDC4"))
	systemStyle    = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#95A5A6"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E74C3C"))
	statusStyle    = lipgloss.NewStyle().Background(lipgloss.Color("#2C3E50")).Foreground(lipgloss.Color("#ECF0F1"))
	infoStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#3498DB"))
	warnStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F"))
)

func New(cfg *config.Config, registry *models.Registry, store *memory.Store) (*Model, error) {
	creds := credentials.New()
	router := adaptive.New(registry, creds)
	router.PreferLocal = cfg.Adaptive.PreferLocal

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	allowed := []string{cwd, home}
	if cfg.Workspace != "" {
		allowed = append(allowed, cfg.Workspace)
	}

	safe := safepath.New(allowed)
	todoStore := todo.NewStore(cfg)
	todos, err := todoStore.Load()
	if err != nil {
		todos = todo.New()
	}
	toolkit := tools.New(safe, todos)

	profile, err := registry.Find(cfg.DefaultModel)
	if err != nil {
		profile = &registry.Profiles[0]
	}

	mode := cfg.Mode
	if mode == "" {
		mode = "adaptive"
	}

	ta := textarea.New()
	ta.Placeholder = "Type a message, or /help for commands"
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
		creds:      creds,
		router:     router,
		toolkit:    toolkit,
		todos:      todos,
		todoStore:  todoStore,
		profile:    profile,
		mode:       mode,
		agentEnabled: cfg.Agent,
		textarea:   ta,
		viewport:   vp,
		streamChan: make(chan llm.StreamEvent),
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
		if m.confirming {
			switch msg.Type {
			case tea.KeyCtrlC, tea.KeyEsc:
				m.confirming = false
				return m, m.onConfirmNo()
			}
			switch msg.String() {
			case "y", "Y":
				m.confirming = false
				return m, m.onConfirmYes()
			case "n", "N":
				m.confirming = false
				return m, m.onConfirmNo()
			}
			return m, nil
		}

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

	case llm.StreamEvent:
		return m.handleStreamEvent(msg)

	case agent.Event:
		return m.handleAgentEvent(msg)

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

	if m.streaming {
		m.errMsg = "wait for the current response to finish"
		return m, nil
	}

	if strings.HasPrefix(input, "/") {
		return m.handleSlash(input)
	}

	m.session.Messages = append(m.session.Messages, memory.Message{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now().UTC(),
	})

	if m.mode == "adaptive" {
		decision, err := m.router.Select(input)
		if err != nil {
			m.errMsg = err.Error()
			m.updateViewport()
			return m, nil
		}
		m.lastDecision = decision
		m.profile = decision.Profile
		if !m.agentEnabled {
			m.addSystem(fmt.Sprintf("Adaptive: %s", decision.Reason))
		}
	}

	m.updateViewport()

	if m.agentEnabled {
		return m, m.startAgent(input)
	}
	return m, m.startStream()
}

func (m *Model) handleSlash(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return m, nil
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "/exit", "/quit":
		m.saveSession()
		return m, tea.Quit

	case "/model":
		if len(args) < 1 {
			m.errMsg = "usage: /model <profile-id>"
			return m, nil
		}
		p, err := m.registry.Find(args[0])
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.profile = p
		m.mode = "manual"
		m.errMsg = ""
		m.addSystem(fmt.Sprintf("Switched to model: %s", p.Name))
		return m, nil

	case "/mode":
		if len(args) < 1 {
			m.errMsg = "usage: /mode adaptive | manual | <profile-id>"
			return m, nil
		}
		arg := args[0]
		switch arg {
		case "adaptive":
			m.mode = "adaptive"
			m.addSystem("Mode: adaptive")
		case "manual":
			m.mode = "manual"
			m.addSystem("Mode: manual")
		default:
			p, err := m.registry.Find(arg)
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.profile = p
			m.mode = "manual"
			m.addSystem(fmt.Sprintf("Mode: manual, model: %s", p.Name))
		}
		return m, nil

	case "/agent":
		if len(args) >= 1 {
			switch args[0] {
			case "on":
				m.agentEnabled = true
			case "off":
				m.agentEnabled = false
			default:
				m.errMsg = "usage: /agent on | off"
				return m, nil
			}
		} else {
			m.agentEnabled = !m.agentEnabled
		}
		state := "off"
		if m.agentEnabled {
			state = "on"
		}
		m.addSystem(fmt.Sprintf("Agent mode: %s", state))
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

	case "/read", "/cat":
		if len(args) < 1 {
			m.errMsg = "usage: /read <path>"
			return m, nil
		}
		return m.runWithApproval("read_file", args[0], nil, func() tea.Cmd {
			content, err := m.toolkit.ReadFile(args[0])
			if err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			m.addSystem(fmt.Sprintf("--- %s ---\n%s", args[0], truncate(content, 6000)))
			return nil
		})

	case "/list", "/ls":
		path := "."
		if len(args) >= 1 {
			path = args[0]
		}
		return m.runWithApproval("list_dir", path, nil, func() tea.Cmd {
			entries, err := m.toolkit.ListDir(path)
			if err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			var b strings.Builder
			for _, e := range entries {
				kind := "f"
				if e.Dir {
					kind = "d"
				}
				b.WriteString(fmt.Sprintf("[%s] %-30s %8d\n", kind, e.Name, e.Size))
			}
			m.addSystem(b.String())
			return nil
		})

	case "/grep":
		if len(args) < 1 {
			m.errMsg = "usage: /grep <pattern> [path]"
			return m, nil
		}
		pattern := args[0]
		path := "."
		if len(args) >= 2 {
			path = args[1]
		}
		return m.runWithApproval("grep", pattern, map[string]any{"pattern": pattern, "path": path}, func() tea.Cmd {
			matches, err := m.toolkit.Grep(pattern, path)
			if err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("%d matches\n", len(matches)))
			for _, match := range matches {
				rel, _ := filepath.Rel(path, match.File)
				if rel == "" {
					rel = match.File
				}
				b.WriteString(fmt.Sprintf("%s:%d: %s\n", rel, match.Line, match.Content))
			}
			m.addSystem(b.String())
			return nil
		})

	case "/run", "/exec":
		if len(args) < 1 {
			m.errMsg = "usage: /run <command>"
			return m, nil
		}
		command := strings.Join(args, " ")
		desc := fmt.Sprintf("run %q", command)
		return m.runWithApproval("exec", desc, map[string]any{"command": command}, func() tea.Cmd {
			res := m.toolkit.Exec(command, "")
			m.showResult(res)
			return nil
		})

	case "/write":
		if len(args) < 1 {
			m.errMsg = "usage: /write <path> <content>"
			return m, nil
		}
		path := args[0]
		content := ""
		if len(args) >= 2 {
			content = strings.Join(args[1:], " ")
		}
		desc := fmt.Sprintf("write %s", path)
		return m.runWithApproval("write_file", desc, map[string]any{"path": path}, func() tea.Cmd {
			if err := m.toolkit.WriteFile(path, content); err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			m.addSystem(fmt.Sprintf("Wrote %s", path))
			return nil
		})

	case "/edit":
		if len(args) < 2 {
			m.errMsg = "usage: /edit <path> <old> -> <new>"
			return m, nil
		}
		path := args[0]
		rest := strings.Join(args[1:], " ")
		split := strings.SplitN(rest, "->", 2)
		if len(split) != 2 {
			m.errMsg = "usage: /edit <path> <old> -> <new>"
			return m, nil
		}
		oldStr := strings.TrimSpace(split[0])
		newStr := strings.TrimSpace(split[1])
		desc := fmt.Sprintf("edit %s", path)
		return m.runWithApproval("edit_file", desc, map[string]any{"path": path}, func() tea.Cmd {
			if err := m.toolkit.EditFile(path, oldStr, newStr); err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			m.addSystem(fmt.Sprintf("Edited %s", path))
			return nil
		})

	case "/git", "/g":
		if len(args) < 1 {
			m.errMsg = "usage: /git status | diff | commit <msg>"
			return m, nil
		}
		return m.handleGit(args)

	case "/todo":
		return m.handleTodo(args)

	case "/fetch":
		if len(args) < 1 {
			m.errMsg = "usage: /fetch <url>"
			return m, nil
		}
		return m.runWithApproval("web_fetch", args[0], map[string]any{"url": args[0]}, func() tea.Cmd {
			content, err := m.toolkit.WebFetch(args[0])
			if err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			m.addSystem(truncate(content, 6000))
			return nil
		})

	case "/models":
		m.addSystem(m.renderModels())
		return m, nil

	case "/set":
		if len(args) < 2 {
			m.errMsg = "usage: /set <key> <value>"
			return m, nil
		}
		key := args[0]
		value := strings.Join(args[1:], " ")
		if err := m.creds.Save(key, value); err != nil {
			m.errMsg = err.Error()
		} else {
			m.addSystem(fmt.Sprintf("Saved credential %s", key))
		}
		return m, nil

	case "/help":
		m.addSystem("Commands: /models, /model, /mode, /agent, /memory, /clear, /read, /list, /grep, /run, /write, /edit, /git, /fetch, /todo, /set, /exit")
		return m, nil

	default:
		m.errMsg = fmt.Sprintf("unknown command: %s", cmd)
		return m, nil
	}
}

func (m *Model) handleGit(args []string) (tea.Model, tea.Cmd) {
	sub := args[0]
	switch sub {
	case "status":
		cwd := ""
		if len(args) >= 2 {
			cwd = args[1]
		}
		return m.runWithApproval("git_status", "git status", nil, func() tea.Cmd {
			res := m.toolkit.GitStatus(cwd)
			m.showResult(res)
			return nil
		})
	case "diff":
		cwd := ""
		if len(args) >= 2 {
			cwd = args[1]
		}
		return m.runWithApproval("git_diff", "git diff", nil, func() tea.Cmd {
			res := m.toolkit.GitDiff(cwd)
			m.showResult(res)
			return nil
		})
	case "commit":
		if len(args) < 2 {
			m.errMsg = "usage: /git commit <message>"
			return m, nil
		}
		msg := strings.Join(args[1:], " ")
		desc := fmt.Sprintf("git commit -m %q", msg)
		return m.runWithApproval("git_commit", desc, nil, func() tea.Cmd {
			res := m.toolkit.GitCommit("", msg)
			m.showResult(res)
			return nil
		})
	default:
		m.errMsg = fmt.Sprintf("unknown git subcommand: %s", sub)
		return m, nil
	}
}

func (m *Model) runWithApproval(tool, description string, params map[string]any, fn func() tea.Cmd) (tea.Model, tea.Cmd) {
	policy := m.cfg.Approval.For(tool)
	switch policy {
	case approval.LevelNever:
		m.errMsg = fmt.Sprintf("tool %s is set to never", tool)
		m.updateViewport()
		return m, nil
	case approval.LevelShadow:
		m.addSystem(fmt.Sprintf("[shadow] %s", description))
		return m, nil
	case approval.LevelAsk:
		m.confirming = true
		m.confirmText = fmt.Sprintf("Allow %s? [y/n]", description)
		m.onConfirmYes = fn
		m.onConfirmNo = func() tea.Cmd {
			m.addSystem("cancelled")
			return nil
		}
		m.updateViewport()
		return m, nil
	default:
		return m, fn()
	}
}

func (m *Model) renderModels() string {
	var b strings.Builder
	b.WriteString("## Models\n")
	for _, p := range m.registry.List() {
		available := "no"
		if m.router.Available(&p) {
			available = "yes"
		}
		b.WriteString(fmt.Sprintf("- %s (%s) | %s | key: %s | available: %s\n", p.ID, p.Name, p.ProviderKind, p.APIKeyCredential, available))
	}
	return b.String()
}

func (m *Model) handleTodo(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.addSystem(m.todos.Render())
		return m, nil
	}

	sub := args[0]
	switch sub {
	case "add", "a":
		if len(args) < 2 {
			m.errMsg = "usage: /todo add <text>"
			return m, nil
		}
		text := strings.Join(args[1:], " ")
		id := m.todos.Add(text)
		_ = m.todoStore.Save(m.todos)
		m.addSystem(fmt.Sprintf("Added todo %s: %s", id, text))
	case "done", "d":
		if len(args) < 2 {
			m.errMsg = "usage: /todo done <id>"
			return m, nil
		}
		if m.todos.MarkDone(args[1]) {
			_ = m.todoStore.Save(m.todos)
			m.addSystem(fmt.Sprintf("Marked %s done", args[1]))
		} else {
			m.errMsg = fmt.Sprintf("todo %s not found", args[1])
		}
	case "remove", "rm":
		if len(args) < 2 {
			m.errMsg = "usage: /todo remove <id>"
			return m, nil
		}
		if m.todos.Remove(args[1]) {
			_ = m.todoStore.Save(m.todos)
			m.addSystem(fmt.Sprintf("Removed %s", args[1]))
		} else {
			m.errMsg = fmt.Sprintf("todo %s not found", args[1])
		}
	case "clear":
		m.todos.Clear()
		_ = m.todoStore.Save(m.todos)
		m.addSystem("Cleared todos")
	case "list", "ls":
		m.addSystem(m.todos.Render())
	default:
		m.errMsg = "usage: /todo [add|done|remove|clear|list]"
	}
	m.updateViewport()
	return m, nil
}

func (m *Model) showResult(res tools.Result) {
	var b strings.Builder
	if res.ExitCode != 0 {
		b.WriteString(fmt.Sprintf("exit code: %d\n", res.ExitCode))
	}
	if res.Error != "" {
		b.WriteString(fmt.Sprintf("error: %s\n", res.Error))
	}
	if res.Stdout != "" {
		b.WriteString(truncate(res.Stdout, 6000))
	}
	if res.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(truncate(res.Stderr, 2000))
	}
	m.addSystem(b.String())
}

func (m *Model) addSystem(text string) {
	m.session.Messages = append(m.session.Messages, memory.Message{
		Role:      "system",
		Content:   text,
		Timestamp: time.Now().UTC(),
	})
	m.updateViewport()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}

func (m *Model) startAgent(input string) tea.Cmd {
	m.streaming = true
	m.pending = ""
	m.errMsg = ""
	m.startTime = time.Now()
	m.currentStep = "starting"
	m.elapsed = 0
	m.totalUsage = openai.Usage{}

	m.agentOut = make(chan agent.Event)
	m.agentApproval = make(chan bool)

	cfg := m.cfg
	reg := m.registry
	runner := agent.New(cfg, reg, m.toolkit)
	sess := m.session

	go runner.Run(context.Background(), m.profile, input, sess, m.agentOut, m.agentApproval)

	return m.agentWait()
}

func (m *Model) agentWait() tea.Cmd {
	return func() tea.Msg {
		if m.agentOut == nil {
			return agent.Event{Type: "done"}
		}
		ev, ok := <-m.agentOut
		if !ok {
			m.agentOut = nil
			return agent.Event{Type: "done"}
		}
		return ev
	}
}

func (m Model) handleAgentEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	m.currentStep = ev.Step
	m.elapsed = ev.Elapsed
	m.totalUsage.PromptTokens += ev.Usage.PromptTokens
	m.totalUsage.CompletionTokens += ev.Usage.CompletionTokens
	m.totalUsage.TotalTokens += ev.Usage.TotalTokens

	switch ev.Type {
	case "step":
		m.updateViewport()
		return m, m.agentWait()

	case "tool_call":
		if ev.ToolCall == nil {
			return m, m.agentWait()
		}
		m.currentStep = fmt.Sprintf("tool: %s", ev.ToolCall.Name)
		desc := fmt.Sprintf("%s(%s)", ev.ToolCall.Name, string(ev.ToolCall.Arguments))
		policy := m.cfg.Approval.For(ev.ToolCall.Name)

		switch policy {
		case approval.LevelAlways:
			m.addSystem(fmt.Sprintf("Calling %s", desc))
			if m.agentApproval != nil {
				m.agentApproval <- true
			}
			return m, m.agentWait()
		case approval.LevelNever:
			m.addSystem(fmt.Sprintf("Denied %s", desc))
			if m.agentApproval != nil {
				m.agentApproval <- false
			}
			return m, m.agentWait()
		case approval.LevelShadow:
			m.addSystem(fmt.Sprintf("[shadow] %s", desc))
			if m.agentApproval != nil {
				m.agentApproval <- false
			}
			return m, m.agentWait()
		default:
			m.confirming = true
			m.confirmText = fmt.Sprintf("Allow %s? [y/n]", desc)
			m.onConfirmYes = func() tea.Cmd {
				if m.agentApproval != nil {
					m.agentApproval <- true
				}
				return m.agentWait()
			}
			m.onConfirmNo = func() tea.Cmd {
				if m.agentApproval != nil {
					m.agentApproval <- false
				}
				return m.agentWait()
			}
			m.updateViewport()
			return m, nil
		}

	case "tool_result":
		if ev.ToolResult == nil {
			return m, m.agentWait()
		}
		content := ev.ToolResult.Content
		if ev.ToolResult.Error != "" {
			content = fmt.Sprintf("error: %s", ev.ToolResult.Error)
		}
		m.addSystem(fmt.Sprintf("Tool %s result:\n%s", ev.ToolResult.Name, truncate(content, 2000)))
		return m, m.agentWait()

	case "message":
		m.pending += ev.Content
		m.updateViewport()
		return m, m.agentWait()

	case "usage":
		m.updateViewport()
		return m, m.agentWait()

	case "done":
		m.streaming = false
		m.currentStep = ""
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

	case "error":
		m.streaming = false
		m.currentStep = ""
		m.errMsg = ev.Error
		m.updateViewport()
		return m, nil
	}

	return m, m.agentWait()
}

func (m *Model) startStream() tea.Cmd {
	m.streaming = true
	m.pending = ""
	m.errMsg = ""
	m.startTime = time.Now()
	m.currentStep = "streaming"
	m.elapsed = 0
	m.totalUsage = openai.Usage{}

	ch := make(chan llm.StreamEvent)
	m.streamChan = ch

	client := llm.New(m.profile, m.creds)
	go client.ChatStream(context.Background(), m.session.Messages, ch)

	return waitForStream(ch)
}

func waitForStream(ch chan llm.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return llm.StreamEvent{Done: true}
		}
		return ev
	}
}

func (m Model) handleStreamEvent(ev llm.StreamEvent) (tea.Model, tea.Cmd) {
	if ev.Err != nil {
		m.streaming = false
		m.errMsg = ev.Err.Error()
		m.currentStep = ""
		m.updateViewport()
		return m, nil
	}

	if ev.Done {
		m.streaming = false
		m.currentStep = ""
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
	if m.confirming {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(m.confirmText))
		b.WriteString("\n")
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
		return infoStyle.Render(m.Content)
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

	var status strings.Builder
	status.WriteString(fmt.Sprintf(" moz %s | mode: %s | agent: %v | model: %s ", version.Version, m.mode, m.agentEnabled, m.profile.Name))

	if m.currentStep != "" {
		status.WriteString(fmt.Sprintf("| step: %s ", m.currentStep))
	}
	if m.elapsed > 0 {
		status.WriteString(fmt.Sprintf("| time: %s ", m.elapsed.Round(time.Millisecond)))
	}
	if m.totalUsage.TotalTokens > 0 {
		status.WriteString(fmt.Sprintf("| tokens: %d ", m.totalUsage.TotalTokens))
	}
	if costStr := cost.Format(m.profile.ID, m.totalUsage); costStr != "" {
		status.WriteString(fmt.Sprintf("| cost: %s ", costStr))
	}
	if m.todos.PendingCount() > 0 {
		status.WriteString(fmt.Sprintf("| todos: %d ", m.todos.PendingCount()))
	}

	bar := statusStyle.Width(m.viewport.Width).Render(status.String())

	return fmt.Sprintf(
		"%s\n%s\n%s",
		m.viewport.View(),
		m.textarea.View(),
		bar,
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
