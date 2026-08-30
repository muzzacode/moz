package tools

import (
	"encoding/json"
	"fmt"
	"strings"
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
	"add_todo":       "add_todo",
	"list_todos":     "list_todos",
	"mark_done":      "mark_done",
	"complete_todo":  "mark_done",
}

func resolveToolName(name string) string {
	if n, ok := toolAliases[strings.ToLower(strings.TrimSpace(name))]; ok {
		return n
	}
	return name
}

func (tk *Toolkit) Execute(call ToolCall) ToolResult {
	call.Name = resolveToolName(call.Name)
	tr := ToolResult{ID: call.ID, Name: call.Name}

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
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		matches, err := tk.Grep(args.Pattern, args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		data, _ := json.MarshalIndent(matches, "", "  ")
		tr.Content = string(data)

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
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		// Also accept old/new as a fallback.
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			var fallback struct {
				Path string `json:"path"`
				Old  string `json:"old"`
				New  string `json:"new"`
			}
			if err2 := json.Unmarshal(call.Arguments, &fallback); err2 != nil {
				tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
				return tr
			}
			args.Path = fallback.Path
			args.OldString = fallback.Old
			args.NewString = fallback.New
		}
		if err := tk.EditFile(args.Path, args.OldString, args.NewString); err != nil {
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

	default:
		tr.Error = fmt.Sprintf("unknown tool: %s", call.Name)
	}

	return tr
}
