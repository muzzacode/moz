package approval

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

type Level string

const (
	LevelAlways Level = "always"
	LevelAsk    Level = "ask"
	LevelShadow Level = "shadow"
	LevelNever  Level = "never"
)

type Policy struct {
	Read  Level `yaml:"read"`
	Write Level `yaml:"write"`
	Edit  Level `yaml:"edit"`
	Exec  Level `yaml:"exec"`
	Git   Level `yaml:"git"`
}

func Default() *Policy {
	return &Policy{
		Read:  LevelAlways,
		Write: LevelAsk,
		Edit:  LevelAsk,
		Exec:  LevelAsk,
		Git:   LevelAlways,
	}
}

func (p *Policy) ensureDefaults() {
	if p.Read == "" {
		p.Read = LevelAlways
	}
	if p.Write == "" {
		p.Write = LevelAsk
	}
	if p.Edit == "" {
		p.Edit = LevelAsk
	}
	if p.Exec == "" {
		p.Exec = LevelAsk
	}
	if p.Git == "" {
		p.Git = LevelAlways
	}
}

func (p *Policy) For(tool string) Level {
	p.ensureDefaults()
	switch tool {
	// spawn_agents is grouped with reads because sub-agents are read-only and
	// cannot change anything. Approval exists to gate destructive actions.
	case "read_file", "list_dir", "grep", "find_files", "outline",
		"git_status", "git_diff", "web_search", "web_fetch",
		"add_todo", "list_todos", "mark_done", "spawn_agents":
		return p.Read
	case "write_file":
		return p.Write
	case "edit_file":
		return p.Edit
	case "exec", "bash":
		return p.Exec
	case "git_commit", "git_push", "git_pull":
		return p.Git
	default:
		return p.Exec
	}
}

type Action struct {
	Tool        string
	Description string
	Destructive bool
}

func IsDestructive(action Action) bool {
	if action.Destructive {
		return true
	}
	switch action.Tool {
	case "write_file", "edit_file", "exec", "bash", "git_commit", "git_push", "git_pull":
		return true
	default:
		return false
	}
}

func DescribeTool(tool string, params map[string]any) string {
	switch tool {
	case "read_file":
		return fmt.Sprintf("read %v", params["path"])
	case "write_file":
		return fmt.Sprintf("write %v", params["path"])
	case "edit_file":
		return fmt.Sprintf("edit %v", params["path"])
	case "list_dir":
		return fmt.Sprintf("list %v", params["path"])
	case "grep":
		return fmt.Sprintf("grep %q in %v", params["pattern"], params["path"])
	case "exec", "bash":
		return fmt.Sprintf("run %q", params["command"])
	case "git_status":
		return fmt.Sprintf("git status in %v", params["cwd"])
	case "git_diff":
		return fmt.Sprintf("git diff in %v", params["cwd"])
	default:
		return fmt.Sprintf("%s %v", tool, params)
	}
}

// OpenEditor opens the user's default editor for a file path.
// Not used in Phase 3a, but kept as a hook for later.
func OpenEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vim"
		}
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
