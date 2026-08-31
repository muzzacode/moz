package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/muzzacode/moz/internal/index"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

var toolAliases = map[string]string{
	"read_file":      "read_file",
	"read":           "read_file",
	"list_dir":       "list_dir",
	"list_directory": "list_dir",
	"ls":             "list_dir",
	"grep":           "grep",
	"search":         "grep",
	"search_files":   "grep",
	"find_files":     "find_files",
	"find_file":      "find_files",
	"glob":           "find_files",
	"outline":        "outline",
	"symbols":        "outline",
	"file_outline":   "outline",
	"exec":           "exec",
	"run":            "exec",
	"command":        "exec",
	"write_file":     "write_file",
	"write":          "write_file",
	"edit_file":      "edit_file",
	"edit":           "edit_file",
	"replace":        "edit_file",
	"git_status":     "git_status",
	"git_diff":       "git_diff",
	"git_commit":     "git_commit",
	"web_search":     "web_search",
	"search_web":     "web_search",
	"web_fetch":      "web_fetch",
	"fetch_url":      "web_fetch",
	"spawn_agents":   "spawn_agents",
	"spawn_agent":    "spawn_agents",
	"run_subagents":  "spawn_agents",
	"subagents":      "spawn_agents",
	"add_todo":       "add_todo",
	"list_todos":     "list_todos",
	"mark_done":      "mark_done",
	"complete_todo":  "mark_done",
}

// renderSearch formats matches as compact grep-style lines. This is far cheaper
// in tokens than JSON, which matters because search results are among the
// largest things the agent reads.
func renderSearch(res *index.SearchResult) string {
	if len(res.Matches) == 0 {
		return fmt.Sprintf("no matches (%d files searched)", res.FilesScanned)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es) in %d file(s)", len(res.Matches), res.FilesScanned)
	if res.Truncated {
		b.WriteString(" [truncated: narrow the pattern or use include]")
	}
	b.WriteString("\n")
	for _, m := range res.Matches {
		fmt.Fprintf(&b, "%s:%d: %s\n", m.File, m.Line, m.Content)
	}
	return b.String()
}

// ResolveName maps an alias to its canonical tool name.
func ResolveName(name string) string { return resolveToolName(name) }

func resolveToolName(name string) string {
	if n, ok := toolAliases[strings.ToLower(strings.TrimSpace(name))]; ok {
		return n
	}
	return name
}

func (tk *Toolkit) Execute(call ToolCall) ToolResult {
	call.Name = resolveToolName(call.Name)
	tr := ToolResult{ID: call.ID, Name: call.Name}

	// Enforced here rather than at the call sites so a read-only toolkit cannot
	// be bypassed by any caller.
	if tk.ReadOnly && mutatingTools[call.Name] {
		tr.Error = fmt.Sprintf("%s is not available: this agent is read-only and cannot modify anything", call.Name)
		return tr
	}

	switch call.Name {
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		content, err := tk.ReadFile(args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = content

	case "list_dir":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		entries, err := tk.ListDir(args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		tr.Content = string(data)

	case "grep":
		var args struct {
			Pattern    string `json:"pattern"`
			Path       string `json:"path"`
			Include    string `json:"include"`
			IgnoreCase bool   `json:"ignore_case"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		res, err := tk.GrepWithOptions(args.Pattern, args.Path, index.SearchOptions{
			Include:    args.Include,
			IgnoreCase: args.IgnoreCase,
		})
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = renderSearch(res)

	case "find_files":
		var args struct {
			Query string `json:"query"`
			Path  string `json:"path"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		paths, err := tk.FindFiles(args.Query, args.Path, args.Limit)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		if len(paths) == 0 {
			tr.Content = fmt.Sprintf("no files matching %q", args.Query)
			return tr
		}
		tr.Content = fmt.Sprintf("%d file(s):\n%s", len(paths), strings.Join(paths, "\n"))

	case "outline":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		outline, err := tk.Outline(args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = outline.Render()

	case "exec":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.Exec(args.Command, "")
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)
		if res.Error != "" {
			tr.Error = res.Error
		}

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if err := tk.WriteFile(args.Path, args.Content); err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = fmt.Sprintf("wrote %s", args.Path)

	case "edit_file":
		var args struct {
			Path       string `json:"path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			Old        string `json:"old"`
			New        string `json:"new"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.OldString == "" {
			args.OldString = args.Old
		}
		if args.NewString == "" {
			args.NewString = args.New
		}
		var err error
		if args.ReplaceAll {
			err = tk.EditFileAll(args.Path, args.OldString, args.NewString)
		} else {
			err = tk.EditFile(args.Path, args.OldString, args.NewString)
		}
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = fmt.Sprintf("edited %s", args.Path)

	case "web_search":
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		results, err := tk.WebSearch(args.Query)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		data, _ := json.MarshalIndent(results, "", "  ")
		tr.Content = string(data)

	case "web_fetch":
		var args struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		content, err := tk.WebFetch(args.URL)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = content

	case "add_todo":
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		id := tk.Todos.Add(args.Text)
		tr.Content = fmt.Sprintf("added todo %s", id)

	case "list_todos":
		tr.Content = tk.Todos.Render()

	case "mark_done":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if !tk.Todos.MarkDone(args.ID) {
			tr.Error = fmt.Sprintf("todo %s not found", args.ID)
			return tr
		}
		tr.Content = fmt.Sprintf("marked %s done", args.ID)

	case "git_status":
		var args struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.GitStatus(args.CWD)
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)

	case "git_diff":
		var args struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.GitDiff(args.CWD)
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)

	case "spawn_agents":
		// Handled by the agent, which owns the model client. Reaching here means
		// the interception was bypassed.
		tr.Error = "spawn_agents must be handled by the agent runner"

	default:
		tr.Error = fmt.Sprintf("unknown tool: %s", call.Name)
	}

	return tr
}
