package tui

import (
	"context"
	"errors"
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
	"github.com/muzzacode/moz/internal/checkpoint"
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
	cfg         *config.Config
	registry    *models.Registry
	store       *memory.Store
	session     *memory.Session
	creds       *credentials.Manager
	router      *adaptive.Router
	toolkit     *tools.Toolkit
	todos       *todo.List
	todoStore   *todo.Store
	checkpoints *checkpoint.Store

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

	// cancel aborts the in-flight agent task or stream. Non-nil only while a
	// task is running.
	cancel context.CancelFunc
}

// busy reports whether a model request or agent task is in flight.
func (m *Model) busy() bool {
	return m.streaming || m.cancel != nil
}

// abort cancels the in-flight task without exiting the application.
func (m *Model) abort(reason string) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	// Release the agent if it is blocked waiting on an approval decision.
	if m.agentApproval != nil {
		select {
		case m.agentApproval <- false:
		default:
		}
	}
	// Once aborted the update loop stops calling agentWait, so nothing would
	// consume the agent's remaining events and its goroutine would block
	// forever on send. Drain until it closes the channel.
	if m.agentOut != nil {
		go drain(m.agentOut, m.agentApproval)
		m.agentOut = nil
		m.agentApproval = nil
	}
	m.streaming = false
	m.confirming = false
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
	m.addSystem(reason)
	m.updateViewport()
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

func New(cfg *config.Config, registry *models.Registry, store *memory.Store, initial ...*memory.Session) (*Model, error) {
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
	checkpoints := checkpoint.New()
	toolkit.Checkpoints = checkpoints

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
	if len(initial) > 0 && initial[0] != nil {
		sess = initial[0]
	}

	return &Model{
		cfg:          cfg,
		registry:     registry,
		store:        store,
		session:      sess,
		creds:        creds,
		router:       router,
		toolkit:      toolkit,
		checkpoints:  checkpoints,
		todos:        todos,
		todoStore:    todoStore,
		profile:      profile,
		mode:         mode,
		agentEnabled: cfg.Agent,
		textarea:     ta,
		viewport:     vp,
		streamChan:   make(chan llm.StreamEvent),
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
		// Esc interrupts the running task rather than killing the session, so a
		// long agent run can be abandoned without losing the conversation.
		case msg.Type == tea.KeyEsc:
			if m.busy() {
				m.abort("Interrupted")
				return m, nil
			}
			m.saveSession()
			return m, tea.Quit
		case msg.Type == tea.KeyCtrlC:
			if m.busy() {
				m.abort("Interrupted (press Ctrl+C again to quit)")
				return m, nil
			}
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

	case "/undo":
		if m.busy() {
			m.errMsg = "cannot undo while a task is running; press esc first"
			return m, nil
		}
		actions, err := m.checkpoints.UndoLast()
		if err != nil && len(actions) == 0 {
			m.errMsg = err.Error()
			return m, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Undid %d file change(s):", len(actions))
		for _, a := range actions {
			b.WriteString("\n  " + a)
		}
		if err != nil {
			fmt.Fprintf(&b, "\n  (partial: %v)", err)
		}
		m.addSystem(b.String())
		return m, nil

	case "/sessions":
		infos, err := m.store.SessionInfos()
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		if len(infos) == 0 {
			m.addSystem("No saved sessions")
			return m, nil
		}
		var b strings.Builder
		b.WriteString("Saved sessions:\n")
		for _, info := range infos {
			fmt.Fprintf(&b, "%s | %s | %d messages", info.ID, info.Started.Local().Format("2006-01-02 15:04"), info.Messages)
			if info.Preview != "" {
				fmt.Fprintf(&b, " | %s", info.Preview)
			}
			b.WriteByte('\n')
		}
		m.addSystem(strings.TrimSpace(b.String()))
		return m, nil

	case "/resume":
		id := "latest"
		if len(args) > 0 {
			id = args[0]
		}
		m.saveSession()
		var sess *memory.Session
		var err error
		if id == "latest" {
			infos, listErr := m.store.SessionInfos()
			if listErr != nil {
				err = listErr
			} else {
				for _, info := range infos {
					if info.ID != m.session.ID {
						sess, err = m.store.LoadSession(info.ID)
						break
					}
				}
				if sess == nil && err == nil {
					err = fmt.Errorf("no previous saved session")
				}
			}
		} else {
			sess, err = m.store.LoadSession(id)
		}
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.session = sess
		m.pending = ""
		m.streaming = false
		m.errMsg = ""
		m.updateViewport()
		m.addSystem(fmt.Sprintf("Resumed session: %s", sess.ID))
		return m, nil

	case "/clear", "/new":
		m.saveSession()
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
			res, err := m.toolkit.Grep(pattern, path)
			if err != nil {
				m.errMsg = err.Error()
				m.updateViewport()
				return nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d matches in %d files", len(res.Matches), res.FilesScanned)
			if res.Truncated {
				b.WriteString(" (truncated)")
			}
			b.WriteString("\n")
			for _, match := range res.Matches {
				rel, relErr := filepath.Rel(path, match.File)
				if relErr != nil || rel == "" {
					rel = match.File
				}
				fmt.Fprintf(&b, "%s:%d: %s\n", rel, match.Line, match.Content)
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
		m.checkpoints.Begin("/write " + path)
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
		m.checkpoints.Begin("/edit " + path)
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
		m.addSystem("Commands: /models, /model, /mode, /agent, /memory, /sessions, /resume, /new, /clear, /undo, /read, /list, /grep, /run, /write, /edit, /git, /fetch, /todo, /set, /exit\nEsc interrupts a running task. /undo reverses the last task's file changes.")
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

	// Group every file change made by this task so /undo reverses the task as
	// a unit rather than one edit at a time.
	m.checkpoints.Begin(truncate(input, 60))

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	go runner.Run(ctx, m.profile, input, sess, m.agentOut, m.agentApproval)

	return m.agentWait()
}

// agentWait returns a command that blocks for the next agent event.
//
// The channel is captured when the command is built, not read inside the
// closure. Bubble Tea runs commands on their own goroutines, so touching
// m.agentOut in there would race with abort clearing it. For the same reason
// the closure never mutates model state; only Update may do that.
func (m *Model) agentWait() tea.Cmd {
	ch := m.agentOut
	if ch == nil {
		return func() tea.Msg { return agent.Event{Type: "done"} }
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
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
		desc := m.describeToolCall(ev.ToolCall)
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
			m.confirmText = formatConfirm(desc)
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
		m.finishTask()
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

	case "warning", "compacted", "verified":
		m.addSystem(ev.Step)
		return m, m.agentWait()

	case "cancelled":
		m.finishTask()
		m.addSystem("Task cancelled")
		m.updateViewport()
		return m, nil

	case "error":
		m.finishTask()
		m.errMsg = ev.Error
		m.updateViewport()
		return m, nil
	}

	return m, m.agentWait()
}

// drain consumes a cancelled agent's remaining events so its goroutine can
// reach its deferred close instead of blocking on an unread channel. Approvals
// are auto-denied because no user is watching any more.
func drain(events <-chan agent.Event, approvals chan bool) {
	for ev := range events {
		if ev.Type != "tool_call" || approvals == nil {
			continue
		}
		select {
		case approvals <- false:
		default:
		}
	}
}

// finishTask clears in-flight task state and releases the cancel function.
func (m *Model) finishTask() {
	m.streaming = false
	m.currentStep = ""
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
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

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	client := llm.New(m.profile, m.creds).WithTimeout(m.cfg.RequestTimeout())
	go client.ChatStream(ctx, m.session.Messages, ch)

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
		m.finishTask()
		// A cancelled request is a user action, not an error to report.
		if errors.Is(ev.Err, context.Canceled) {
			m.updateViewport()
			return m, nil
		}
		m.errMsg = ev.Err.Error()
		m.updateViewport()
		return m, nil
	}

	if ev.Done {
		m.finishTask()
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
	// Surface the interrupt affordance only while it applies.
	if m.busy() {
		status.WriteString("| esc: interrupt ")
	}

	bar := statusStyle.Width(m.viewport.Width).Render(status.String())

	return fmt.Sprintf(
		"%s\n%s\n%s",
		m.viewport.View(),
		m.textarea.View(),
		bar,
	)
}

func Run(cfg *config.Config, registry *models.Registry, store *memory.Store, initial ...*memory.Session) error {
	m, err := New(cfg, registry, store, initial...)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
